package presenter

import (
	"errors"
	"strings"

	"minoMac_wails/model"
)

type ProxyPresenter struct {
	manager  *model.ProxyManager
	urlModel *model.URLModel

	LocalAddress string
	Upstream     string
	Username     string
	Password     string

	StatusText string
	ErrorText  string

	urlPresenter *URLPresenter
}

func NewProxyPresenter(manager *model.ProxyManager, urlModel *model.URLModel, urlPresenter *URLPresenter) *ProxyPresenter {
	p := &ProxyPresenter{
		manager:      manager,
		urlModel:     urlModel,
		urlPresenter: urlPresenter,
	}

	cfg := manager.Config()
	p.LocalAddress = cfg.LocalAddress
	p.Upstream = cfg.Upstream
	p.Username = cfg.Username
	p.Password = cfg.Password

	p.updateStatus()

	go func() {
		for state := range manager.StateChan() {
			if state.Running {
				p.StatusText = "代理运行中 - " + state.Address
				p.ErrorText = ""
			} else if state.Error != "" {
				p.StatusText = "代理已停止"
				p.ErrorText = "错误: " + state.Error
			} else {
				p.StatusText = "代理已停止"
				p.ErrorText = ""
			}
		}
	}()

	return p
}

func (p *ProxyPresenter) ToggleProxy() (string, error) {
	state := p.manager.State()
	if state.Running {
		if err := p.manager.Stop(); err != nil {
			return "停止代理失败", err
		}
		return "代理已停止", nil
	}

	cfg := p.manager.Config()
	if cfg.LocalAddress == "" {
		return "请设置监听地址", errors.New("empty local address")
	}

	if err := p.manager.Start(); err != nil {
		return "启动代理失败", err
	}
	return "代理已启动", nil
}

func (p *ProxyPresenter) SaveConfig() (string, error) {
	addr := strings.TrimSpace(p.LocalAddress)
	if addr == "" {
		return "监听地址不能为空", errors.New("empty address")
	}

	cfg := &model.ProxyConfig{
		LocalAddress: addr,
		Upstream:     strings.TrimSpace(p.Upstream),
		Username:     strings.TrimSpace(p.Username),
		Password:     strings.TrimSpace(p.Password),
		Timeout:      p.manager.Config().Timeout,
	}

	if err := p.manager.UpdateConfig(cfg); err != nil {
		return "保存配置失败", err
	}

	p.updateStatus()
	return "配置已保存", nil
}

func (p *ProxyPresenter) GetConfig() *model.ProxyConfig {
	return p.manager.Config()
}

func (p *ProxyPresenter) IsProxyRunning() bool {
	return p.manager.State().Running
}

func (p *ProxyPresenter) GetStatusMessage() string {
	return p.StatusText
}

func (p *ProxyPresenter) GetErrorMessage() string {
	return p.ErrorText
}

func (p *ProxyPresenter) updateStatus() {
	state := p.manager.State()
	if state.Running {
		p.StatusText = "代理运行中 - " + state.Address
		p.ErrorText = ""
	} else if state.Error != "" {
		p.StatusText = "代理已停止"
		p.ErrorText = "错误: " + state.Error
	} else {
		p.StatusText = "代理已停止"
		p.ErrorText = ""
	}
}
