package telemetry

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type HttpServer struct {
	server *http.Server
	listen func(network, address string) (net.Listener, error)
	Host   string
	Port   int
}

func NewHttpServer(host string, port int) *HttpServer {
	return NewHttpServerWithListen(host, port, net.Listen)
}

func NewHttpServerWithListen(host string, port int, listen func(network, address string) (net.Listener, error)) *HttpServer {
	if listen == nil {
		listen = net.Listen
	}
	return &HttpServer{
		listen: listen,
		Host:   host,
		Port:   port,
	}
}

func (s *HttpServer) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	ln, err := s.listen("tcp", addr)
	if err != nil {
		return err
	}
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		s.Port = tcpAddr.Port
	}

	s.server = &http.Server{
		Handler: mux,
	}

	go func() {
		logger.Logger.Infow("Starting telemetry HTTP server", "addr", ln.Addr().String())
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Logger.Errorw("Telemetry HTTP server error", err)
		}
	}()

	return nil
}

func (s *HttpServer) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
