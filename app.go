package main

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"

	"minoMac_wails/model"
	"minoMac_wails/storage"
)

type App struct {
	ctx          context.Context
	urlModel     *model.URLModel
	proxyManager *model.ProxyManager

	mu        sync.RWMutex
	running   bool
	proxyAddr string
	statusMsg string
	errorMsg  string
	uptime    int64
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	st, err := storage.NewFileStorage("minomac_data.enc", storage.DefaultPassphrase)
	if err != nil {
		log.Printf("storage init error: %v", err)
	}

	a.urlModel = model.NewURLModel(st)

	// Use encrypted config storage instead of plaintext JSON
	cfgStorage := model.NewEncryptedJSONConfigStorage("minomac_config.enc", storage.DefaultPassphrase)

	a.proxyManager = model.NewProxyManager(cfgStorage)
	if err := a.proxyManager.Init(); err != nil {
		log.Println("proxy init warning:", err)
	}

	go func() {
		for state := range a.proxyManager.StateChan() {
			a.mu.Lock()
			if state.Running {
				a.running = true
				a.proxyAddr = state.Address
				a.statusMsg = "代理运行中 - " + state.Address
				a.errorMsg = ""
			} else if state.Error != "" {
				a.running = false
				a.proxyAddr = ""
				a.statusMsg = "代理已停止"
				a.errorMsg = "错误: " + state.Error
			} else {
				a.running = false
				a.proxyAddr = ""
				a.statusMsg = "代理已停止"
				a.errorMsg = ""
			}
			a.mu.Unlock()
		}
	}()
}

func (a *App) GetStatus() map[string]interface{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cfg := a.proxyManager.Config()
	return map[string]interface{}{
		"running":        a.running,
		"proxyAddr":      a.proxyAddr,
		"statusMsg":      a.statusMsg,
		"errorMsg":       a.errorMsg,
		"uptime":         a.uptime,
		"localAddress":   cfg.LocalAddress,
		"upstream":       cfg.Upstream,
		"importURLCount": 0,
	}
}

func (a *App) ToggleProxy() map[string]interface{} {
	state := a.proxyManager.State()
	if state.Running {
		if err := a.proxyManager.Stop(); err != nil {
			return map[string]interface{}{"success": false, "message": "停止代理失败: " + err.Error()}
		}
		return map[string]interface{}{"success": true, "message": "代理已停止"}
	}

	// Reload config from storage before starting
	a.proxyManager.ReloadConfig()

	cfg := a.proxyManager.Config()
	if cfg.LocalAddress == "" {
		return map[string]interface{}{"success": false, "message": "请设置监听地址"}
	}

	if err := a.proxyManager.Start(); err != nil {
		return map[string]interface{}{"success": false, "message": "启动代理失败: " + err.Error()}
	}
	return map[string]interface{}{"success": true, "message": "代理已启动"}
}

func (a *App) ImportURL(rawURL string) map[string]interface{} {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return map[string]interface{}{"success": false, "message": "请输入有效的 URL"}
	}

	if strings.HasPrefix(rawURL, "mino://") || strings.HasPrefix(rawURL, "socks5://") || strings.HasPrefix(rawURL, "http://") {
		// Upstream auth is embedded in the URL string itself (user:pass@host)
		// and is parsed by the proxy package. Local proxy auth should be empty
		// so browsers can connect without prompting for credentials.
		upstream := rawURL
		currentCfg := a.proxyManager.Config()
		newCfg := &model.ProxyConfig{
			LocalAddress: currentCfg.LocalAddress,
			Upstream:     upstream,
			Username:     "",
			Password:     "",
			Timeout:      currentCfg.Timeout,
			AutoStart:    currentCfg.AutoStart,
		}
		if err := a.proxyManager.UpdateConfig(newCfg); err != nil {
			log.Println("auto-configure proxy error:", err)
		}
	}

	if err := a.urlModel.ImportURL(rawURL); err != nil {
		return map[string]interface{}{"success": false, "message": "保存失败，请检查日志"}
	}
	return map[string]interface{}{"success": true, "message": "URL 已加密保存，代理配置已更新"}
}

func (a *App) SaveConfig(config map[string]interface{}) map[string]interface{} {
	addr, _ := config["localAddress"].(string)
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return map[string]interface{}{"success": false, "message": "监听地址不能为空"}
	}

	upstream, _ := config["upstream"].(string)
	username, _ := config["username"].(string)
	password, _ := config["password"].(string)

	currentCfg := a.proxyManager.Config()
	cfg := &model.ProxyConfig{
		LocalAddress: addr,
		Upstream:     strings.TrimSpace(upstream),
		Username:     strings.TrimSpace(username),
		Password:     strings.TrimSpace(password),
		Timeout:      currentCfg.Timeout,
		AutoStart:    currentCfg.AutoStart,
	}

	if err := a.proxyManager.UpdateConfig(cfg); err != nil {
		return map[string]interface{}{"success": false, "message": "保存配置失败: " + err.Error()}
	}

	return map[string]interface{}{"success": true, "message": "配置已保存"}
}

func (a *App) GetConfig() map[string]interface{} {
	cfg := a.proxyManager.Config()
	return map[string]interface{}{
		"localAddress": cfg.LocalAddress,
		"upstream":     cfg.Upstream,
		"username":     cfg.Username,
		"password":     cfg.Password,
		"timeout":      cfg.Timeout,
		"autoStart":    cfg.AutoStart,
	}
}

func (a *App) GetImportedURLs() []map[string]interface{} {
	items, err := a.urlModel.ListURLs()
	if err != nil || items == nil {
		return []map[string]interface{}{}
	}
	result := make([]map[string]interface{}, len(items))
	for i, item := range items {
		// Only show the host/IP, never the full URL with credentials
		displayHost := maskURL(item.URL)
		result[i] = map[string]interface{}{
			"host":       displayHost,
			"created_at": item.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}
	return result
}

// maskURL extracts only the host:port from a URL, stripping credentials and scheme.
func maskURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// Simple extraction: find host part after @ or after ://
	host := rawURL
	if idx := strings.LastIndex(rawURL, "@"); idx >= 0 {
		host = rawURL[idx+1:]
	}
	// Remove scheme prefix if present (e.g., "socks5://host" -> "host")
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	// Remove trailing /?query etc
	if idx := strings.IndexAny(host, "/?"); idx >= 0 {
		host = host[:idx]
	}
	if host == "" {
		return "***"
	}
	return host
}

func (a *App) QuitApp() {
	_ = a.proxyManager.Stop()
	os.Exit(0)
}
