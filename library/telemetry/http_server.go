package telemetry

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cavos-io/conversation-worker/library/logger"
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
	s.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		logger.Logger.Infow("Starting telemetry HTTP server", "addr", addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
