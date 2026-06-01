package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
)

var ErrSpeechSchedulingPaused = errors.New("speech scheduling is paused")

type EndOfTurnInfo struct {
	SkipReply            bool
	NewTranscript        string
	TranscriptConfidence float64
	StartedSpeakingAt    *float64
	StoppedSpeakingAt    *float64
}

// AgentActivity handles the internal event loops, I/O processing, and
// speech generation queue for an Agent.
type AgentActivity struct {
	AgentIntf AgentInterface
	Agent     *Agent
	Session   *AgentSession

	currentSpeech  *SpeechHandle
	speechQueue    []scheduledSpeech
	nextSpeechSeq  uint64
	queueMu        sync.Mutex
	queueUpdatedCh chan struct{}

	schedulingPaused bool

	sttEOSReceived bool
	speaking       bool

	ctx    context.Context
	cancel context.CancelFunc

	eouMu     sync.Mutex
	eouCancel context.CancelFunc
}

func NewAgentActivity(agentIntf AgentInterface, session *AgentSession) *AgentActivity {
	ctx, cancel := context.WithCancel(context.Background())
	return &AgentActivity{
		AgentIntf:      agentIntf,
		Agent:          agentIntf.GetAgent(),
		Session:        session,
		speechQueue:    make([]scheduledSpeech, 0),
		queueUpdatedCh: make(chan struct{}, 1),
		ctx:            ctx,
		cancel:         cancel,
	}
}

type scheduledSpeech struct {
	speech   *SpeechHandle
	priority int
	seq      uint64
}

func (a *AgentActivity) Start() {
	a.AgentIntf.OnEnter()
	go a.schedulingTask()
}

func (a *AgentActivity) Stop() {
	a.AgentIntf.OnExit()
	a.cancel()
}

func (a *AgentActivity) ScheduleSpeech(speech *SpeechHandle, priority int, force bool) error {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()

	if a.schedulingPaused && !force {
		_ = speech.Interrupt(true)
		return ErrSpeechSchedulingPaused
	}

	speech.Priority = priority

	a.speechQueue = append(a.speechQueue, scheduledSpeech{
		speech:   speech,
		priority: priority,
		seq:      a.nextSpeechSeq,
	})
	a.nextSpeechSeq++

	// Notify the scheduling loop
	select {
	case a.queueUpdatedCh <- struct{}{}:
	default:
	}

	speech.MarkScheduled()
	return nil
}

func (a *AgentActivity) schedulingTask() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.queueUpdatedCh:
			a.processQueue()
		}
	}
}

func (a *AgentActivity) processQueue() {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()

	if len(a.speechQueue) == 0 || a.schedulingPaused || a.currentSpeech != nil {
		return
	}

	nextIdx := a.nextSpeechIndexLocked()
	speech := a.speechQueue[nextIdx].speech
	a.speechQueue = append(a.speechQueue[:nextIdx], a.speechQueue[nextIdx+1:]...)

	if speech.IsDone() {
		return
	}

	a.currentSpeech = speech

	// Run speech completion asynchronously
	go func() {
		// Wait for generation to finish or be interrupted
		<-speech.doneCh

		a.queueMu.Lock()
		a.currentSpeech = nil
		a.queueMu.Unlock()

		// Trigger next
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
	}()
}

func (a *AgentActivity) nextSpeechIndexLocked() int {
	best := 0
	for i := 1; i < len(a.speechQueue); i++ {
		current := a.speechQueue[i]
		candidate := a.speechQueue[best]
		if current.priority > candidate.priority || (current.priority == candidate.priority && current.seq < candidate.seq) {
			best = i
		}
	}
	return best
}

// Event callbacks from RecognitionHooks
func (a *AgentActivity) OnStartOfSpeech(ev *vad.VADEvent) {
	a.speaking = true
	a.sttEOSReceived = false
	logger.Logger.Infow("Start of speech detected")

	// Cancel pending EOU detection
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
		a.eouCancel = nil
	}
	a.eouMu.Unlock()
}

func (a *AgentActivity) OnEndOfSpeech(ev *vad.VADEvent) {
	a.speaking = false
	logger.Logger.Infow("End of speech detected")

	if a.Agent.TurnDetection == TurnDetectionModeVAD {
		// Trigger EOU detection
		a.runEOUDetection(EndOfTurnInfo{})
	}
}

func (a *AgentActivity) OnFinalTranscript(ev *stt.SpeechEvent) {
	a.sttEOSReceived = true
	if a.Agent.TurnDetection == TurnDetectionModeSTT {
		transcript := ""
		confidence := 0.0
		if len(ev.Alternatives) > 0 {
			transcript = ev.Alternatives[0].Text
			confidence = ev.Alternatives[0].Confidence
		}
		a.runEOUDetection(EndOfTurnInfo{
			NewTranscript:        transcript,
			TranscriptConfidence: confidence,
		})
	}
}

func (a *AgentActivity) runEOUDetection(info EndOfTurnInfo) {
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.eouCancel = cancel
	a.eouMu.Unlock()

	go func() {
		defer cancel()

		endpointingDelay := a.Agent.MinEndpointingDelay
		if endpointingDelay <= 0 {
			endpointingDelay = 0.5 // default
		}

		if a.Agent.TurnDetector != nil && info.NewTranscript != "" {
			// Predict end of turn
			chatCtx := a.Agent.ChatCtx.Copy()
			chatCtx.Append(&llm.ChatMessage{
				Role:    llm.ChatRoleUser,
				Content: []llm.ChatContent{{Text: info.NewTranscript}},
			})

			prob, err := a.Agent.TurnDetector.PredictEndOfTurn(ctx, chatCtx)
			if err == nil {
				logger.Logger.Infow("EOU prediction", "probability", prob)
				// Apply probability threshold logic
				if prob < 0.5 {
					endpointingDelay = a.Agent.MaxEndpointingDelay
					if endpointingDelay <= 0 {
						endpointingDelay = 2.0 // default
					}
				}
			} else {
				logger.Logger.Errorw("EOU prediction failed", err)
			}
		}

		timer := time.NewTimer(time.Duration(endpointingDelay * float64(time.Second)))
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// EOU detected
			logger.Logger.Infow("EOU detected, completing user turn")
			newMsg := &llm.ChatMessage{
				Role:    llm.ChatRoleUser,
				Content: []llm.ChatContent{{Text: info.NewTranscript}},
			}
			a.AgentIntf.OnUserTurnCompleted(a.ctx, a.Agent.ChatCtx, newMsg)
		}
	}()
}
