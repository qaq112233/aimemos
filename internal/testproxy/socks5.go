// Package testproxy provides local protocol fixtures for outbound proxy tests.
// It is imported only by tests, never by the application.
package testproxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// SOCKS5 forwards to one local backend, while recording the hostname and
// credentials actually received on the wire. The target may be unresolvable
// locally, allowing tests to verify DNS is delegated to the proxy.
type SOCKS5 struct {
	URL         string
	mu          sync.Mutex
	targets     []string
	users       []string
	passwords   []string
	connections []net.Conn
}

func NewSOCKS5(t *testing.T, backend string, auth bool) *SOCKS5 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &SOCKS5{URL: "socks5://" + listener.Addr().String()}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.connections = append(p.connections, conn)
			p.mu.Unlock()
			wg.Go(func() { p.serve(conn, backend, auth) })
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		p.mu.Lock()
		for _, conn := range p.connections {
			_ = conn.Close()
		}
		p.mu.Unlock()
		wg.Wait()
	})
	return p
}

func (p *SOCKS5) Snapshot() (targets, users, passwords []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...), append([]string(nil), p.users...), append([]string(nil), p.passwords...)
}

func (p *SOCKS5) serve(conn net.Conn, backend string, auth bool) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	read := func(n int) []byte {
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil
		}
		return buf
	}
	header := read(2)
	if len(header) != 2 || header[0] != 5 || read(int(header[1])) == nil {
		return
	}
	method := byte(0)
	if auth {
		method = 2
	}
	if _, err := conn.Write([]byte{5, method}); err != nil {
		return
	}
	var user, password string
	if auth {
		h := read(2)
		if len(h) != 2 || h[0] != 1 {
			return
		}
		user = string(read(int(h[1])))
		length := read(1)
		if len(length) != 1 {
			return
		}
		password = string(read(int(length[0])))
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return
		}
	}
	header = read(4)
	if len(header) != 4 || header[0] != 5 || header[1] != 1 {
		return
	}
	var host string
	switch header[3] {
	case 1:
		host = net.IP(read(4)).String()
	case 3:
		length := read(1)
		if len(length) != 1 {
			return
		}
		host = string(read(int(length[0])))
	case 4:
		host = net.IP(read(16)).String()
	default:
		return
	}
	port := read(2)
	if len(port) != 2 {
		return
	}
	p.mu.Lock()
	p.targets = append(p.targets, net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))))
	p.users, p.passwords = append(p.users, user), append(p.passwords, password)
	p.mu.Unlock()
	upstream, err := net.DialTimeout("tcp", backend, time.Second)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprint(conn, "\x05\x00\x00\x01\x7f\x00\x00\x01\x00\x00"); err != nil {
		return
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close(); close(done) }()
	_, _ = io.Copy(conn, upstream)
	_ = conn.Close()
	<-done
}
