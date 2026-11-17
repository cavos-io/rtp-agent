//go:build nativeinprocess && nativeroomintegration

package main

import (
	"context"
	"github.com/cavos-io/rtp-agent/examples/internal/nativeroom"
	"testing"
	"time"
)

func TestStarterInProcessNative(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "10s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := nativeroom.CloseInProcessRuntime(ctx); err != nil {
			t.Error(err)
		}
	}()
	runNativePrimary(t, []string{"session-close", "false-recovery", "publisher-disconnect"})
}

func TestStarterInProcessNativeCaptions(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_CAPTIONS", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
			t.Error(e)
		}
	}()
	runNativePrimary(t, []string{"session-close"})
}

func TestStarterInProcessNativeClientState(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_STATE", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
			t.Error(e)
		}
	}()
	runNativePrimary(t, []string{"session-close"})
}

func TestStarterInProcessNativeDTMF(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_DTMF", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
			t.Error(e)
		}
	}()
	runNativePrimary(t, []string{"end-call"})
}
