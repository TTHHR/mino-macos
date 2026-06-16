package presenter

import (
	"errors"
	"net/url"
	"strings"

	"minoMac_wails/model"
)

// URLImportCallback is called when a URL is imported and contains proxy config info.
type URLImportCallback func(upstream, username, password string)

type URLPresenter struct {
	model    *model.URLModel
	state    bool
	onImport URLImportCallback
}

func NewURLPresenter(m *model.URLModel) *URLPresenter {
	return &URLPresenter{model: m}
}

// SetImportCallback sets a callback that will be invoked when an imported URL
// contains proxy configuration (e.g. mino://user:pass@host:port).
func (p *URLPresenter) SetImportCallback(cb URLImportCallback) {
	p.onImport = cb
}

func (p *URLPresenter) Toggle() {
	p.state = !p.state
}

func (p *URLPresenter) IsEnabled() bool {
	return p.state
}

func (p *URLPresenter) ImportURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "请输入有效的 URL", errors.New("empty url")
	}

	if strings.HasPrefix(raw, "mino://") || strings.HasPrefix(raw, "socks5://") || strings.HasPrefix(raw, "http://") {
		parsed, err := url.Parse(raw)
		if err == nil {
			upstream := raw
			username := ""
			password := ""
			if parsed.User != nil {
				username = parsed.User.Username()
				password, _ = parsed.User.Password()
			}
			if p.onImport != nil {
				p.onImport(upstream, username, password)
			}
		}
	}

	if err := p.model.ImportURL(raw); err != nil {
		return "保存失败，请检查日志", err
	}
	return "URL 已加密保存，代理配置已更新", nil
}

func (p *URLPresenter) GetStatusMessage() string {
	if p.state {
		return "已开启"
	}
	return "已关闭"
}
