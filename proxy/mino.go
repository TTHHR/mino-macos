package proxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Mino stream protocol constants (compatible with dxkite.cn/mino/stream/mino v2)
const (
	minoVersion2 = 0x02
)

// minoRequestMessage represents the mino protocol v2 request message.
// Format:
//
//	Version (1B): 0x02
//	Flags (1B):
//	  bit 0: has auth (1 = username/password present)
//	  bit 4: network (0 = tcp, 1 = udp)
//	  bit 5: address type (0 = IPv4, 1 = IPv6) - only if not hostname
//	  bit 6: address type (0 = IP, 1 = hostname)
//	Address (variable): 4B (IPv4), 16B (IPv6), or 1B len + hostname
//	Port (2B): big-endian
//	Auth (variable, optional): ULEN(1B) + PLEN(1B) + Username(ULEN) + Password(PLEN)
type minoRequestMessage struct {
	Network  string
	Address  string
	Username string
	Password string
}

func (m *minoRequestMessage) marshal() ([]byte, error) {
	host, ports, err := net.SplitHostPort(m.Address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(ports)
	if err != nil {
		return nil, err
	}

	var flags uint8 = 0
	if len(m.Username) > 0 {
		flags |= 1 // has auth
	}
	if m.Network == "udp" {
		flags |= 1 << 4
	}

	ip := net.ParseIP(host)
	var isHostname bool
	var addrBytes []byte

	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			addrBytes = ip4 // IPv4 (4 bytes)
		} else {
			flags |= 1 << 5 // IPv6
			addrBytes = ip.To16()
		}
	} else {
		flags |= 1 << 6 // hostname
		isHostname = true
	}

	buf := make([]byte, 0, 2+len(host)+2+2+len(m.Username)+len(m.Password))
	buf = append(buf, minoVersion2, flags)
	if isHostname {
		buf = append(buf, byte(len(host)))
		buf = append(buf, host...)
	} else {
		buf = append(buf, addrBytes...)
	}

	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	buf = append(buf, portBytes[:]...)

	if len(m.Username) > 0 {
		buf = append(buf, byte(len(m.Username)))
		buf = append(buf, byte(len(m.Password)))
		buf = append(buf, m.Username...)
		buf = append(buf, m.Password...)
	}
	return buf, nil
}

// minoResponseMessage represents the mino protocol response.
// 0 = success, non-zero = error message length followed by error string.
type minoResponseMessage struct {
	err error
}

func (m *minoResponseMessage) unmarshal(r io.Reader) error {
	var buf [255]byte
	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return err
	}
	l := buf[0]
	if l > 0 {
		if _, err := io.ReadFull(r, buf[:l]); err != nil {
			return err
		}
		m.err = errors.New(string(buf[:l]))
	}
	return nil
}

// dialMinoUpstream establishes a connection to a mino:// upstream server.
// The flow is:
//  1. TCP connect to upstream host:port
//  2. XOR encoder handshake (if encoder is "xor")
//  3. Mino stream protocol handshake: send version + request message
//  4. Wait for response (0 = success)
//
// Returns the established connection on success.
func dialMinoUpstream(upstreamHost string, encoder string, xorMod int,
	username, password string, targetAddress string, timeoutSec int) (net.Conn, error) {

	log := Log()

	log.Info("dialMinoUpstream: dialing %s (target=%s, encoder=%s, xorMod=%d, user=%s)",
		upstreamHost, targetAddress, encoder, xorMod, username)

	dialer := net.Dialer{Timeout: secondsToDuration(timeoutSec)}
	conn, err := dialer.Dial("tcp", upstreamHost)
	if err != nil {
		log.Error("TCP connect to %s failed: %v", upstreamHost, err)
		return nil, fmt.Errorf("connect to upstream %s: %w", upstreamHost, err)
	}
	log.Info("TCP connected to %s", upstreamHost)

	// Apply XOR encoder if configured
	if encoder == "xor" {
		if xorMod <= 0 {
			xorMod = 4
		}
		log.Info("wrapping connection with XOR encoder (mod=%d)", xorMod)
		conn = newXorClient(conn, xorMod)
		log.Info("XOR encoder applied, handshake will occur on first read/write")
	}

	// Mino stream protocol v2 handshake
	// Send: Version(0x02) + RequestMessage
	req := &minoRequestMessage{
		Network:  "tcp",
		Address:  targetAddress,
		Username: username,
		Password: password,
	}

	data, err := req.marshal()
	if err != nil {
		conn.Close()
		log.Error("mino request marshal failed: %v", err)
		return nil, fmt.Errorf("mino request marshal: %w", err)
	}
	log.Info("sending mino v2 request (%d bytes): target=%s, has_auth=%v",
		len(data), targetAddress, username != "")

	if _, err := conn.Write(data); err != nil {
		conn.Close()
		log.Error("mino request write failed: %v", err)
		return nil, fmt.Errorf("mino request write: %w", err)
	}
	log.Info("mino request sent, waiting for response...")

	// Read response
	// NOTE: We do NOT set read deadline here, because the upstream server
	// needs to connect to the target, which can take a long time for slow
	// targets (especially when the upstream server is in a different region).
	// The HTTP manager's timeout will handle the overall request timeout.
	resp := &minoResponseMessage{}
	if err := resp.unmarshal(conn); err != nil {
		conn.Close()
		log.Error("mino response read failed: %v", err)
		return nil, fmt.Errorf("mino response read: %w", err)
	}

	if resp.err != nil {
		conn.Close()
		log.Error("mino upstream rejected connection: %v", resp.err)
		return nil, fmt.Errorf("mino upstream rejected: %w", resp.err)
	}

	log.Info("mino upstream connection established successfully to %s", targetAddress)
	return conn, nil
}

// parseUpstreamEncoder extracts encoder and xor_mod from upstream URL query.
func parseUpstreamEncoder(upstreamURL string) (encoder string, xorMod int) {
	// Parse query params from upstream URL
	if idx := strings.IndexByte(upstreamURL, '?'); idx >= 0 {
		query := upstreamURL[idx+1:]
		for _, part := range strings.Split(query, "&") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				switch kv[0] {
				case "encoder":
					encoder = kv[1]
				case "xor_mod":
					if v, err := strconv.Atoi(kv[1]); err == nil {
						xorMod = v
					}
				}
			}
		}
	}
	return
}

func secondsToDuration(sec int) time.Duration {
	if sec <= 0 {
		sec = 10
	}
	return time.Duration(sec) * time.Second
}
