package utils

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrConnectionPoolNoConnect = errors.New("connection pool requires a connect callback")

type ConnectionPoolOptions[T comparable] struct {
	MaxSessionDuration time.Duration
	MarkRefreshedOnGet bool
	ConnectTimeout     time.Duration
	Connect            func(context.Context) (T, error)
	Close              func(context.Context, T) error
}

type ConnectionPool[T comparable] struct {
	opts ConnectionPoolOptions[T]

	mu          sync.Mutex
	connections map[T]time.Time
	available   map[T]struct{}
	toClose     map[T]struct{}
	prewarming  bool

	LastAcquireTime      time.Duration
	LastConnectionReused bool
}

func NewConnectionPool[T comparable](opts ConnectionPoolOptions[T]) *ConnectionPool[T] {
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 10 * time.Second
	}
	return &ConnectionPool[T]{
		opts:        opts,
		connections: make(map[T]time.Time),
		available:   make(map[T]struct{}),
		toClose:     make(map[T]struct{}),
	}
}

func (p *ConnectionPool[T]) Get(ctx context.Context, timeout time.Duration) (T, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var zero T
	p.drainToCloseLocked(ctx)

	now := time.Now()
	for conn := range p.available {
		delete(p.available, conn)
		connectedAt := p.connections[conn]
		if p.opts.MaxSessionDuration == 0 || now.Sub(connectedAt) <= p.opts.MaxSessionDuration {
			if p.opts.MarkRefreshedOnGet {
				p.connections[conn] = now
			}
			p.LastAcquireTime = 0
			p.LastConnectionReused = true
			return conn, nil
		}
		p.removeLocked(conn)
	}

	start := time.Now()
	conn, err := p.connectLocked(ctx, timeout)
	if err != nil {
		return zero, err
	}
	p.LastAcquireTime = time.Since(start)
	p.LastConnectionReused = false
	return conn, nil
}

func (p *ConnectionPool[T]) WithConnection(ctx context.Context, timeout time.Duration, fn func(T) error) error {
	conn, err := p.Get(ctx, timeout)
	if err != nil {
		return err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			p.Remove(conn)
			panic(recovered)
		}
	}()
	if err := fn(conn); err != nil {
		p.Remove(conn)
		return err
	}
	p.Put(conn)
	return nil
}

func (p *ConnectionPool[T]) Put(conn T) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.connections[conn]; ok {
		p.available[conn] = struct{}{}
	}
}

func (p *ConnectionPool[T]) Remove(conn T) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeLocked(conn)
}

func (p *ConnectionPool[T]) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for conn := range p.connections {
		p.toClose[conn] = struct{}{}
	}
	p.connections = make(map[T]time.Time)
	p.available = make(map[T]struct{})
}

func (p *ConnectionPool[T]) Prewarm(ctx context.Context) {
	p.mu.Lock()
	if p.prewarming || len(p.connections) > 0 {
		p.mu.Unlock()
		return
	}
	p.prewarming = true
	timeout := p.opts.ConnectTimeout
	p.mu.Unlock()

	go func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.prewarming = false

		var conn T
		conn, err := p.connectLocked(ctx, timeout)
		if err == nil {
			p.available[conn] = struct{}{}
		}
	}()
}

func (p *ConnectionPool[T]) Close(ctx context.Context) error {
	p.Invalidate()

	p.mu.Lock()
	defer p.mu.Unlock()
	p.drainToCloseLocked(ctx)
	return nil
}

func (p *ConnectionPool[T]) connectLocked(ctx context.Context, timeout time.Duration) (T, error) {
	var zero T
	if p.opts.Connect == nil {
		return zero, ErrConnectionPoolNoConnect
	}
	if timeout == 0 {
		timeout = p.opts.ConnectTimeout
	}

	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := p.opts.Connect(connectCtx)
	if err != nil {
		return zero, err
	}
	p.connections[conn] = time.Now()
	return conn, nil
}

func (p *ConnectionPool[T]) removeLocked(conn T) {
	delete(p.available, conn)
	if _, ok := p.connections[conn]; ok {
		p.toClose[conn] = struct{}{}
		delete(p.connections, conn)
	}
}

func (p *ConnectionPool[T]) drainToCloseLocked(ctx context.Context) {
	for conn := range p.toClose {
		delete(p.toClose, conn)
		if p.opts.Close != nil {
			_ = p.opts.Close(ctx, conn)
		}
	}
}
