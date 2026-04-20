package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
)

type DtmfEvent struct {
	Digit string
	Time  time.Time
}

type IVRActivity struct {
	AgentIntf AgentInterface
	Agent     *Agent

	dtmfCh chan DtmfEvent
	ctx    context.Context
	cancel context.CancelFunc

	buffer        string
	lastDigitTime time.Time
	mu            sync.Mutex

	timeout time.Duration
	onDigit func(buffer string) (bool, error) // Returns true to continue, false to stop buffering
}

func NewIVRActivity(agentIntf AgentInterface) *IVRActivity {
	ctx, cancel := context.WithCancel(context.Background())
	return &IVRActivity{
		AgentIntf: agentIntf,
		Agent:     agentIntf.GetAgent(),
		dtmfCh:    make(chan DtmfEvent, 100),
		ctx:       ctx,
		cancel:    cancel,
		timeout:   5 * time.Second, // Default inter-digit timeout
	}
}

func (i *IVRActivity) SetDigitCallback(timeout time.Duration, cb func(buffer string) (bool, error)) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.timeout = timeout
	i.onDigit = cb
}

func (i *IVRActivity) Start() {
	_ = i.AgentIntf.OnEnter(i.ctx)
	go i.run()
}

func (i *IVRActivity) Stop() {
	i.cancel()
	_ = i.AgentIntf.OnExit(context.Background())
}

func (i *IVRActivity) OnDtmf(digit string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	
	if i.ctx.Err() != nil {
		return
	}

	i.dtmfCh <- DtmfEvent{
		Digit: digit,
		Time:  time.Now(),
	}
}

func (i *IVRActivity) run() {
	var timer *time.Timer
	var timerCh <-chan time.Time

	for {
		select {
		case <-i.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		
		case ev := <-i.dtmfCh:
			i.mu.Lock()
			i.buffer += ev.Digit
			i.lastDigitTime = ev.Time
			cb := i.onDigit
			buffer := i.buffer
			timeout := i.timeout
			i.mu.Unlock()

			logger.Logger.Infow("Received DTMF", "digit", ev.Digit, "buffer", buffer)

			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(timeout)
			timerCh = timer.C

			if cb != nil {
				cont, err := cb(buffer)
				if err != nil {
					logger.Logger.Errorw("IVR digit callback error", err)
				}
				if !cont {
					// Stop buffering, assume input is complete
					i.mu.Lock()
					i.buffer = ""
					i.mu.Unlock()
					if timer != nil {
						timer.Stop()
						timerCh = nil
					}
				}
			}

		case <-timerCh:
			// Timeout elapsed without new digits
			i.mu.Lock()
			buffer := i.buffer
			i.buffer = ""
			cb := i.onDigit
			i.mu.Unlock()

			if buffer != "" {
				logger.Logger.Infow("IVR timeout reached", "final_buffer", buffer)
				// You can trigger a different callback here if needed,
				// or re-trigger the onDigit with a special flag
				if cb != nil {
					_, _ = cb(buffer)
				}
			}
			timerCh = nil
		}
	}
}
