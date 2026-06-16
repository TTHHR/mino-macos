package proxy

import (
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"time"
)

// FakeDNS provides a local DNS server that returns fake IPs for all queries.
// The fake IPs are from the 198.18.0.0/15 range (assigned for benchmark/testing).
// This prevents DNS pollution when the proxy handles CONNECT requests,
// and ensures Chrome does not get stuck on DNS resolution for blocked domains.
//
// Domain -> IP mapping is consistent (same domain always gets same IP)
// to avoid SSL certificate issues with IP-based routing at the upstream.

const (
	fakeDNSBase = "\xc6\x12" // 198.18 in big-endian
)

// FakeDNSServer implements a minimal DNS server.
type FakeDNSServer struct {
	addr    string
	conn    *net.UDPConn
	mu      sync.Mutex
	running bool
	quit    chan struct{}
	wg      sync.WaitGroup

	domainCounter uint32
	domainMap     map[string]net.IP
	domainMu      sync.Mutex
}

// NewFakeDNSServer creates a new fake DNS server listening on the given address.
// If addr is empty, defaults to ":53" (requires admin on most systems).
// We recommend using ":5353" or any high port, and configure system DNS to 127.0.0.1:5353.
// For simplicity, this implementation returns 198.18.x.x IPs.
func NewFakeDNSServer(addr string) *FakeDNSServer {
	if addr == "" {
		addr = ":5353"
	}
	return &FakeDNSServer{
		addr:          addr,
		domainMap:     make(map[string]net.IP),
		domainCounter: 1,
	}
}

// Start starts the fake DNS server.
func (d *FakeDNSServer) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.running {
		return nil
	}

	udpAddr, err := net.ResolveUDPAddr("udp", d.addr)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	d.conn = conn

	// Store actual listening address
	actualAddr := conn.LocalAddr().String()

	d.running = true
	d.quit = make(chan struct{})

	Log().Info("FakeDNS listening on %s", actualAddr)

	d.wg.Add(1)
	go d.serve()

	return nil
}

// Addr returns the address the FakeDNS server is listening on.
func (d *FakeDNSServer) Addr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		return d.conn.LocalAddr().String()
	}
	return ""
}

// Stop stops the fake DNS server.
func (d *FakeDNSServer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return
	}

	close(d.quit)
	if d.conn != nil {
		d.conn.Close()
	}
	d.wg.Wait()
	d.running = false
	Log().Info("FakeDNS stopped")
}

func (d *FakeDNSServer) serve() {
	defer d.wg.Done()

	buf := make([]byte, 512)
	for {
		select {
		case <-d.quit:
			return
		default:
		}

		_ = d.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-d.quit:
				return
			default:
				Log().Warn("FakeDNS read error: %v", err)
				continue
			}
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		go d.handleQuery(addr, data)
	}
}

func (d *FakeDNSServer) handleQuery(addr *net.UDPAddr, query []byte) {
	// Parse DNS query
	if len(query) < 12 {
		return
	}

	// Get transaction ID
	txID := binary.BigEndian.Uint16(query[0:2])

	// Check flags: must be a standard query (0x0100 = recursion desired)
	flags := binary.BigEndian.Uint16(query[2:4])
	if flags&0xF800 != 0x0000 { // Not a query
		return
	}

	// Parse question
	qname, qtype, _, ok := parseDNSQuestion(query[12:])
	if !ok || qtype != 1 { // Type A only
		return
	}

	domain := strings.TrimSuffix(qname, ".")
	if domain == "" {
		return
	}

	// Get or create fake IP for this domain
	fakeIP := d.getOrCreateIP(domain)

	Log().Debug("FakeDNS: %s -> %s", domain, fakeIP.String())

	// Build response
	response := d.buildResponse(txID, query, domain, fakeIP)

	_ = d.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := d.conn.WriteToUDP(response, addr)
	if err != nil {
		Log().Warn("FakeDNS write error: %v", err)
	}
}

func (d *FakeDNSServer) getOrCreateIP(domain string) net.IP {
	d.domainMu.Lock()
	defer d.domainMu.Unlock()

	if ip, ok := d.domainMap[domain]; ok {
		return ip
	}

	// Use consistent IP from 198.18.0.0/15 range
	counter := d.domainCounter
	d.domainCounter++

	// 198.18.x.x where x = counter / 256, counter % 256
	ip := net.IPv4(
		198,
		18,
		byte((counter/256)%256),
		byte(counter%256),
	)
	d.domainMap[domain] = ip
	return ip
}

func (d *FakeDNSServer) buildResponse(txID uint16, query []byte, domain string, ip net.IP) []byte {
	// DNS response header
	resp := make([]byte, 0, 512)

	// Transaction ID
	resp = append(resp, byte(txID>>8), byte(txID))

	// Flags: response + recursion desired + recursion available + no error
	// 0x8180 = 10000001 10000000
	resp = append(resp, 0x81, 0x80)

	// Questions: 1
	resp = append(resp, 0x00, 0x01)

	// Answers: 1
	resp = append(resp, 0x00, 0x01)

	// Authority: 0
	resp = append(resp, 0x00, 0x00)

	// Additional: 0
	resp = append(resp, 0x00, 0x00)

	// Copy original question
	resp = append(resp, query[12:]...)

	// Answer: name pointer (0xc00c = point to question name at offset 12)
	resp = append(resp, 0xc0, 0x0c)

	// Type: A (1)
	resp = append(resp, 0x00, 0x01)

	// Class: IN (1)
	resp = append(resp, 0x00, 0x01)

	// TTL: 60 seconds
	resp = append(resp, 0x00, 0x00, 0x00, 60)

	// Data length: 4 bytes
	resp = append(resp, 0x00, 0x04)

	// IP address
	resp = append(resp, ip.To4()...)

	return resp
}

// parseDNSQuestion parses a DNS question section.
// Returns: qname, qtype, qclass, ok
func parseDNSQuestion(data []byte) (string, uint16, uint16, bool) {
	if len(data) < 5 {
		return "", 0, 0, false
	}

	var qname strings.Builder
	pos := 0

	for pos < len(data) {
		length := int(data[pos])
		if length == 0 {
			pos++
			break
		}
		if pos+length >= len(data) {
			return "", 0, 0, false
		}
		if qname.Len() > 0 {
			qname.WriteByte('.')
		}
		qname.Write(data[pos+1 : pos+1+length])
		pos += 1 + length
	}

	if pos+4 > len(data) {
		return "", 0, 0, false
	}

	qtype := binary.BigEndian.Uint16(data[pos:])
	qclass := binary.BigEndian.Uint16(data[pos+2:])

	return qname.String(), qtype, qclass, true
}
