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
	ErrSpeechNoActiveGeneration    = errors.New("speech handle has no active generation")
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

	interruptCh        chan struct{}
	doneCh             chan struct{}
	scheduledCh        chan struct{}
	authorizationCh    chan struct{}
	generationChs      []chan struct{}
	nextCallbackID     uint64
	doneCallbacks      map[uint64]func(*SpeechHandle)
	itemAddedCallbacks map[uint64]func(llm.ChatItem)

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
		authorizationCh:    make(chan struct{}),
		doneCallbacks:      make(map[uint64]func(*SpeechHandle)),
		itemAddedCallbacks: make(map[uint64]func(llm.ChatItem)),
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
	if s.IsDone() {
		s.mu.Unlock()
		return
	}

	close(s.doneCh)
	if len(s.generationChs) > 0 {
		s.closeGenerationLocked(len(s.generationChs) - 1)
	}
	callbacks := make([]func(*SpeechHandle), 0, len(s.doneCallbacks))
	for _, callback := range s.doneCallbacks {
		callbacks = append(callbacks, callback)
	}
	s.mu.Unlock()

	for _, callback := range callbacks {
		callback(s)
	}
}

func (s *SpeechHandle) MarkScheduled() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !isClosed(s.scheduledCh) {
		close(s.scheduledCh)
	}
}

func (s *SpeechHandle) WaitForScheduled(ctx context.Context) error {
	select {
	case <-s.scheduledCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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

func (s *SpeechHandle) AddDoneCallback(callback func(*SpeechHandle)) func() {
	if callback == nil {
		return func() {}
	}

	s.mu.Lock()
	if s.IsDone() {
		s.mu.Unlock()
		callback(s)
		return func() {}
	}

	id := s.nextCallbackID
	s.nextCallbackID++
	s.doneCallbacks[id] = callback
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		delete(s.doneCallbacks, id)
		s.mu.Unlock()
	}
}

func (s *SpeechHandle) AddItemAddedCallback(callback func(llm.ChatItem)) func() {
	if callback == nil {
		return func() {}
	}

	s.mu.Lock()
	id := s.nextCallbackID
	s.nextCallbackID++
	s.itemAddedCallbacks[id] = callback
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		delete(s.itemAddedCallbacks, id)
		s.mu.Unlock()
	}
}

func (s *SpeechHandle) AddChatItems(items ...llm.ChatItem) {
	for _, item := range items {
		s.mu.Lock()
		callbacks := make([]func(llm.ChatItem), 0, len(s.itemAddedCallbacks))
		for _, callback := range s.itemAddedCallbacks {
			callbacks = append(callbacks, callback)
		}
		s.mu.Unlock()

		for _, callback := range callbacks {
			callback(item)
		}

		s.mu.Lock()
		s.chatItems = append(s.chatItems, item)
		s.mu.Unlock()
	}
}

func (s *SpeechHandle) ChatItems() []llm.ChatItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]llm.ChatItem, len(s.chatItems))
	copy(items, s.chatItems)
	return items
}

func (s *SpeechHandle) AuthorizeGeneration() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.generationChs = append(s.generationChs, make(chan struct{}))
	if !isClosed(s.authorizationCh) {
		close(s.authorizationCh)
	}
}

func (s *SpeechHandle) ClearAuthorization() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if isClosed(s.authorizationCh) {
		s.authorizationCh = make(chan struct{})
	}
}

func (s *SpeechHandle) WaitForAuthorization(ctx context.Context) error {
	s.mu.Lock()
	authorizationCh := s.authorizationCh
	s.mu.Unlock()

	select {
	case <-authorizationCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *SpeechHandle) WaitForGeneration(ctx context.Context, stepIndex int) error {
	s.mu.Lock()
	if len(s.generationChs) == 0 {
		s.mu.Unlock()
		return ErrSpeechNoActiveGeneration
	}
	if stepIndex < 0 {
		stepIndex = len(s.generationChs) + stepIndex
	}
	if stepIndex < 0 || stepIndex >= len(s.generationChs) {
		s.mu.Unlock()
		return ErrSpeechNoActiveGeneration
	}
	generationCh := s.generationChs[stepIndex]
	s.mu.Unlock()

	select {
	case <-generationCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *SpeechHandle) MarkGenerationDone() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.generationChs) == 0 {
		return ErrSpeechNoActiveGeneration
	}

	s.closeGenerationLocked(len(s.generationChs) - 1)
	return nil
}

func (s *SpeechHandle) closeGenerationLocked(index int) {
	if !isClosed(s.generationChs[index]) {
		close(s.generationChs[index])
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
