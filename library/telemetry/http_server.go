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
	Host   string
	Port   int
}

func NewHttpServer(host string, port int) *HttpServer {
	return &HttpServer{
		Host: host,
		Port: port,
	}
}

func (s *HttpServer) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	ln, err := net.Listen("tcp", addr)
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
