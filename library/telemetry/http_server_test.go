package telemetry

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestHttpServerStartBindsDynamicMetricsPort(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	listener := newPipeMetricsListener(serverConn, 43210)
	server := NewHttpServerWithListen("127.0.0.1", 0, func(network, address string) (net.Listener, error) {
		if network != "tcp" {
			t.Fatalf("listen network = %q, want tcp", network)
		}
		if address != "127.0.0.1:0" {
			t.Fatalf("listen address = %q, want 127.0.0.1:0", address)
		}
		return listener, nil
	})
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop(context.Background())

	if server.Port != 43210 {
		t.Fatalf("Port = %d after Start(), want assigned listener port 43210", server.Port)
	}

	client := &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
		},
	}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(server.Port) + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", resp.StatusCode)
	}
}

type pipeMetricsListener struct {
	once   sync.Once
	connC  chan net.Conn
	closeC chan struct{}
	addr   net.Addr
}

func newPipeMetricsListener(conn net.Conn, port int) *pipeMetricsListener {
	l := &pipeMetricsListener{
		connC:  make(chan net.Conn, 1),
		closeC: make(chan struct{}),
		addr:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port},
	}
	l.connC <- conn
	return l
}

func (l *pipeMetricsListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connC:
		return conn, nil
	case <-l.closeC:
		return nil, net.ErrClosed
	}
}

func (l *pipeMetricsListener) Close() error {
	l.once.Do(func() {
		close(l.closeC)
	})
	return nil
}

func (l *pipeMetricsListener) Addr() net.Addr {
	return l.addr
}
