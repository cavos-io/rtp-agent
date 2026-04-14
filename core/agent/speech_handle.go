package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/google/uuid"
)

const (
	SpeechPriorityLow    = 0
	SpeechPriorityNormal = 5
	SpeechPriorityHigh   = 10
	InterruptionTimeout  = 5 * time.Second
)

type InputDetails struct {
	Modality string
}

func DefaultInputDetails() InputDetails {
	return InputDetails{Modality: "audio"}
}

type RunResultInterface interface {
	AddEvent(ev RunEvent)
	WatchTask(done <-chan struct{})
}

type SpeechHandle struct {
	ID                 string
	AllowInterruptions bool
	InputDetails       InputDetails
	Priority           int
	CreatedAt          time.Time

	numSteps  int
	chatItems []llm.ChatItem

	interruptCh chan struct{}
	doneCh      chan struct{}
	scheduledCh chan struct{}

	FinalOutput any
	
	mu sync.Mutex
	err error
	
	OnItemAdded func(item llm.ChatItem)
	RunResult   RunResultInterface
}

func (s *SpeechHandle) Error() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func NewSpeechHandle(allowInterruptions bool, inputDetails InputDetails) *SpeechHandle {
	return &SpeechHandle{
		ID:                 "speech_" + uuid.NewString()[:12],
		AllowInterruptions: allowInterruptions,
		InputDetails:       inputDetails,
		CreatedAt:          time.Now(),
		numSteps:           1,
		interruptCh:        make(chan struct{}),
		doneCh:             make(chan struct{}),
		scheduledCh:        make(chan struct{}),
	}
}

func (s *SpeechHandle) IsDone() bool {
	select {
	case <-s.doneCh:
		return true
	default:
		return false
	}
}

func (s *SpeechHandle) IsInterrupted() bool {
	select {
	case <-s.interruptCh:
		return true
	default:
		return false
	}
}

func (s *SpeechHandle) IsScheduled() bool {
	select {
	case <-s.scheduledCh:
		return true
	default:
		return false
	}
}

func (s *SpeechHandle) Interrupt(force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !force && !s.AllowInterruptions {
		return nil // Cannot interrupt
	}

	if !s.IsInterrupted() && !s.IsDone() {
		close(s.interruptCh)

		// Start a timeout to force-close doneCh if it doesn't resolve naturally
		go func() {
			time.Sleep(InterruptionTimeout)
			if !s.IsDone() {
				s.MarkDone()
			}
		}()
	}

	return nil
}

func (s *SpeechHandle) MarkDone() {
	s.MarkDoneWithError(nil)
}

func (s *SpeechHandle) MarkDoneWithError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.IsDone() {
		s.err = err
		close(s.doneCh)
	}
}

func (s *SpeechHandle) MarkScheduled() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.IsScheduled() {
		close(s.scheduledCh)
	}
}

func (s *SpeechHandle) Wait(ctx context.Context) error {
	select {
	case <-s.doneCh:
		return s.Error()
	case <-ctx.Done():
		return ctx.Err()
	}
}
