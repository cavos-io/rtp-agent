package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
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

	// Execute LLM+TTS pipeline for this speech
	go a.executeSpeech(speech)

	// Wait for completion then trigger next item in queue
	go func() {
		// Wait for generation to finish, be interrupted, or session to stop
		select {
		case <-speech.doneCh:
		case <-a.ctx.Done():
		}

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

func (a *AgentActivity) executeSpeech(speech *SpeechHandle) {
	defer speech.MarkDone()

	// Create a context that cancels on interrupt or activity shutdown
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	go func() {
		select {
		case <-speech.interruptCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	session := a.Session
	if session == nil || session.LLM == nil || session.TTS == nil {
		return
	}

	toolsInterface := make([]interface{}, len(session.Tools))
	for i, t := range session.Tools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

	// Loop to handle tool calls, same pattern as PipelineAgent.generateReply
	for {
		session.UpdateAgentState(AgentStateThinking)

		genData, err := PerformLLMInference(ctx, session.LLM, session.ChatCtx, session.Tools)
		if err != nil {
			logger.Logger.Errorw("LLM inference failed in executeSpeech", err)
			session.UpdateAgentState(AgentStateIdle)
			return
		}

		toolOutCh := PerformToolExecutions(ctx, genData.FunctionCh, toolCtx)

		// Run TTS in parallel with LLM text stream
		ttsGen, err := PerformTTSInference(ctx, session.TTS, genData.TextCh)
		if err != nil {
			logger.Logger.Errorw("TTS inference failed in executeSpeech", err)
		} else {
			session.UpdateAgentState(AgentStateSpeaking)
		audioLoop:
			for frame := range ttsGen.AudioCh {
				select {
				case <-ctx.Done():
					break audioLoop
				default:
					if session.Assistant != nil && session.Assistant.PublishAudio != nil {
						_ = session.Assistant.PublishAudio(frame)
					}
				}
			}
		}

		// Collect tool execution results
		var executedTools bool
		for toolOut := range toolOutCh {
			executedTools = true
			session.ChatCtx.Append(&toolOut.FncCall)
			if toolOut.FncCallOut != nil {
				session.ChatCtx.Append(toolOut.FncCallOut)
			}
		}

		if !executedTools {
			break
		}
		// Tool calls were made — loop back to LLM with results
	}

	session.UpdateAgentState(AgentStateIdle)
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
