//go:build nativeffi

package nativeinprocess

import (
	"context"
	"os"
	"testing"
	"time"
)

// Runs in its own test process: FFI cannot be initialized again after disposal.
// This verifies ABI loading and shutdown without contacting a LiveKit server.
func TestPinnedNativeLibrarySmoke(t *testing.T) {
	path := os.Getenv("LIVEKIT_LIB_PATH")
	if path == "" {
		t.Fatal("LIVEKIT_LIB_PATH required for nativeffi smoke")
	}
	rt, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
