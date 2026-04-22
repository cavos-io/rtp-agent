package testutils

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// NewJSONMockServer creates an httptest.Server that returns a fixed JSON response for any request.
func NewJSONMockServer(jsonResponse string, statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write([]byte(jsonResponse))
	}))
}

// NewSSEMockServer creates an httptest.Server that streams SSE chunks.
func NewSSEMockServer(chunks []string, includeDone bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		for _, chunk := range chunks {
			w.Write([]byte("data: " + chunk + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if includeDone {
			w.Write([]byte("data: [DONE]\n\n"))
		}
	}))
}

// NewWebSocketMockServer creates an httptest.Server that upgrades to WebSocket and allows custom handling.
func NewWebSocketMockServer(handler func(conn *websocket.Conn)) *httptest.Server {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler(conn)
	}))
}

// GetWSURL converts an http:// URL to a ws:// URL.
func GetWSURL(httpURL string) string {
	return strings.Replace(httpURL, "http://", "ws://", 1)
}

// RewritingTransport is a RoundTripper that redirects all requests to a target URL.
type RewritingTransport struct {
	TargetURL string
	Base      http.RoundTripper
}

func (t *RewritingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL, _ := url.ParseRequestURI(t.TargetURL)
	req.URL.Scheme = newURL.Scheme
	req.URL.Host = newURL.Host
	return t.Base.RoundTrip(req)
}

// NewRewritingClient creates an http.Client that redirects all requests to targetURL.
func NewRewritingClient(targetURL string) *http.Client {
	return &http.Client{
		Transport: &RewritingTransport{
			TargetURL: targetURL,
			Base:      http.DefaultTransport,
		},
	}
}
