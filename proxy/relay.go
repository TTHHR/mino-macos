package proxy

import (
	"io"
	"net"
	"sync"
	"time"
)

// relay copies data bidirectionally between two connections.
func relay(conn1, conn2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		copyConn(conn1, conn2)
	}()
	go func() {
		defer wg.Done()
		copyConn(conn2, conn1)
	}()

	wg.Wait()
}

func copyConn(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()

	buf := make([]byte, 32*1024)
	for {
		_ = src.SetReadDeadline(time.Time{})
		n, err := src.Read(buf)
		if n > 0 {
			_, writeErr := dst.Write(buf[:n])
			if writeErr != nil {
				return
			}
		}
		if err != nil {
			if err == io.EOF {
				return
			}
			// Check if connection is closed
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
	}
}
