package proxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// httpProxyManager handles HTTP CONNECT proxy connections.
type httpProxyManager struct {
	username    string
	password    string
	upstreamURL *url.URL
	timeout     time.Duration
}

func newHTTPProxyManager(username, password string, upstream *url.URL, timeout time.Duration) *httpProxyManager {
	return &httpProxyManager{
		username:    username,
		password:    password,
		upstreamURL: upstream,
		timeout:     timeout,
	}
}

func (m *httpProxyManager) handle(conn net.Conn, peekBuf []byte) {
	clog := NewConnLogger(conn.RemoteAddr().String())
	clog.Debug("HTTP handling started")

	req, err := readHTTPRequest(conn, peekBuf)
	if err != nil {
		clog.Warn("read HTTP request error: %v", err)
		_ = conn.Close()
		return
	}
	clog.Info("HTTP request: %s %s (host=%s, port=%s)", req.method, req.raw, req.host, req.port)

	// Check authentication
	if !m.authenticate(req) {
		clog.Warn("auth required but not provided or invalid")
		_, _ = conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"Proxy\"\r\nContent-Length: 0\r\n\r\n"))
		_ = conn.Close()
		return
	}

	// Handle CONNECT method
	if req.method == "CONNECT" {
		clog.Info("HTTP CONNECT tunnel to %s:%s", req.host, req.port)
		m.handleConnect(conn, req)
	} else {
		clog.Info("HTTP plain proxy to %s:%s", req.host, req.port)
		m.handlePlainHTTP(conn, req)
	}
}

type httpRequest struct {
	method   string
	host     string
	port     string
	raw      string
	headers  map[string]string
	username string
	password string
}

func readHTTPRequest(conn net.Conn, peekBuf []byte) (*httpRequest, error) {
	// Create a reader with peeked data
	var reader io.Reader
	if len(peekBuf) > 0 {
		reader = io.MultiReader(strings.NewReader(string(peekBuf)), conn)
	} else {
		reader = conn
	}

	br := bufio.NewReader(reader)

	// Read the request line
	requestLine, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	requestLine = strings.TrimRight(requestLine, "\r\n")

	parts := strings.SplitN(requestLine, " ", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid request line: %s", requestLine)
	}

	req := &httpRequest{
		method:  parts[0],
		raw:     requestLine,
		headers: make(map[string]string),
	}

	// Read headers
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx > 0 {
			key := strings.TrimSpace(line[:colonIdx])
			value := strings.TrimSpace(line[colonIdx+1:])
			req.headers[key] = value
		}
	}

	// Parse host
	if req.method == "CONNECT" {
		req.host = parts[1]
	} else {
		// For plain HTTP, get host from URL or Host header
		if len(parts) > 2 {
			if u, err := url.Parse(parts[1]); err == nil {
				req.host = u.Host
			}
		}
		if req.host == "" {
			req.host = req.headers["Host"]
		}
	}

	// Split host and port
	if strings.Contains(req.host, ":") {
		h, p, err := net.SplitHostPort(req.host)
		if err == nil {
			req.host = h
			req.port = p
		}
	} else {
		req.port = "80"
	}

	// Parse Proxy-Authorization
	if auth := req.headers["Proxy-Authorization"]; auth != "" {
		req.username, req.password = parseBasicAuth(auth)
	}

	return req, nil
}

func parseBasicAuth(auth string) (username, password string) {
	const prefix = "Basic "
	if !strings.HasPrefix(auth, prefix) {
		return "", ""
	}
	decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
	if err != nil {
		return "", ""
	}
	s := string(decoded)
	colonIdx := strings.IndexByte(s, ':')
	if colonIdx < 0 {
		return s, ""
	}
	return s[:colonIdx], s[colonIdx+1:]
}

func (m *httpProxyManager) authenticate(req *httpRequest) bool {
	if m.username == "" && m.password == "" {
		return true // No auth required
	}
	return req.username == m.username && req.password == m.password
}

func (m *httpProxyManager) handleConnect(conn net.Conn, req *httpRequest) {
	clog := NewConnLogger(conn.RemoteAddr().String())
	target := net.JoinHostPort(req.host, req.port)

	if m.upstreamURL != nil {
		clog.Info("tunneling %s via upstream %s", target, m.upstreamURL.Host)
		m.proxyViaUpstream(conn, "tcp", target)
		return
	}

	// Direct connection
	clog.Info("direct CONNECT to %s", target)
	backend, err := net.DialTimeout("tcp", target, m.timeout)
	if err != nil {
		clog.Error("direct CONNECT to %s failed: %v", target, err)
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		_ = conn.Close()
		return
	}
	clog.Info("direct CONNECT to %s succeeded, starting relay", target)

	_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	relay(conn, backend)
}

func (m *httpProxyManager) handlePlainHTTP(conn net.Conn, req *httpRequest) {
	clog := NewConnLogger(conn.RemoteAddr().String())
	target := net.JoinHostPort(req.host, req.port)

	if m.upstreamURL != nil {
		clog.Info("proxying plain HTTP %s via upstream %s", target, m.upstreamURL.Host)
		m.proxyViaUpstream(conn, "tcp", target)
		return
	}

	clog.Info("direct plain HTTP to %s", target)
	backend, err := net.DialTimeout("tcp", target, m.timeout)
	if err != nil {
		clog.Error("direct HTTP to %s failed: %v", target, err)
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		_ = conn.Close()
		return
	}

	// Rewrite the request to be a full URL
	newRequest := req.method + " http://" + net.JoinHostPort(req.host, req.port) + "/" + "\r\n"
	for k, v := range req.headers {
		newRequest += k + ": " + v + "\r\n"
	}
	newRequest += "\r\n"

	_, _ = backend.Write([]byte(newRequest))
	relay(conn, backend)
}

func (m *httpProxyManager) proxyViaUpstream(conn net.Conn, network, address string) {
	clog := NewConnLogger(conn.RemoteAddr().String())

	// Check if upstream is mino:// protocol
	if m.upstreamURL != nil && m.upstreamURL.Scheme == "mino" {
		clog.Info("detected MINO upstream scheme")
		m.proxyViaMinoUpstream(conn, address)
		return
	}

	upstreamHost := m.upstreamURL.Host
	clog.Info("HTTP upstream via %s to target %s", upstreamHost, address)

	backend, err := net.DialTimeout("tcp", upstreamHost, m.timeout)
	if err != nil {
		clog.Error("connect to upstream %s failed: %v", upstreamHost, err)
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		_ = conn.Close()
		return
	}
	clog.Info("connected to upstream %s", upstreamHost)

	// For upstream, we use HTTP CONNECT to the upstream proxy
	upReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", address, address)

	if m.upstreamURL.User != nil {
		user := m.upstreamURL.User.Username()
		pass, _ := m.upstreamURL.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		upReq += "Proxy-Authorization: Basic " + credentials + "\r\n"
		clog.Debug("adding upstream proxy auth: user=%s", user)
	}
	upReq += "\r\n"

	clog.Debug("sending CONNECT request to upstream: %s", upReq[:min(len(upReq), 200)])
	_, _ = backend.Write([]byte(upReq))

	// Read response
	resp := make([]byte, 1024)
	n, _ := backend.Read(resp)
	if n < 12 || string(resp[9:12]) != "200" {
		clog.Warn("upstream CONNECT failed, response: %s", string(resp[:n]))
		_ = conn.Close()
		_ = backend.Close()
		return
	}
	clog.Info("upstream CONNECT succeeded, starting relay")

	// Relay
	_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	relay(conn, backend)
}

func (m *httpProxyManager) proxyViaMinoUpstream(conn net.Conn, address string) {
	clog := NewConnLogger(conn.RemoteAddr().String())
	clog.Info("MINO upstream dial: host=%s target=%s", m.upstreamURL.Host, address)

	upstreamHost := m.upstreamURL.Host

	encoder, xorMod := parseUpstreamEncoder(m.upstreamURL.String())
	clog.Debug("MINO encoder=%s xorMod=%d", encoder, xorMod)

	upstreamUser := ""
	upstreamPass := ""
	if m.upstreamURL.User != nil {
		upstreamUser = m.upstreamURL.User.Username()
		upstreamPass, _ = m.upstreamURL.User.Password()
		clog.Debug("MINO upstream auth: user=%s pass_len=%d", upstreamUser, len(upstreamPass))
	}

	timeoutSec := int(m.timeout.Seconds())

	upstream, err := dialMinoUpstream(upstreamHost, encoder, xorMod,
		upstreamUser, upstreamPass, address, timeoutSec)
	if err != nil {
		clog.Error("MINO upstream dial failed: %v", err)
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		_ = conn.Close()
		return
	}
	clog.Info("MINO upstream connected, starting relay")

	_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	relay(conn, upstream)
}
