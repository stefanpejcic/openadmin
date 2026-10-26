package server

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Caddy proxies the hostname to reverse_proxy localhost:<port> over plain HTTP, so in TLS mode loopback still gets plain HTTP or every request via 443 is a 400
func serveTLS(srv *http.Server, ln net.Listener, certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		_ = ln.Close()
		return err
	}
	cfg := srv.TLSConfig.Clone()
	cfg.Certificates = []tls.Certificate{cert}
	// Serve only wires up HTTP/2 when the server's own TLSConfig lists h2
	if !slices.Contains(cfg.NextProtos, "h2") {
		cfg.NextProtos = append([]string{"h2", "http/1.1"}, cfg.NextProtos...)
	}
	srv.TLSConfig = cfg
	return srv.Serve(newSniffListener(ln, cfg))
}

// var so tests can treat 127.0.0.1 as a remote client
var isLoopback = func(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

type acceptResult struct {
	conn net.Conn
	err  error
}

// sniffListener gives remote clients TLS like before, loopback connections get TLS or plain HTTP depending on their first byte
type sniffListener struct {
	net.Listener
	cfg       *tls.Config
	results   chan acceptResult
	done      chan struct{}
	closeOnce sync.Once
}

func newSniffListener(inner net.Listener, cfg *tls.Config) *sniffListener {
	l := &sniffListener{Listener: inner, cfg: cfg, results: make(chan acceptResult), done: make(chan struct{})}
	go l.acceptLoop()
	return l
}

func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case r := <-l.results:
		return r.conn, r.err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

func (l *sniffListener) acceptLoop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.push(acceptResult{err: err})
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if !isLoopback(c.RemoteAddr()) {
			l.push(acceptResult{conn: tls.Server(c, l.cfg)})
			continue
		}
		// peek in its own goroutine so a local client that never sends anything can't stall Accept
		go l.sniff(c)
	}
}

func (l *sniffListener) sniff(c net.Conn) {
	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	first, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		_ = c.Close()
		return
	}
	pc := &peekedConn{Conn: c, r: br}
	// 0x16 is a TLS handshake record
	if first[0] == 0x16 {
		l.push(acceptResult{conn: tls.Server(pc, l.cfg)})
		return
	}
	l.push(acceptResult{conn: pc})
}

func (l *sniffListener) push(r acceptResult) {
	select {
	case l.results <- r:
	case <-l.done:
		if r.conn != nil {
			_ = r.conn.Close()
		}
	}
}

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
