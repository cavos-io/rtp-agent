//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"
	native "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeinprocess"
)

// NewProvider returns a fresh, lazy worker owner. Construction does not load FFI.
func NewProvider(cfg Config, key, secret string) (worker.SessionTransportProvider, error) {
	return &provider{cfg: cfg, key: key, secret: secret}, nil
}

type provider struct {
	mu          sync.Mutex
	cfg         Config
	key, secret string
	runtime     *native.Runtime
	closed      bool
}

func (p *provider) NewSession(job *worker.JobContext, session *agent.AgentSession, opts worker.StartSessionOptions) (worker.SessionRoomIO, error) {
	if len(opts.ConnectOptions) != 0 || opts.RoomCallback != nil {
		return nil, errors.New("native transport does not support Pion connect options or room callbacks")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("native worker provider closed")
	}
	r := NewInProcess(job, session, p.cfg, p.key, p.secret)
	if err := r.ConfigureRoomOptions(opts.RoomOptions); err != nil {
		return nil, err
	}
	r.connect = func(ctx context.Context, cfg Config, consume func(context.Context, *model.AudioFrame)) (*client, error) {
		rt, err := p.acquire()
		if err != nil {
			return nil, err
		}
		return launchWithRuntime(ctx, cfg, consume, rt)
	}
	return r, nil
}

func (p *provider) acquire() (*native.Runtime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("native worker provider closed")
	}
	if p.runtime != nil {
		return p.runtime, nil
	}
	processOwner.Lock()
	defer processOwner.Unlock()
	if processOwner.manual {
		return nil, errors.New("cannot mix managed and manual native runtime ownership")
	}
	rt, err := processRuntimeLocked(p.cfg.Library)
	if err != nil {
		return nil, err
	}
	processOwner.leases++
	p.runtime = rt
	return rt, nil
}

func (p *provider) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.runtime == nil {
		return nil
	}
	processOwner.Lock()
	processOwner.leases--
	last := processOwner.leases == 0
	if last {
		processOwner.closed = true
	}
	processOwner.Unlock()
	if last {
		return p.runtime.Close(ctx)
	}
	return nil
}
