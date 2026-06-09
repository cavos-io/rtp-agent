package utils

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestConnectionPoolReusesReturnedConnection(t *testing.T) {
	var next int
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
	})

	conn, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	if conn != 1 || pool.LastConnectionReused {
		t.Fatalf("first Get() conn=%d reused=%v, want conn=1 reused=false", conn, pool.LastConnectionReused)
	}

	pool.Put(conn)
	reused, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() reused error = %v", err)
	}
	if reused != conn || !pool.LastConnectionReused || pool.LastAcquireTime != 0 {
		t.Fatalf("reused Get() conn=%d reused=%v acquire=%v, want conn=%d reused=true acquire=0", reused, pool.LastConnectionReused, pool.LastAcquireTime, conn)
	}
}

func TestConnectionPoolExpiresAndClosesOldConnection(t *testing.T) {
	var next int
	var closed []int
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		MaxSessionDuration: 5 * time.Millisecond,
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
		Close: func(_ context.Context, conn int) error {
			closed = append(closed, conn)
			return nil
		},
	})

	conn, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	pool.Put(conn)
	time.Sleep(10 * time.Millisecond)

	fresh, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() fresh error = %v", err)
	}
	if fresh == conn || pool.LastConnectionReused {
		t.Fatalf("fresh Get() conn=%d reused=%v, want new connection", fresh, pool.LastConnectionReused)
	}
	if len(closed) != 0 {
		t.Fatalf("closed immediately after expiry = %#v, want deferred close", closed)
	}
	pool.Put(fresh)
	reused, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() after deferred close error = %v", err)
	}
	if reused != fresh || !pool.LastConnectionReused {
		t.Fatalf("Get() after deferred close conn=%d reused=%v, want reused fresh conn %d", reused, pool.LastConnectionReused, fresh)
	}
	if !reflect.DeepEqual(closed, []int{conn}) {
		t.Fatalf("closed = %#v, want [%d]", closed, conn)
	}
}

func TestConnectionPoolExpiredCloseErrorDoesNotBlockFreshGet(t *testing.T) {
	var next int
	closeErr := errors.New("close failed")
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		MaxSessionDuration: time.Nanosecond,
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
		Close: func(context.Context, int) error {
			return closeErr
		},
	})

	conn, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	pool.Put(conn)
	time.Sleep(time.Millisecond)

	fresh, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() after expiry error = %v, want fresh connection despite deferred close error", err)
	}
	if fresh == conn || pool.LastConnectionReused {
		t.Fatalf("Get() after expiry conn=%d reused=%v, want fresh connection", fresh, pool.LastConnectionReused)
	}
}

func TestConnectionPoolDeferredCloseErrorDoesNotBlockNextGet(t *testing.T) {
	var next int
	closeErr := errors.New("close failed")
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
		Close: func(context.Context, int) error {
			return closeErr
		},
	})

	conn, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	pool.Remove(conn)

	fresh, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() after deferred close error = %v, want nil despite close failure", err)
	}
	if fresh == conn || pool.LastConnectionReused {
		t.Fatalf("Get() after deferred close conn=%d reused=%v, want fresh connection", fresh, pool.LastConnectionReused)
	}
}

func TestConnectionPoolInvalidateClosesOnNextGet(t *testing.T) {
	var next int
	var closed []int
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
		Close: func(_ context.Context, conn int) error {
			closed = append(closed, conn)
			return nil
		},
	})

	conn, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	pool.Put(conn)
	pool.Invalidate()

	fresh, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() after invalidate error = %v", err)
	}
	if fresh == conn {
		t.Fatalf("Get() after invalidate reused %d, want fresh connection", conn)
	}
	if !reflect.DeepEqual(closed, []int{conn}) {
		t.Fatalf("closed = %#v, want [%d]", closed, conn)
	}
}

func TestConnectionPoolPrewarmCreatesReusableConnection(t *testing.T) {
	var next int
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		ConnectTimeout: time.Second,
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
	})

	pool.Prewarm(context.Background())
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && next == 0 {
		time.Sleep(time.Millisecond)
	}

	conn, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if conn != 1 || !pool.LastConnectionReused {
		t.Fatalf("Get() after prewarm conn=%d reused=%v, want conn=1 reused=true", conn, pool.LastConnectionReused)
	}
}

func TestConnectionPoolCloseCancelsPrewarmConnect(t *testing.T) {
	connectStarted := make(chan struct{})
	connectCanceled := make(chan struct{})
	releaseConnect := make(chan struct{})
	closeReturned := make(chan struct{})

	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		ConnectTimeout: time.Hour,
		Connect: func(ctx context.Context) (int, error) {
			close(connectStarted)
			select {
			case <-ctx.Done():
				close(connectCanceled)
				return 0, ctx.Err()
			case <-releaseConnect:
				return 1, nil
			}
		},
	})

	pool.Prewarm(context.Background())
	select {
	case <-connectStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("prewarm connect did not start")
	}

	go func() {
		_ = pool.Close(context.Background())
		close(closeReturned)
	}()

	select {
	case <-connectCanceled:
	case <-time.After(200 * time.Millisecond):
		close(releaseConnect)
		<-closeReturned
		t.Fatal("Close did not cancel prewarm connect")
	}
	select {
	case <-closeReturned:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close did not return after canceling prewarm connect")
	}
}

func TestConnectionPoolWithConnectionRemovesOnError(t *testing.T) {
	var next int
	var closed []int
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
		Close: func(_ context.Context, conn int) error {
			closed = append(closed, conn)
			return nil
		},
	})

	err := pool.WithConnection(context.Background(), time.Second, func(conn int) error {
		if conn != 1 {
			t.Fatalf("WithConnection() conn = %d, want 1", conn)
		}
		return context.Canceled
	})
	if err != context.Canceled {
		t.Fatalf("WithConnection() error = %v, want context.Canceled", err)
	}

	fresh, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() after failed WithConnection error = %v", err)
	}
	if fresh != 2 || pool.LastConnectionReused {
		t.Fatalf("Get() after failed WithConnection conn=%d reused=%v, want conn=2 reused=false", fresh, pool.LastConnectionReused)
	}
	if !reflect.DeepEqual(closed, []int{1}) {
		t.Fatalf("closed = %#v, want [1]", closed)
	}
}

func TestConnectionPoolWithConnectionRemovesOnPanic(t *testing.T) {
	var next int
	var closed []int
	pool := NewConnectionPool(ConnectionPoolOptions[int]{
		Connect: func(context.Context) (int, error) {
			next++
			return next, nil
		},
		Close: func(_ context.Context, conn int) error {
			closed = append(closed, conn)
			return nil
		},
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("WithConnection panic = %#v, want boom", recovered)
			}
		}()
		_ = pool.WithConnection(context.Background(), time.Second, func(conn int) error {
			if conn != 1 {
				t.Fatalf("WithConnection() conn = %d, want 1", conn)
			}
			panic("boom")
		})
	}()

	fresh, err := pool.Get(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Get() after panic error = %v", err)
	}
	if fresh != 2 || pool.LastConnectionReused {
		t.Fatalf("Get() after panic conn=%d reused=%v, want conn=2 reused=false", fresh, pool.LastConnectionReused)
	}
	if !reflect.DeepEqual(closed, []int{1}) {
		t.Fatalf("closed = %#v, want [1]", closed)
	}
}
