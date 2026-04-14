package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
)

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
	speechQueue    []*SpeechHandle
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
		speechQueue:    make([]*SpeechHandle, 0),
		queueUpdatedCh: make(chan struct{}, 1),
		ctx:            ctx,
		cancel:         cancel,
	}
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

	speech.Priority = priority

	// Add to queue (ideally a priority queue, but simple slice for now)
	a.speechQueue = append(a.speechQueue, speech)

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

	if len(a.speechQueue) == 0 || a.schedulingPaused {
		return
	}

	// Basic queue processing, grabbing the first item
	speech := a.speechQueue[0]
	a.speechQueue = a.speechQueue[1:]

	if speech.IsDone() {
		return
	}

	a.currentSpeech = speech

	// Run speech completion asynchronously
	go func() {
		// Trigger the pipeline agent to process the speech request
		if a.Session.Assistant != nil {
			a.Session.Assistant.GenerateReply(speech)
		} else {
			speech.MarkDone()
		}

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
