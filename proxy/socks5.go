package proxy

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	socksVersion5          = 0x05
	socksCmdConnect        = 0x01
	socksCmdBind           = 0x02
	socksCmdUDP            = 0x03
	socksAddrIPv4          = 0x01
	socksAddrFQDN          = 0x03
	socksAddrIPv6          = 0x04
	socksAuthNone          = 0x00
	socksAuthPassword      = 0x02
	socksAuthNoAcceptable  = 0xFF
	socksRepSuccess        = 0x00
	socksRepFailure        = 0x01
	socksRepNotAllowed     = 0x02
	socksRepNetworkUnreach = 0x03
	socksRepHostUnreach    = 0x04
	socksRepRefused        = 0x05
	socksRepTTLExpired     = 0x06
	socksRepCmdNotSupp     = 0x07
	socksRepAddrNotSupp    = 0x08
)

// socks5Manager handles SOCKS5 proxy connections.
type socks5Manager struct {
	username    string
	password    string
	upstreamURL *url.URL
	timeout     time.Duration
}

func newSocks5Manager(username, password string, upstream *url.URL, timeout time.Duration) *socks5Manager {
	return &socks5Manager{
		username:    username,
		password:    password,
		upstreamURL: upstream,
		timeout:     timeout,
	}
}

func (m *socks5Manager) handle(conn net.Conn, peekBuf []byte) {
	clog := NewConnLogger(conn.RemoteAddr().String())
	clog.Debug("SOCKS5 handling started")

	// Method selection
	methods, err := m.readMethods(conn, peekBuf)
	if err != nil {
		clog.Warn("read methods error: %v", err)
		_ = conn.Close()
		return
	}
	clog.Debug("client supported methods: %v", methods)

	// Choose authentication method
	var chosenMethod byte = socksAuthNoAcceptable
	if m.username != "" && m.password != "" {
		if hasMethod(methods, socksAuthPassword) {
			chosenMethod = socksAuthPassword
		}
	} else {
		if hasMethod(methods, socksAuthNone) {
			chosenMethod = socksAuthNone
		}
	}

	if chosenMethod == socksAuthNoAcceptable {
		clog.Warn("no acceptable auth method (local_auth=%v)", m.username != "")
		_, _ = conn.Write([]byte{socksVersion5, socksAuthNoAcceptable})
		_ = conn.Close()
		return
	}
	clog.Debug("chosen auth method: 0x%02x", chosenMethod)

	_, _ = conn.Write([]byte{socksVersion5, chosenMethod})

	// Authenticate if needed
	if chosenMethod == socksAuthPassword {
		if err := m.authPassword(conn); err != nil {
			clog.Warn("password auth failed: %v", err)
			_ = conn.Close()
			return
		}
		clog.Info("password auth succeeded")
	}

	// Read request
	network, address, cmd, err := m.readRequest(conn)
	if err != nil {
		clog.Warn("read request error: %v", err)
		_ = conn.Close()
		return
	}
	clog.Info("SOCKS5 request: cmd=0x%02x network=%s target=%s", cmd, network, address)

	if cmd != socksCmdConnect {
		clog.Warn("unsupported command: 0x%02x", cmd)
		m.sendReply(conn, socksRepCmdNotSupp, nil)
		_ = conn.Close()
		return
	}

	m.handleConnect(conn, network, address)
}

func (m *socks5Manager) readMethods(conn net.Conn, peekBuf []byte) ([]byte, error) {
	// If we already read the first byte (version), we need to read the rest
	var headerBuf [2]byte
	var n int
	var err error

	if len(peekBuf) > 0 {
		headerBuf[0] = peekBuf[0]
		n, err = io.ReadFull(conn, headerBuf[1:])
		if err != nil {
			return nil, err
		}
		_ = n
	} else {
		_, err = io.ReadFull(conn, headerBuf[:])
		if err != nil {
			return nil, err
		}
	}

	if headerBuf[0] != socksVersion5 {
		return nil, errors.New("unsupported SOCKS version")
	}

	nMethods := int(headerBuf[1])
	if nMethods > 255 {
		return nil, errors.New("invalid number of methods")
	}

	methods := make([]byte, nMethods)
	_, err = io.ReadFull(conn, methods)
	return methods, err
}

func hasMethod(methods []byte, method byte) bool {
	for _, m := range methods {
		if m == method {
			return true
		}
	}
	return false
}

func (m *socks5Manager) authPassword(conn net.Conn) error {
	// Read auth request: VER(1) + ULEN(1) + UNAME(ULEN) + PLEN(1) + PASSWD(PLEN)
	var buf [2]byte
	if _, err := io.ReadFull(conn, buf[:]); err != nil {
		return err
	}
	ver := buf[0]
	if ver != 0x01 {
		return errors.New("unsupported auth version")
	}
	uLen := int(buf[1])
	uname := make([]byte, uLen)
	if _, err := io.ReadFull(conn, uname); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, buf[:1]); err != nil {
		return err
	}
	pLen := int(buf[0])
	passwd := make([]byte, pLen)
	if _, err := io.ReadFull(conn, passwd); err != nil {
		return err
	}

	if string(uname) == m.username && string(passwd) == m.password {
		_, _ = conn.Write([]byte{0x01, 0x00})
		return nil
	}

	_, _ = conn.Write([]byte{0x01, 0x01})
	return errors.New("authentication failed")
}

func (m *socks5Manager) readRequest(conn net.Conn) (network, address string, cmd byte, err error) {
	// Read VERSION(1) + CMD(1) + RSV(1) + ATYP(1)
	header := make([]byte, 4)
	if _, err = io.ReadFull(conn, header); err != nil {
		return
	}

	if header[0] != socksVersion5 {
		err = errors.New("unsupported SOCKS version in request")
		return
	}

	cmd = header[1]
	atyp := header[3]

	var host string
	var port uint16

	switch atyp {
	case socksAddrIPv4:
		ipv4 := make([]byte, 4)
		if _, err = io.ReadFull(conn, ipv4); err != nil {
			return
		}
		host = net.IP(ipv4).String()

	case socksAddrFQDN:
		var lenBuf [1]byte
		if _, err = io.ReadFull(conn, lenBuf[:]); err != nil {
			return
		}
		domain := make([]byte, lenBuf[0])
		if _, err = io.ReadFull(conn, domain); err != nil {
			return
		}
		host = string(domain)

	case socksAddrIPv6:
		ipv6 := make([]byte, 16)
		if _, err = io.ReadFull(conn, ipv6); err != nil {
			return
		}
		host = net.IP(ipv6).String()

	default:
		err = errors.New("unknown address type")
		return
	}

	var portBuf [2]byte
	if _, err = io.ReadFull(conn, portBuf[:]); err != nil {
		return
	}
	port = binary.BigEndian.Uint16(portBuf[:])

	network = "tcp"
	address = net.JoinHostPort(host, strconv.Itoa(int(port)))
	return
}

func (m *socks5Manager) sendReply(conn net.Conn, rep byte, bindAddr net.Addr) {
	reply := []byte{socksVersion5, rep, 0x00, socksAddrIPv4, 0, 0, 0, 0, 0, 0}
	if bindAddr != nil {
		if addr, ok := bindAddr.(*net.TCPAddr); ok {
			ip4 := addr.IP.To4()
			if ip4 != nil {
				reply[3] = socksAddrIPv4
				copy(reply[4:8], ip4)
			}
			binary.BigEndian.PutUint16(reply[8:10], uint16(addr.Port))
		}
	}
	_, _ = conn.Write(reply)
}

func (m *socks5Manager) handleConnect(conn net.Conn, network, address string) {
	clog := NewConnLogger(conn.RemoteAddr().String())

	if m.upstreamURL != nil {
		clog.Info("connecting via upstream %s://%s to %s",
			m.upstreamURL.Scheme, m.upstreamURL.Host, address)
		// Connect via upstream proxy
		m.connectViaUpstream(conn, network, address)
		return
	}

	// Direct connection
	clog.Info("connecting directly to %s", address)
	backend, err := net.DialTimeout(network, address, m.timeout)
	if err != nil {
		clog.Error("direct connect to %s failed: %v", address, err)
		var repCode byte = socksRepHostUnreach
		if strings.Contains(err.Error(), "refused") {
			repCode = byte(socksRepRefused)
		} else if strings.Contains(err.Error(), "network") {
			repCode = byte(socksRepNetworkUnreach)
		}
		m.sendReply(conn, repCode, nil)
		_ = conn.Close()
		return
	}

	clog.Info("direct connect to %s succeeded, starting relay", address)
	m.sendReply(conn, socksRepSuccess, backend.LocalAddr())
	relay(conn, backend)
}

func (m *socks5Manager) connectViaUpstream(conn net.Conn, network, address string) {
	switch m.upstreamURL.Scheme {
	case "http":
		m.connectViaHTTPUpstream(conn, address)
	case "socks5", "socks":
		m.connectViaSocksUpstream(conn, address)
	case "mino":
		m.connectViaMinoUpstream(conn, address)
	default:
		// Try direct
		m.handleConnect(conn, network, address)
	}
}

func (m *socks5Manager) connectViaHTTPUpstream(conn net.Conn, address string) {
	upstreamHost := m.upstreamURL.Host
	backend, err := net.DialTimeout("tcp", upstreamHost, m.timeout)
	if err != nil {
		m.sendReply(conn, socksRepNetworkUnreach, nil)
		_ = conn.Close()
		return
	}

	// HTTP CONNECT to upstream
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", address, address)

	if m.upstreamURL.User != nil {
		user := m.upstreamURL.User.Username()
		pass, _ := m.upstreamURL.User.Password()
		creds := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		connectReq += "Proxy-Authorization: Basic " + creds + "\r\n"
	}
	connectReq += "\r\n"

	_, _ = backend.Write([]byte(connectReq))

	// Read HTTP response
	resp := make([]byte, 1024)
	n, err := backend.Read(resp)
	if err != nil || n < 12 {
		m.sendReply(conn, socksRepFailure, nil)
		_ = backend.Close()
		_ = conn.Close()
		return
	}

	// Check for "200"
	respStr := string(resp[:n])
	if !strings.Contains(respStr, "200") {
		m.sendReply(conn, socksRepFailure, nil)
		_ = backend.Close()
		_ = conn.Close()
		return
	}

	m.sendReply(conn, socksRepSuccess, backend.LocalAddr())
	relay(conn, backend)
}

func (m *socks5Manager) connectViaSocksUpstream(conn net.Conn, address string) {
	upstreamHost := m.upstreamURL.Host
	backend, err := net.DialTimeout("tcp", upstreamHost, m.timeout)
	if err != nil {
		m.sendReply(conn, socksRepNetworkUnreach, nil)
		_ = conn.Close()
		return
	}

	// SOCKS5 handshake with upstream
	var methods []byte
	if m.upstreamURL.User != nil {
		methods = []byte{socksVersion5, 2, socksAuthNone, socksAuthPassword}
	} else {
		methods = []byte{socksVersion5, 1, socksAuthNone}
	}

	_, _ = backend.Write(methods)

	// Read response
	resp := make([]byte, 2)
	if _, err := io.ReadFull(backend, resp); err != nil || resp[0] != socksVersion5 {
		m.sendReply(conn, socksRepFailure, nil)
		_ = backend.Close()
		_ = conn.Close()
		return
	}

	// Handle auth if needed
	if resp[1] == socksAuthPassword && m.upstreamURL.User != nil {
		user := m.upstreamURL.User.Username()
		pass, _ := m.upstreamURL.User.Password()
		authReq := []byte{0x01, byte(len(user))}
		authReq = append(authReq, []byte(user)...)
		authReq = append(authReq, byte(len(pass)))
		authReq = append(authReq, []byte(pass)...)
		_, _ = backend.Write(authReq)

		authResp := make([]byte, 2)
		if _, err := io.ReadFull(backend, authResp); err != nil || authResp[1] != 0x00 {
			m.sendReply(conn, socksRepNotAllowed, nil)
			_ = backend.Close()
			_ = conn.Close()
			return
		}
	}

	// Send CONNECT to upstream
	host, portStr, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portStr)

	req := []byte{socksVersion5, socksCmdConnect, 0x00}

	ip := net.ParseIP(host)
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, socksAddrIPv4)
			req = append(req, ip4...)
		} else {
			req = append(req, socksAddrIPv6)
			req = append(req, ip.To16()...)
		}
	} else {
		req = append(req, socksAddrFQDN)
		req = append(req, byte(len(host)))
		req = append(req, []byte(host)...)
	}

	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	req = append(req, portBytes...)

	_, _ = backend.Write(req)

	// Read response
	resp2 := make([]byte, 10)
	if _, err := io.ReadFull(backend, resp2[:4]); err != nil {
		m.sendReply(conn, socksRepFailure, nil)
		_ = backend.Close()
		_ = conn.Close()
		return
	}

	if resp2[1] != socksRepSuccess {
		m.sendReply(conn, resp2[1], nil)
		_ = backend.Close()
		_ = conn.Close()
		return
	}

	// Read rest of response (address + port)
	atyp := resp2[3]
	var remainLen int
	switch atyp {
	case socksAddrIPv4:
		remainLen = 4 + 2
	case socksAddrIPv6:
		remainLen = 16 + 2
	case socksAddrFQDN:
		// Need to read domain length first
		if _, err := io.ReadFull(backend, resp2[:1]); err != nil {
			m.sendReply(conn, socksRepFailure, nil)
			_ = backend.Close()
			_ = conn.Close()
			return
		}
		remainLen = int(resp2[0]) + 2
	default:
		remainLen = 4 + 2
	}

	if remainLen > 0 {
		extra := make([]byte, remainLen)
		_, _ = io.ReadFull(backend, extra)
	}

	m.sendReply(conn, socksRepSuccess, backend.LocalAddr())
	relay(conn, backend)
}

func (m *socks5Manager) connectViaMinoUpstream(conn net.Conn, address string) {
	clog := NewConnLogger(conn.RemoteAddr().String())
	clog.Info("MINO upstream connecting to %s target=%s", m.upstreamURL.Host, address)

	upstreamHost := m.upstreamURL.Host

	// Extract encoder and xor_mod from upstream URL query
	encoder, xorMod := parseUpstreamEncoder(m.upstreamURL.String())
	clog.Debug("MINO encoder=%s xorMod=%d", encoder, xorMod)

	// Extract upstream auth from URL
	upstreamUser := ""
	upstreamPass := ""
	if m.upstreamURL.User != nil {
		upstreamUser = m.upstreamURL.User.Username()
		upstreamPass, _ = m.upstreamURL.User.Password()
		clog.Debug("MINO upstream auth: user=%s pass_len=%d", upstreamUser, len(upstreamPass))
	}

	// Use timeout from manager
	timeoutSec := int(m.timeout.Seconds())

	upstream, err := dialMinoUpstream(upstreamHost, encoder, xorMod,
		upstreamUser, upstreamPass, address, timeoutSec)
	if err != nil {
		clog.Error("MINO upstream dial failed: %v", err)
		m.sendReply(conn, socksRepHostUnreach, nil)
		_ = conn.Close()
		return
	}

	clog.Info("MINO upstream connected, starting relay to %s", address)
	m.sendReply(conn, socksRepSuccess, upstream.LocalAddr())
	relay(conn, upstream)
}
