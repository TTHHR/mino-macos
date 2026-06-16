// Package proxy provides a lightweight proxy server implementation
// inspired by the mino agent (dxkite.cn/mino), supporting:
// - HTTP CONNECT proxy
// - SOCKS5 proxy
// - Upstream proxy chaining
// - Authentication support
package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"
)

// Config holds proxy configuration.
type Config struct {
	// Local address to listen on, e.g. ":1080"
	Address string

	// Upstream proxy URL, e.g. "socks5://127.0.0.1:8080" or "mino://user:pass@host:port?encoder=xor"
	Upstream string

	// LocalProxyAuthUsername / LocalProxyAuthPassword - sets authentication for the local proxy listener.
	// When empty (default), clients (e.g. Chrome) can connect without auth.
	LocalProxyAuthUsername string
	LocalProxyAuthPassword string

	// Timeout in seconds
	Timeout int

	// Encoder type: "xor", "tls", or empty for none (used for mino:// upstream)
	Encoder string
}

// DefaultConfig returns default proxy configuration.
func DefaultConfig() *Config {
	return &Config{
		Address: ":1080",
		Timeout: 10,
	}
}

// Proxy manages the proxy server lifecycle.
type Proxy struct {
	config *Config

	listener net.Listener
	httpMgr  *httpProxyManager
	socksMgr *socks5Manager
	fakeDNS  *FakeDNSServer
	wg       sync.WaitGroup
	quit     chan struct{}

	mu      sync.RWMutex
	running bool

	// upstream URL parsed
	upstreamURL *url.URL
}

// New creates a new Proxy with the given config.
func New(cfg *Config) *Proxy {
	p := &Proxy{
		config: cfg,
		quit:   make(chan struct{}),
	}
	return p
}

// Start begins listening and serving proxy requests.
func (p *Proxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	log := Log()

	if p.running {
		log.Warn("proxy already running, ignoring start")
		return errors.New("proxy already running")
	}

	// Parse upstream if configured
	if p.config.Upstream != "" {
		u, err := url.Parse(p.config.Upstream)
		if err != nil {
			log.Error("invalid upstream URL %q: %v", p.config.Upstream, err)
			return fmt.Errorf("invalid upstream URL: %w", err)
		}
		p.upstreamURL = u
		log.Info("upstream configured: scheme=%s host=%s has_auth=%v",
			u.Scheme, u.Host, u.User != nil)

		// Detect encoder from URL query (e.g. ?encoder=xor)
		encoderFromURL := u.Query().Get("encoder")
		if encoderFromURL != "" && p.config.Encoder == "" {
			p.config.Encoder = encoderFromURL
			log.Info("encoder detected from URL: %s", encoderFromURL)
		}
	} else {
		log.Info("no upstream configured, acting as direct proxy")
	}

	// Start listening
	listener, err := net.Listen("tcp", p.config.Address)
	if err != nil {
		log.Error("failed to listen on %s: %v", p.config.Address, err)
		return fmt.Errorf("failed to listen on %s: %w", p.config.Address, err)
	}
	p.listener = listener
	log.Info("proxy listening on %s", listener.Addr().String())

	timeout := time.Duration(p.config.Timeout) * time.Second

	// Local proxy managers: auth settings are for local clients (Chrome).
	// When empty, no auth required - Chrome connects without prompting.
	p.httpMgr = newHTTPProxyManager(p.config.LocalProxyAuthUsername, p.config.LocalProxyAuthPassword, p.upstreamURL, timeout)
	p.socksMgr = newSocks5Manager(p.config.LocalProxyAuthUsername, p.config.LocalProxyAuthPassword, p.upstreamURL, timeout)

	// Start FakeDNS server (if upstream is configured, to prevent DNS pollution)
	if p.config.Upstream != "" {
		p.fakeDNS = NewFakeDNSServer(":0")
		if err := p.fakeDNS.Start(); err != nil {
			log.Warn("FakeDNS start failed (non-fatal): %v", err)
		} else {
			log.Info("FakeDNS started on %s", p.fakeDNS.Addr())
		}
	}

	p.running = true

	p.wg.Add(1)
	go p.acceptLoop()

	log.Info("proxy started successfully")
	return nil
}

// Stop gracefully stops the proxy server.
func (p *Proxy) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	log := Log()

	if !p.running {
		return nil
	}

	log.Info("stopping proxy...")
	close(p.quit)

	if p.listener != nil {
		_ = p.listener.Close()
	}
	if p.fakeDNS != nil {
		p.fakeDNS.Stop()
	}

	p.wg.Wait()
	p.running = false
	log.Info("proxy stopped")
	return nil
}

// IsRunning returns whether the proxy is currently running.
func (p *Proxy) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// ListenAddr returns the address the proxy is listening on.
func (p *Proxy) ListenAddr() net.Addr {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.listener != nil {
		return p.listener.Addr()
	}
	return nil
}

func (p *Proxy) acceptLoop() {
	defer p.wg.Done()
	log := Log()

	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.quit:
				return
			default:
				log.Warn("accept error: %v", err)
				continue
			}
		}

		p.wg.Add(1)
		go p.handleConn(conn)
	}
}

func (p *Proxy) handleConn(conn net.Conn) {
	defer p.wg.Done()
	defer conn.Close()

	clog := NewConnLogger(conn.RemoteAddr().String())
	clog.Info("new connection accepted")

	// Set a read deadline for protocol detection
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// Peek at the first byte to detect protocol
	buf := make([]byte, 1)
	n, err := conn.Read(buf)
	if err != nil || n < 1 {
		clog.Warn("read first byte error: %v", err)
		return
	}

	// Reset deadline
	_ = conn.SetReadDeadline(time.Time{})

	// SOCKS5 starts with 0x05
	if buf[0] == 0x05 {
		clog.Info("detected SOCKS5 protocol")
		p.socksMgr.handle(conn, buf[:n])
		return
	}

	// HTTP methods start with letters (G, P, D, H, O, T, C for CONNECT)
	if isHTTPMethod(buf[0]) {
		clog.Info("detected HTTP protocol (method byte: %c)", buf[0])
		p.httpMgr.handle(conn, buf[:n])
		return
	}

	// Unknown protocol
	clog.Warn("unknown protocol, first byte=0x%02x", buf[0])
	_ = conn.Close()
}

func isHTTPMethod(b byte) bool {
	switch b {
	case 'G', 'P', 'D', 'H', 'O', 'T', 'C':
		return true
	}
	return false
}
