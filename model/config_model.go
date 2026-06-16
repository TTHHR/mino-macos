package model

// ProxyConfig represents the proxy configuration.
type ProxyConfig struct {
	// LocalAddress is the local proxy listen address (e.g. ":1080")
	LocalAddress string `json:"local_address"`

	// Upstream is the upstream proxy URL (e.g. "socks5://host:port")
	Upstream string `json:"upstream"`

	// Username for proxy authentication
	Username string `json:"username"`

	// Password for proxy authentication
	Password string `json:"password"`

	// AutoStart indicates whether to start proxy on app launch
	AutoStart bool `json:"auto_start"`

	// Timeout in seconds
	Timeout int `json:"timeout"`
}

// DefaultProxyConfig returns default proxy configuration.
func DefaultProxyConfig() *ProxyConfig {
	return &ProxyConfig{
		LocalAddress: ":1080",
		Timeout:      10,
	}
}

// ProxyState holds the runtime state of the proxy.
type ProxyState struct {
	Running bool
	Address string
	Error   string
}
