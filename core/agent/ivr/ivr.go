package ivr

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// UserState represents the user's state in the session (mirrored from agent package)
type UserState string

const (
	UserStateListening UserState = "listening"
	UserStateSpeaking  UserState = "speaking"
	UserStateAway      UserState = "away"
)

// AgentState represents the agent's state in the session (mirrored from agent package)
type AgentState string

const (
	AgentStateIdle      AgentState = "idle"
	AgentStateListening AgentState = "listening"
	AgentStateSpeaking  AgentState = "speaking"
)

// UserInputTranscribedEvent represents a transcription event (mirrored from agent package)
type UserInputTranscribedEvent struct {
	Transcript string
	IsFinal    bool
}

// IVRSession interface defines the methods needed by IVRActivity from AgentSession
type IVRSession interface {
	GenerateReply(ctx context.Context, userInput string, allowInterruptions bool) (any, error)
	GetDataPublisher() DataPublisher
}

type DataPublisher interface {
	PublishDataPacket(pck lksdk.DataPacket, opts ...lksdk.DataPublishOption) error
}

type LoopDetector struct {
	windowSize                  int
	similarityThreshold         float64
	consecutiveThreshold        int
	transcribedChunks           []string
	numConsecutiveSimilarChunks int
	mu                          sync.Mutex
}

func NewLoopDetector() *LoopDetector {
	return &LoopDetector{
		windowSize:           20,
		similarityThreshold:  0.85,
		consecutiveThreshold: 3,
	}
}

func (l *LoopDetector) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.transcribedChunks = nil
	l.numConsecutiveSimilarChunks = 0
}

func (l *LoopDetector) AddChunk(chunk string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.transcribedChunks = append(l.transcribedChunks, chunk)
	if len(l.transcribedChunks) > l.windowSize {
		l.transcribedChunks = l.transcribedChunks[len(l.transcribedChunks)-l.windowSize:]
	}
}

func jaccardSimilarity(s1, s2 string) float64 {
	words1 := strings.Fields(strings.ToLower(s1))
	words2 := strings.Fields(strings.ToLower(s2))
	if len(words1) == 0 && len(words2) == 0 {
		return 1.0
	}

	set1 := make(map[string]bool)
	for _, w := range words1 {
		set1[w] = true
	}

	set2 := make(map[string]bool)
	for _, w := range words2 {
		set2[w] = true
	}

	intersection := 0
	for w := range set1 {
		if set2[w] {
			intersection++
		}
	}

	union := len(set1) + len(set2) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func (l *LoopDetector) CheckLoopDetection() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.transcribedChunks) < 2 {
		return false
	}

	lastChunk := l.transcribedChunks[len(l.transcribedChunks)-1]
	maxSim := 0.0

	for i := 0; i < len(l.transcribedChunks)-1; i++ {
		sim := jaccardSimilarity(l.transcribedChunks[i], lastChunk)
		if sim > maxSim {
			maxSim = sim
		}
	}

	if maxSim > l.similarityThreshold {
		l.numConsecutiveSimilarChunks++
	} else {
		l.numConsecutiveSimilarChunks = 0
	}

	return l.numConsecutiveSimilarChunks >= l.consecutiveThreshold
}

type IVRActivity struct {
	session            IVRSession
	maxSilenceDuration time.Duration
	loopDetector       *LoopDetector

	currentUserState  UserState
	currentAgentState AgentState

	silenceTimer *time.Timer
	lastShouldScheduleCheck bool
	mu           sync.Mutex
}

func NewIVRActivity(session IVRSession) *IVRActivity {
	return &IVRActivity{
		session:            session,
		maxSilenceDuration: 5 * time.Second,
		loopDetector:       NewLoopDetector(),
	}
}

func (i *IVRActivity) Tools() []interface{} {
	return []interface{}{NewSendDTMFTool(i.session.GetDataPublisher())}
}

func (i *IVRActivity) Start() {
	// Event handlers are hooked natively via OnAgentStateChanged, OnUserStateChanged, OnUserInputTranscribed
}

func (i *IVRActivity) Stop() {
	i.mu.Lock()
	if i.silenceTimer != nil {
		i.silenceTimer.Stop()
	}
	i.mu.Unlock()
}

func (i *IVRActivity) OnUserInputTranscribed(ev *UserInputTranscribedEvent) {
	if !ev.IsFinal {
		return
	}

	i.loopDetector.AddChunk(ev.Transcript)

	if i.loopDetector.CheckLoopDetection() {
		logger.Logger.Debugw("IVRActivity: speech loop detected; sending notification")
		_, _ = i.session.GenerateReply(context.Background(), "", false)
		i.loopDetector.Reset()
	}
}

func (i *IVRActivity) OnUserStateChanged(oldState, newState UserState) {
	i.mu.Lock()
	i.currentUserState = newState
	i.mu.Unlock()
	i.scheduleSilenceCheck()
}

func (i *IVRActivity) OnAgentStateChanged(oldState, newState AgentState) {
	i.mu.Lock()
	i.currentAgentState = newState
	i.mu.Unlock()
	i.scheduleSilenceCheck()
}

func (i *IVRActivity) shouldScheduleCheckLocked() bool {
	isUserSilent := i.currentUserState == UserStateListening || i.currentUserState == UserStateAway
	isAgentSilent := i.currentAgentState == AgentStateIdle || i.currentAgentState == AgentStateListening
	return isUserSilent && isAgentSilent
}

func (i *IVRActivity) scheduleSilenceCheck() {
	i.mu.Lock()
	defer i.mu.Unlock()

	shouldSchedule := i.shouldScheduleCheckLocked()

	if shouldSchedule {
		if i.lastShouldScheduleCheck {
			return
		}
		if i.silenceTimer == nil {
			i.silenceTimer = time.AfterFunc(i.maxSilenceDuration, i.onSilenceDetected)
		} else {
			i.silenceTimer.Reset(i.maxSilenceDuration)
		}
	} else {
		if i.silenceTimer != nil {
			i.silenceTimer.Stop()
			i.silenceTimer = nil
		}
	}
	i.lastShouldScheduleCheck = shouldSchedule
}

func (i *IVRActivity) onSilenceDetected() {
	logger.Logger.Debugw("IVRActivity: silence detected; sending notification")
	_, _ = i.session.GenerateReply(context.Background(), "", true)
}

// SendDTMFTool implementation (mirrored from beta tools to avoid cycle)
type SendDTMFTool struct {
	publisher DataPublisher
}

func NewSendDTMFTool(publisher DataPublisher) *SendDTMFTool {
	return &SendDTMFTool{
		publisher: publisher,
	}
}

func (t *SendDTMFTool) ID() string   { return "send_dtmf_events" }
func (t *SendDTMFTool) Name() string { return "send_dtmf_events" }

func (t *SendDTMFTool) Description() string {
	return `Send a list of DTMF events to the telephony provider.

Call when:
- User wants to send DTMF events`
}

func (t *SendDTMFTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"events": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "*", "#", "A", "B", "C", "D"},
				},
			},
		},
		"required": []string{"events"},
	}
}

type sendDTMFArgs struct {
	Events []beta.DtmfEvent `json:"events"`
}

func (t *SendDTMFTool) Args() any {
	return &sendDTMFArgs{}
}

func (t *SendDTMFTool) Execute(ctx context.Context, args any) (any, error) {
	var a *sendDTMFArgs
	if typed, ok := args.(*sendDTMFArgs); ok {
		a = typed
	} else {
		return nil, fmt.Errorf("unexpected arguments type: %T", args)
	}

	if t.publisher == nil {
		return "", fmt.Errorf("Data publisher not available")
	}

	for _, event := range a.Events {
		code, err := beta.DtmfEventToCode(event)
		if err != nil {
			return "", err
		}

		err = t.publisher.PublishDataPacket(&livekit.SipDTMF{
			Code:  uint32(code),
			Digit: string(event),
		}, lksdk.WithDataPublishReliable(true))
		if err != nil {
			return fmt.Sprintf("Failed to send DTMF event: %s. Error: %v", event, err), nil
		}

		// Wait for publish delay (0.3s)
		select {
		case <-ctx.Done():
			return "Cancelled", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	return fmt.Sprintf("Successfully sent DTMF events: %s", beta.FormatDtmf(a.Events)), nil
}

