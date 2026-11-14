package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublisherBarrierWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitPublisherBarrier(ctx, path) }()
	select {
	case err := <-done:
		t.Fatalf("released before subscription: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPublisherBarrierCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitPublisherBarrier(ctx, filepath.Join(t.TempDir(), "absent")); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
