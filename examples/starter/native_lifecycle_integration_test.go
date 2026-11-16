//go:build nativeroomintegration

package main

import (
	"context"
	"fmt"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	protologger "github.com/livekit/protocol/logger"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The proxy carries only the native participant's signaling. RTP remains direct.
// Closing an upgraded upstream body drops that WebSocket without touching host
// networking or the caller. A new 101 response is evidence of signal reconnection;
// the shared harness independently checks input, output content and cleanup.
func TestNativePrimarySignalReconnect(t *testing.T) {
	runNativeSignalReconnect(t, 0, false)
}
func TestNativePrimarySignalOutage(t *testing.T) {
	runNativeSignalReconnect(t, 2*time.Second, false)
}
func TestNativePrimarySignalOutageRecovery(t *testing.T) {
	runNativeSignalReconnect(t, 2*time.Second, true)
}
func runNativeSignalReconnect(t *testing.T, outage time.Duration, recovery bool) {
	protologger.InitFromConfig(&protologger.Config{Level: "info", JSON: true}, "reconnect-comparison")
	logutil.SetLogger(protologger.GetLogger())
	target, err := url.Parse(strings.Replace(os.Getenv("NATIVE_TEST_URL"), "ws://", "http://", 1))
	if err != nil || target.Host == "" {
		t.Fatal("local NATIVE_TEST_URL required")
	}
	if target.Hostname() != "127.0.0.1" && target.Hostname() != "localhost" {
		t.Fatal("fault injection requires local server")
	}
	connections := make(chan io.ReadCloser, 8)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.StatusCode == http.StatusSwitchingProtocols {
			select {
			case connections <- r.Body:
			default:
				return fmt.Errorf("unexpected signal connection burst")
			}
		}
		return nil
	}
	var blocked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path only: query strings contain access tokens.
		t.Logf("signal request path=%s blocked=%v", r.URL.Path, blocked.Load())
		if blocked.Load() {
			http.Error(w, "controlled local signaling outage", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("NATIVE_ROOM_DURATION", "10s")
	runNativePrimaryWithSignalFault(t, []string{"native-signal-reconnect"}, strings.Replace(server.URL, "http://", "ws://", 1), func(ctx context.Context) error {
		var original io.ReadCloser
		select {
		case original = <-connections:
		case <-ctx.Done():
			return fmt.Errorf("initial websocket absent: %w", ctx.Err())
		}
		blocked.Store(outage > 0)
		defer blocked.Store(false)
		if err := original.Close(); err != nil {
			return err
		}
		if outage > 0 {
			timer := time.NewTimer(outage)
			defer timer.Stop()
			select {
			case <-timer.C:
				blocked.Store(false)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case <-connections:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("replacement websocket absent: %w", ctx.Err())
		}
	}, recovery)
}

func TestNativePrimaryPublisherSignalReconnect(t *testing.T) {
	t.Setenv("NATIVE_ROOM_DURATION", "10s")
	runNativePrimary(t, []string{"publisher-signal-reconnect"})
}

// Current contract is termination on the selected track's unsubscribe. A new SID
// must not silently attach to the old agent session. This is not seamless recovery.
func TestNativePrimaryTrackReplacementEndsSession(t *testing.T) {
	runNativePrimary(t, []string{"track-republish"})
}
