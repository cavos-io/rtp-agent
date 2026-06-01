package agent

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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

var (
	ErrSpeechInterruptionsDisabled = errors.New("speech handle does not allow interruptions")
	ErrSpeechAlreadyInterrupted    = errors.New("speech handle is already interrupted")
	ErrSpeechInterrupted           = errors.New("speech interrupted")
)

type InputDetails struct {
	Modality string
}

func DefaultInputDetails() InputDetails {
	return InputDetails{Modality: "audio"}
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

	mu sync.Mutex
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
		return ErrSpeechInterruptionsDisabled
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

func (s *SpeechHandle) SetAllowInterruptions(allow bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !allow && s.IsInterrupted() {
		return ErrSpeechAlreadyInterrupted
	}

	s.AllowInterruptions = allow
	return nil
}

func (s *SpeechHandle) MarkDone() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.IsDone() {
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
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *SpeechHandle) WaitIfNotInterrupted(ctx context.Context, workDone ...<-chan error) error {
	cases := []reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(s.interruptCh)},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
	}
	for _, ch := range workDone {
		if ch == nil {
			continue
		}
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)})
	}

	if len(cases) == 2 {
		return nil
	}

	for len(cases) > 2 {
		chosen, value, ok := reflect.Select(cases)
		switch chosen {
		case 0:
			return ErrSpeechInterrupted
		case 1:
			return ctx.Err()
		default:
			if ok && !value.IsNil() {
				if err, _ := value.Interface().(error); err != nil {
					return err
				}
			}
			cases = append(cases[:chosen], cases[chosen+1:]...)
		}
	}

	return nil
}

func (s *SpeechHandle) GenerationID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.ID + "_" + strconv.Itoa(s.numSteps)
}

func (s *SpeechHandle) ParentGenerationID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.numSteps <= 1 {
		return ""
	}

	return s.ID + "_" + strconv.Itoa(s.numSteps-1)
}

func (s *SpeechHandle) IncrementStep() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.numSteps++
}
