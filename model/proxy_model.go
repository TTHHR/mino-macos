package model

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"

	"golang.org/x/crypto/scrypt"

	"minoMac_wails/proxy"
)

// ProxyManager manages the proxy lifecycle and configuration.
type ProxyManager struct {
	cfg    *ProxyConfig
	proxy  *proxy.Proxy
	mu     sync.RWMutex
	state  ProxyState
	onChan chan ProxyState

	storage ConfigStorage
}

// ConfigStorage defines the interface for storing proxy configuration.
type ConfigStorage interface {
	SaveProxyConfig(*ProxyConfig) error
	LoadProxyConfig() (*ProxyConfig, error)
}

// NewProxyManager creates a new proxy manager.
func NewProxyManager(storage ConfigStorage) *ProxyManager {
	return &ProxyManager{
		cfg:     DefaultProxyConfig(),
		storage: storage,
		onChan:  make(chan ProxyState, 10),
	}
}

// Init loads configuration from storage.
func (pm *ProxyManager) Init() error {
	if pm.storage == nil {
		return nil
	}
	cfg, err := pm.storage.LoadProxyConfig()
	if err != nil {
		pm.cfg = DefaultProxyConfig()
		return nil
	}
	if cfg != nil {
		pm.cfg = cfg
	}
	return nil
}

// Config returns the current proxy configuration.
func (pm *ProxyManager) Config() *ProxyConfig {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.cfg
}

// UpdateConfig updates the proxy configuration.
func (pm *ProxyManager) UpdateConfig(cfg *ProxyConfig) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	wasRunning := pm.state.Running

	if wasRunning {
		if pm.proxy != nil {
			_ = pm.proxy.Stop()
		}
	}

	pm.cfg = cfg

	if pm.storage != nil {
		if err := pm.storage.SaveProxyConfig(cfg); err != nil {
			return err
		}
	}

	if wasRunning {
		return pm.startLocked()
	}

	return nil
}

// Start starts the proxy server.
func (pm *ProxyManager) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.startLocked()
}

func (pm *ProxyManager) startLocked() error {
	if pm.state.Running {
		return nil
	}

	proxyCfg := &proxy.Config{
		Address:                pm.cfg.LocalAddress,
		Upstream:               pm.cfg.Upstream,
		LocalProxyAuthUsername: pm.cfg.Username,
		LocalProxyAuthPassword: pm.cfg.Password,
		Timeout:                pm.cfg.Timeout,
	}

	p := proxy.New(proxyCfg)
	if err := p.Start(); err != nil {
		pm.state = ProxyState{Error: err.Error()}
		pm.notify()
		return err
	}

	pm.proxy = p
	addr := ""
	if p.ListenAddr() != nil {
		addr = p.ListenAddr().String()
	}
	pm.state = ProxyState{Running: true, Address: addr}
	pm.notify()
	return nil
}

// Stop stops the proxy server.
func (pm *ProxyManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if !pm.state.Running {
		return nil
	}

	if pm.proxy != nil {
		if err := pm.proxy.Stop(); err != nil {
			return err
		}
	}

	pm.proxy = nil
	pm.state = ProxyState{Running: false}
	pm.notify()
	return nil
}

// ReloadConfig reloads the proxy configuration from storage.
func (pm *ProxyManager) ReloadConfig() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.storage != nil {
		cfg, err := pm.storage.LoadProxyConfig()
		if err == nil && cfg != nil {
			pm.cfg = cfg
		}
	}
}

// State returns the current proxy state.
func (pm *ProxyManager) State() ProxyState {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.state
}

// StateChan returns a channel that receives state changes.
func (pm *ProxyManager) StateChan() <-chan ProxyState {
	return pm.onChan
}

func (pm *ProxyManager) notify() {
	select {
	case pm.onChan <- pm.state:
	default:
	}
}

// --- Plain JSON Config Storage (deprecated, use Encrypted version) ---

// JSONConfigStorage stores proxy config as JSON.
type JSONConfigStorage struct {
	store func(data []byte) error
	load  func() ([]byte, error)
}

// NewJSONConfigStorage creates a config storage using JSON serialization.
func NewJSONConfigStorage(saveFn func([]byte) error, loadFn func() ([]byte, error)) *JSONConfigStorage {
	return &JSONConfigStorage{store: saveFn, load: loadFn}
}

func (s *JSONConfigStorage) SaveProxyConfig(cfg *ProxyConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.store(data)
}

func (s *JSONConfigStorage) LoadProxyConfig() (*ProxyConfig, error) {
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return DefaultProxyConfig(), nil
	}
	var cfg ProxyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// --- Encrypted Config Storage (AES-GCM) ---

const configSalt = "minomac-config-salt"

// EncryptedJSONConfigStorage stores proxy config as encrypted JSON using AES-GCM.
type EncryptedJSONConfigStorage struct {
	path string
	key  []byte
}

// NewEncryptedJSONConfigStorage creates a config storage that encrypts JSON with AES-GCM.
func NewEncryptedJSONConfigStorage(path string, passphrase string) *EncryptedJSONConfigStorage {
	key, err := deriveConfigKey(passphrase)
	if err != nil {
		return &EncryptedJSONConfigStorage{path: path}
	}
	return &EncryptedJSONConfigStorage{path: path, key: key}
}

func deriveConfigKey(passphrase string) ([]byte, error) {
	const N = 1 << 15
	const r = 8
	const p = 1
	const keyLen = 32
	return scrypt.Key([]byte(passphrase), []byte(configSalt), N, r, p, keyLen)
}

func (s *EncryptedJSONConfigStorage) SaveProxyConfig(cfg *ProxyConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if s.key == nil {
		return os.WriteFile(s.path, data, 0o600)
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := aead.Seal(nonce, nonce, data, nil)
	return os.WriteFile(s.path, ciphertext, 0o600)
}

func (s *EncryptedJSONConfigStorage) LoadProxyConfig() (*ProxyConfig, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultProxyConfig(), nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return DefaultProxyConfig(), nil
	}
	var plaintext []byte
	if s.key != nil {
		block, err := aes.NewCipher(s.key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		nonceSize := aead.NonceSize()
		if len(data) < nonceSize {
			return nil, errors.New("invalid encrypted data")
		}
		nonce, ciphertext := data[:nonceSize], data[nonceSize:]
		plaintext, err = aead.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return nil, err
		}
	} else {
		plaintext = data
	}
	var cfg ProxyConfig
	if err := json.Unmarshal(plaintext, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
