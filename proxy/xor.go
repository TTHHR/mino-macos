package proxy

import (
	"crypto/rand"
	"errors"
	"io"
	"net"
	"sync"
)

// xorVersion is the current XOR protocol version (matches mino encoder/xor).
const xorVersion = 1

// xorConn wraps a net.Conn with XOR encryption/decryption.
// This implements the same protocol as dxkite.cn/mino/encoder/xor.
// Handshake:
//
//	Client -> Server: 'X' (1B) + Version (1B) + XorCode (mod bytes)
//	The XorCode is a random byte sequence used as the XOR key.
//
// NOTE: Read and Write use independent byte counters (rb and wb),
// matching the behavior of dxkite.cn/mino/encoder/xor. This is important
// because different byte positions in the stream are XOR'ed with different
// key bytes, and read/write streams are independent.
type xorConn struct {
	net.Conn
	mod           int
	key           []byte
	readCounter   int64 // independent counter for reads
	writeCounter  int64 // independent counter for writes
	handshakeOnce sync.Once
	handshakeErr  error
}

// newXorClient creates a XOR client connection.
// It will send the XOR handshake on first Read/Write.
func newXorClient(conn net.Conn, mod int) *xorConn {
	if mod <= 0 {
		mod = 4 // default XOR mod
	}
	return &xorConn{
		Conn: conn,
		mod:  mod,
	}
}

// newXorServer creates a XOR server connection.
// It expects the XOR handshake from the client on first Read/Write.
func newXorServer(conn net.Conn, mod int) *xorConn {
	if mod <= 0 {
		mod = 4
	}
	return &xorConn{
		Conn: conn,
		mod:  mod,
	}
}

func (c *xorConn) doHandshakeClient() error {
	log := Log()
	buf := make([]byte, c.mod)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		log.Error("XOR handshake: generate random key failed: %v", err)
		return err
	}
	c.key = make([]byte, c.mod)
	copy(c.key, buf)

	header := []byte{'X', xorVersion}
	header = append(header, buf...)
	log.Info("XOR handshake: sending header (len=%d, version=%d)", len(header), xorVersion)
	_, err := c.Conn.Write(header)
	if err != nil {
		log.Error("XOR handshake: write failed: %v", err)
	} else {
		log.Info("XOR handshake: write succeeded")
	}
	return err
}

func (c *xorConn) doHandshakeServer() error {
	log := Log()
	buf := make([]byte, 2+c.mod)
	if _, err := io.ReadFull(c.Conn, buf); err != nil {
		log.Error("XOR server handshake: read header failed: %v", err)
		return err
	}
	if buf[0] != 'X' {
		log.Error("XOR server handshake: invalid magic byte 0x%02x", buf[0])
		return errors.New("invalid XOR magic byte")
	}
	if buf[1] != xorVersion {
		log.Error("XOR server handshake: unsupported version %d", buf[1])
		return errors.New("unsupported XOR version")
	}
	c.key = make([]byte, c.mod)
	copy(c.key, buf[2:])
	log.Info("XOR server handshake: succeeded (mod=%d)", c.mod)
	return nil
}

func (c *xorConn) handshake() error {
	c.handshakeOnce.Do(func() {
		log := Log()
		log.Info("XOR starting client handshake...")
		c.handshakeErr = c.doHandshakeClient()
		if c.handshakeErr != nil {
			log.Error("XOR client handshake failed: %v", c.handshakeErr)
		}
	})
	return c.handshakeErr
}

func (c *xorConn) Read(b []byte) (int, error) {
	if err := c.handshake(); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(b)
	if n > 0 {
		for i := 0; i < n; i++ {
			b[i] ^= c.key[c.readCounter%int64(c.mod)]
			c.readCounter++
		}
	}
	return n, err
}

func (c *xorConn) Write(b []byte) (int, error) {
	if err := c.handshake(); err != nil {
		return 0, err
	}
	encoded := make([]byte, len(b))
	for i, v := range b {
		encoded[i] = v ^ c.key[c.writeCounter%int64(c.mod)]
		c.writeCounter++
	}
	return c.Conn.Write(encoded)
}
