package agent

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent/ivr"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

type EndOfTurnInfo struct {
	SkipReply            bool
	TranscriptTimeout    time.Duration
	STTFlushDuration     time.Duration
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
	recog     *AudioRecognition

	currentSpeech  *SpeechHandle
	speechQueue    []*SpeechHandle
	queueMu        sync.Mutex
	queueUpdatedCh chan struct{}

	schedulingPaused bool

	sttEOSReceived    bool
	speaking          bool
	pausedSpeech      *SpeechHandle
	discardUserTurn   bool
	userTurnCommitted bool
	endOfTurnActive   bool
	lastSpeechEndedAt time.Time

	ctx    context.Context
	cancel context.CancelFunc

	eouMu     sync.Mutex
	eouCancel context.CancelFunc

	falseInterruptionMu sync.Mutex
	falseInterruptionTm *time.Timer
	userAwayMu          sync.Mutex
	userAwayTm          *time.Timer

	audioTranscript          string
	audioInterimTranscript   string
	audioPreflightTranscript string
	transcriptMu             sync.Mutex
	finalTranscriptCond      *sync.Cond

	speechWg sync.WaitGroup
	loopWg   sync.WaitGroup

	videoNodeCh chan *model.VideoFrame
}

func NewAgentActivity(agentIntf AgentInterface, session *AgentSession) *AgentActivity {
	ctx, cancel := context.WithCancel(context.Background())
	act := &AgentActivity{
		AgentIntf:      agentIntf,
		Agent:          agentIntf.GetAgent(),
		Session:        session,
		speechQueue:    make([]*SpeechHandle, 0),
		queueUpdatedCh: make(chan struct{}, 1),
		ctx:            ctx,
		cancel:         cancel,
		videoNodeCh:    make(chan *model.VideoFrame, 100),
	}
	act.finalTranscriptCond = sync.NewCond(&act.transcriptMu)

	// Keep the base Agent pointer in sync even when the session is started
	// with a custom AgentInterface implementation.
	if act.Agent != nil {
		act.Agent.activity = act
		act.Agent.ChatCtx = session.ChatCtx
	}

	return act
}

func (a *AgentActivity) Start() {
	go func() {
		if err := a.AgentIntf.OnEnter(a.ctx); err != nil {
			logger.Logger.Errorw("Agent OnEnter failed", err)
		}
	}()
	if a.recog == nil {
		a.recog = NewAudioRecognition(a.Session, a, a.Session.STT, a.Session.VAD)
		if err := a.recog.Start(a.ctx); err != nil {
			logger.Logger.Errorw("failed to start audio recognition", err)
		}
	}
	a.Session.UpdateUserState(UserStateListening)
	a.startUserAwayTimer(a.Session.Options.UserAwayTimeout)

	a.loopWg.Add(1)
	go func() {
		defer a.loopWg.Done()
		a.schedulingTask()
	}()

	if a.Agent != nil && a.Agent.VideoNode != nil {
		a.loopWg.Add(1)
		go func() {
			defer a.loopWg.Done()
			if err := a.Agent.VideoNode(a.ctx, a.videoNodeCh); err != nil {
				logger.Logger.Errorw("failed to run VideoNode", err)
			}
		}()
	}
}

func (a *AgentActivity) Stop() {
	_ = a.Drain(context.Background())
	a.AClose()
}

func (a *AgentActivity) Drain(ctx context.Context) error {
	a.loopWg.Add(1)
	go func() {
		defer a.loopWg.Done()
		if err := a.AgentIntf.OnExit(context.Background()); err != nil {
			logger.Logger.Errorw("Agent OnExit failed", err)
		}
	}()

	a.PauseScheduling()

	done := make(chan struct{})
	go func() {
		a.speechWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *AgentActivity) AClose() {
	a.cancel()
	a.cancelFalseInterruptionTimer()
	a.cancelUserAwayTimer()
	a.loopWg.Wait()
}

func (a *AgentActivity) PauseScheduling() {
	a.queueMu.Lock()
	a.schedulingPaused = true
	a.queueMu.Unlock()
}

func (a *AgentActivity) ResumeScheduling() {
	a.queueMu.Lock()
	wasPaused := a.schedulingPaused
	a.schedulingPaused = false
	a.queueMu.Unlock()

	if wasPaused {
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
	}
}

func (a *AgentActivity) PushAudio(frame *model.AudioFrame) error {
	if a.recog == nil {
		return nil
	}
	return a.recog.PushAudio(frame)
}

func (a *AgentActivity) PushVideo(frame *model.VideoFrame) error {
	select {
	case a.videoNodeCh <- frame:
	default:
		// channel full, drop frame
	}
	return nil
}

func (a *AgentActivity) CaptureVideoFrame(frame *model.VideoFrame) error {
	a.Session.mu.Lock()
	videoOut := a.Session.Output.Video
	a.Session.mu.Unlock()

	if videoOut != nil {
		return videoOut.CaptureVideoFrame(frame)
	}
	return nil
}

func (a *AgentActivity) ScheduleSpeech(speech *SpeechHandle, priority int, force bool) error {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()

	speech.Priority = priority

	if a.schedulingPaused && !force {
		_ = speech.Interrupt(true)
		speech.MarkDone()
		return context.Canceled
	}

	// Keep queue ordered by priority (high -> low), then FIFO for equal priority.
	insertAt := len(a.speechQueue)
	for i, queued := range a.speechQueue {
		if speech.Priority > queued.Priority ||
			(speech.Priority == queued.Priority && speech.CreatedAt.Before(queued.CreatedAt)) {
			insertAt = i
			break
		}
	}

	a.speechQueue = append(a.speechQueue, nil)
	copy(a.speechQueue[insertAt+1:], a.speechQueue[insertAt:])
	a.speechQueue[insertAt] = speech

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

	// Ensure only one active speech generation/playback at a time.
	if len(a.speechQueue) == 0 || a.schedulingPaused || a.currentSpeech != nil {
		return
	}

	if minDelay := a.Session.Options.MinConsecutiveSpeechDelay; minDelay > 0 && !a.lastSpeechEndedAt.IsZero() {
		minGap := time.Duration(minDelay * float64(time.Second))
		sinceLast := time.Since(a.lastSpeechEndedAt)
		if sinceLast < minGap {
			wait := minGap - sinceLast
			go a.notifyQueueAfter(wait)
			return
		}
	}

	// Basic queue processing, grabbing the first item
	speech := a.speechQueue[0]
	a.speechQueue = a.speechQueue[1:]

	if speech.IsDone() {
		return
	}

	a.currentSpeech = speech

	// Run speech completion asynchronously
	a.speechWg.Add(1)
	go func() {
		defer a.speechWg.Done()

		// Trigger the pipeline agent to process the speech request
		if a.Session.Assistant != nil {
			a.Session.Assistant.GenerateReply(speech)
		} else {
			speech.MarkDone()
		}

		// Wait for generation to finish or be interrupted
		<-speech.doneCh

		a.queueMu.Lock()
		if a.pausedSpeech == speech {
			a.pausedSpeech = nil
		}
		a.lastSpeechEndedAt = time.Now()
		a.currentSpeech = nil
		a.queueMu.Unlock()

		// Trigger next
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
	}()
}

func (a *AgentActivity) notifyQueueAfter(delay time.Duration) {
	if delay <= 0 {
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
		return
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-a.ctx.Done():
		return
	case <-timer.C:
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
	}
}

// Event callbacks from RecognitionHooks
func (a *AgentActivity) OnStartOfSpeech(ev *vad.VADEvent) {
	a.speaking = true
	a.sttEOSReceived = false
	a.discardUserTurn = false

	a.transcriptMu.Lock()
	a.audioTranscript = ""
	a.audioInterimTranscript = ""
	a.transcriptMu.Unlock()

	logger.Logger.Infow("🎤 User started speaking")
	a.cancelFalseInterruptionTimer()
	a.cancelUserAwayTimer()
	a.Session.UpdateUserState(UserStateSpeaking)

	// Cancel pending EOU detection
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
		a.eouCancel = nil
	}
	a.eouMu.Unlock()

	minDuration := a.Session.Options.MinInterruptionDuration
	if ev != nil && minDuration > 0 && ev.SpeechDuration < minDuration {
		return
	}

	a.queueMu.Lock()
	current := a.currentSpeech
	a.queueMu.Unlock()

	if current != nil && !current.AllowInterruptions && a.Session.Options.DiscardAudioIfUninterruptible {
		a.discardUserTurn = true
		return
	}

	if !a.Session.Options.AllowInterruptions {
		return
	}

	if current == nil {
		return
	}

	usePause := (a.Session.Options.ResumeFalseInterruption && a.Session.Options.FalseInterruptionTimeout > 0) ||
		a.Session.Options.MinInterruptionWords > 0
	if usePause && !current.IsDone() && !current.IsInterrupted() && current.AllowInterruptions {
		a.queueMu.Lock()
		a.pausedSpeech = current
		a.queueMu.Unlock()
		if a.Session.Output.Audio != nil {
			a.Session.Output.Audio.Pause()
		}
		a.Session.UpdateAgentState(AgentStateListening)
		return
	}

	if err := a.Interrupt(false); err != nil {
		logger.Logger.Errorw("failed to interrupt current speech", err)
	}
}

func (a *AgentActivity) OnEndOfSpeech(ev *vad.VADEvent) {
	a.speaking = false
	var speechDuration float64
	if ev != nil {
		speechDuration = ev.SpeechDuration
	}
	logger.Logger.Infow("🔇 User stopped speaking", "speechDuration", speechDuration)
	a.Session.UpdateUserState(UserStateListening)
	a.startUserAwayTimer(a.Session.Options.UserAwayTimeout)

	if a.discardUserTurn {
		a.discardUserTurn = false
		return
	}

	a.queueMu.Lock()
	paused := a.pausedSpeech
	a.queueMu.Unlock()
	if paused != nil && a.Session.Options.FalseInterruptionTimeout > 0 {
		a.startFalseInterruptionTimer(a.Session.Options.FalseInterruptionTimeout)
	}

	if a.Agent.TurnDetection == TurnDetectionModeVAD {
		// Trigger EOU detection
		a.runEOUDetection(EndOfTurnInfo{})
	}
}

func (a *AgentActivity) shouldIgnoreSTTEvent(isInterim bool) bool {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()

	if a.Agent.TurnDetection == TurnDetectionModeManual && a.userTurnCommitted {
		if !a.endOfTurnActive || isInterim {
			return true
		}
	}
	return false
}

func (a *AgentActivity) OnInterimTranscript(ev *stt.SpeechEvent) {
	if a.shouldIgnoreSTTEvent(true) {
		return
	}

	if len(ev.Alternatives) > 0 {
		transcript := ev.Alternatives[0].Text
		if transcript != "" {
			a.transcriptMu.Lock()
			if ev.Type == stt.SpeechEventPreflightTranscript {
				a.audioPreflightTranscript = strings.TrimSpace(a.audioTranscript + " " + transcript)
				a.audioInterimTranscript = transcript
				log.Println("Transcript preflight:", a.audioPreflightTranscript)
				// In a full implementation we would trigger preemptive generation here
				// using a.audioPreflightTranscript, but for parity we ensure the state is updated.
			} else {
				a.audioInterimTranscript = transcript
			}
			a.transcriptMu.Unlock()
		}
	}
}

func (a *AgentActivity) OnFinalTranscript(ev *stt.SpeechEvent) {
	if a.shouldIgnoreSTTEvent(false) {
		return
	}

	a.sttEOSReceived = true

	transcript := ""
	confidence := 0.0
	if len(ev.Alternatives) > 0 {
		transcript = ev.Alternatives[0].Text
		confidence = ev.Alternatives[0].Confidence
	}

	a.transcriptMu.Lock()
	if transcript != "" {
		a.audioTranscript = strings.TrimSpace(a.audioTranscript + " " + transcript)
	}
	a.audioPreflightTranscript = ""
	a.audioInterimTranscript = ""
	a.finalTranscriptCond.Broadcast()
	a.transcriptMu.Unlock()

	if a.Agent.TurnDetection == TurnDetectionModeSTT {
		if a.discardUserTurn {
			a.discardUserTurn = false
			a.cancelSpeechPause(false)
			return
		}

		words := countWords(transcript)
		minWords := a.Session.Options.MinInterruptionWords

		if minWords > 0 {
			if words >= minWords {
				a.cancelSpeechPause(true)
			} else {
				a.cancelSpeechPause(false)
				return
			}
		} else if transcript != "" {
			// User did actually speak; treat this as a real interruption.
			a.cancelSpeechPause(true)
		}

		a.runEOUDetection(EndOfTurnInfo{
			NewTranscript:        transcript,
			TranscriptConfidence: confidence,
		})
	}
}

func (a *AgentActivity) Interrupt(force bool) error {
	a.cancelSpeechPause(false)

	a.queueMu.Lock()
	current := a.currentSpeech
	queued := append([]*SpeechHandle(nil), a.speechQueue...)
	a.queueMu.Unlock()

	var firstErr error
	if current != nil {
		if err := current.Interrupt(force); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, speech := range queued {
		if err := speech.Interrupt(force); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if a.Session.Output.Audio != nil {
		a.Session.Output.Audio.ClearBuffer()
	}
	a.Session.UpdateAgentState(AgentStateListening)

	return firstErr
}

func (a *AgentActivity) startFalseInterruptionTimer(timeout float64) {
	a.cancelFalseInterruptionTimer()
	if timeout <= 0 {
		return
	}

	a.falseInterruptionMu.Lock()
	a.falseInterruptionTm = time.AfterFunc(time.Duration(timeout*float64(time.Second)), func() {
		a.queueMu.Lock()
		paused := a.pausedSpeech
		a.pausedSpeech = nil
		a.queueMu.Unlock()

		a.falseInterruptionMu.Lock()
		a.falseInterruptionTm = nil
		a.falseInterruptionMu.Unlock()

		if paused == nil || paused.IsDone() || paused.IsInterrupted() {
			return
		}

		if a.Session.Options.ResumeFalseInterruption {
			if a.Session.Output.Audio != nil {
				a.Session.Output.Audio.Resume()
			}
			if a.Session.Timeline != nil {
				a.Session.Timeline.AddEvent(&AgentFalseInterruptionEvent{
					Resumed:   true,
					CreatedAt: time.Now(),
				})
			}
			a.Session.UpdateAgentState(AgentStateSpeaking)
			return
		}

		_ = paused.Interrupt(false)
	})
	a.falseInterruptionMu.Unlock()
}

func (a *AgentActivity) cancelFalseInterruptionTimer() {
	a.falseInterruptionMu.Lock()
	defer a.falseInterruptionMu.Unlock()
	if a.falseInterruptionTm != nil {
		a.falseInterruptionTm.Stop()
		a.falseInterruptionTm = nil
	}
}

func (a *AgentActivity) cancelSpeechPause(interrupt bool) {
	a.cancelFalseInterruptionTimer()

	a.queueMu.Lock()
	paused := a.pausedSpeech
	a.pausedSpeech = nil
	a.queueMu.Unlock()
	if paused == nil {
		return
	}

	if interrupt {
		_ = paused.Interrupt(false)
	}

	if a.Session.Output.Audio != nil {
		a.Session.Output.Audio.Resume()
	}
}

func (a *AgentActivity) startUserAwayTimer(timeout float64) {
	a.cancelUserAwayTimer()
	if timeout <= 0 {
		return
	}

	a.userAwayMu.Lock()
	a.userAwayTm = time.AfterFunc(time.Duration(timeout*float64(time.Second)), func() {
		a.Session.UpdateUserState(UserStateAway)
	})
	a.userAwayMu.Unlock()
}

func (a *AgentActivity) cancelUserAwayTimer() {
	a.userAwayMu.Lock()
	defer a.userAwayMu.Unlock()
	if a.userAwayTm != nil {
		a.userAwayTm.Stop()
		a.userAwayTm = nil
	}
}

func countWords(text string) int {
	return len(strings.Fields(strings.TrimSpace(text)))
}

func (a *AgentActivity) runEOUDetection(info EndOfTurnInfo) {
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.eouCancel = cancel
	a.eouMu.Unlock()

	a.transcriptMu.Lock()
	a.endOfTurnActive = true
	a.transcriptMu.Unlock()

	go func() {
		defer cancel()
		defer func() {
			a.transcriptMu.Lock()
			a.endOfTurnActive = false
			a.transcriptMu.Unlock()
		}()

		endpointingDelay := a.Session.Options.MinEndpointingDelay
		if endpointingDelay <= 0 {
			endpointingDelay = a.Agent.MinEndpointingDelay
		}
		if endpointingDelay <= 0 {
			endpointingDelay = 0.5 // default
		}

		if a.Agent.TurnDetector != nil && info.NewTranscript != "" {
			// Predict end of turn
			ctxSrc := a.Session.ChatCtx
			if ctxSrc == nil {
				ctxSrc = a.Agent.ChatCtx
			}
			if ctxSrc == nil {
				ctxSrc = llm.NewChatContext()
			}
			chatCtx := ctxSrc.Copy()
			chatCtx.Append(&llm.ChatMessage{
				Role:    llm.ChatRoleUser,
				Content: []llm.ChatContent{{Text: info.NewTranscript}},
			})

			prob, err := a.Agent.TurnDetector.PredictEndOfTurn(ctx, chatCtx)
			if err == nil {
				logger.Logger.Infow("EOU prediction", "probability", prob)
				// Apply probability threshold logic
				if prob < 0.5 {
					endpointingDelay = a.Session.Options.MaxEndpointingDelay
					if endpointingDelay <= 0 {
						endpointingDelay = a.Agent.MaxEndpointingDelay
					}
					if endpointingDelay <= 0 {
						endpointingDelay = 2.0 // default
					}
				}
			} else {
				logger.Logger.Errorw("EOU prediction failed", err)
			}
		}

		if a.Session.Options.PreemptiveGeneration && info.NewTranscript != "" && endpointingDelay > 0.05 {
			endpointingDelay = 0.05
		}

		timer := time.NewTimer(time.Duration(endpointingDelay * float64(time.Second)))
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// EOU detected
			logger.Logger.Infow("EOU detected, completing user turn")

			transcript := info.NewTranscript
			if transcript == "" {
				a.transcriptMu.Lock()
				if !a.sttEOSReceived {
					waitCh := make(chan struct{})
					go func() {
						a.transcriptMu.Lock()
						defer a.transcriptMu.Unlock()
						for !a.sttEOSReceived {
							a.finalTranscriptCond.Wait()
						}
						close(waitCh)
					}()
					a.transcriptMu.Unlock()

					timeout := info.TranscriptTimeout
					if timeout <= 0 {
						timeout = 2 * time.Second
					}
					select {
					case <-waitCh:
					case <-time.After(timeout): // transcript timeout
						logger.Logger.Warnw("final transcript not received after timeout", nil, "timeout", timeout)

						// Simulate FinalTranscript event using interim transcript
						a.transcriptMu.Lock()
						interim := a.audioInterimTranscript
						a.transcriptMu.Unlock()

						if interim != "" {
							if a.Session.Timeline != nil {
								a.Session.Timeline.AddEvent(&UserInputTranscribedEvent{
									Transcript: interim,
									IsFinal:    true,
									CreatedAt:  time.Now(),
								})
							}

							// Trigger hooks to process it properly
							if a.recog != nil && a.recog.hooks != nil {
								a.recog.hooks.OnFinalTranscript(&stt.SpeechEvent{
									Type: stt.SpeechEventFinalTranscript,
									Alternatives: []stt.SpeechData{
										{Text: interim},
									},
								})
							}
						}
					case <-ctx.Done():
						return
					}
					a.transcriptMu.Lock()
				}

				if a.audioTranscript != "" {
					transcript = a.audioTranscript
				}
				a.audioInterimTranscript = ""
				a.transcriptMu.Unlock()
			}

			if transcript == "" {
				logger.Logger.Infow("EOU detected but transcript is empty, ignoring user turn")
				return
			}

			if a.Session.Timeline != nil {
				a.Session.Timeline.AddEvent(&UserInputTranscribedEvent{
					Transcript: transcript,
					IsFinal:    true,
					CreatedAt:  time.Now(),
				})
			}

			if a.Session != nil && a.Session.ivrActivity != nil {
				a.Session.ivrActivity.OnUserInputTranscribed(&ivr.UserInputTranscribedEvent{
					Transcript: transcript,
					IsFinal:    true,
				})
			}

			newMsg := &llm.ChatMessage{
				Role:      llm.ChatRoleUser,
				Content:   []llm.ChatContent{{Text: transcript}},
				CreatedAt: time.Now(),
			}
			chatCtx := a.Session.ChatCtx
			if chatCtx == nil {
				chatCtx = a.Agent.ChatCtx
			}
			if chatCtx == nil {
				chatCtx = llm.NewChatContext()
				a.Session.ChatCtx = chatCtx
				a.Agent.ChatCtx = chatCtx
			}

			if info.SkipReply {
				logger.Logger.Infow("user turn completed, skipping reply generation")
				return
			}

			if err := a.AgentIntf.OnUserTurnCompleted(a.ctx, chatCtx, newMsg); err != nil {
				if err == llm.ErrStopResponse {
					logger.Logger.Infow("user turn completed returned StopResponse, dropping turn")
					return
				}
				logger.Logger.Errorw("on user turn completed failed", err)
			}
		}
	}()
}

func (a *AgentActivity) ClearUserTurn() {
	a.discardUserTurn = true
	a.transcriptMu.Lock()
	a.audioTranscript = ""
	a.audioInterimTranscript = ""
	a.userTurnCommitted = false
	a.transcriptMu.Unlock()
}

type CommitUserTurnOpts struct {
	AudioDetached     bool
	TranscriptTimeout time.Duration
	STTFlushDuration  time.Duration
	SkipReply         bool
}

func (a *AgentActivity) CommitUserTurn(opts *CommitUserTurnOpts) {
	if opts == nil {
		opts = &CommitUserTurnOpts{}
	}

	a.transcriptMu.Lock()
	a.userTurnCommitted = true
	a.transcriptMu.Unlock()

	if a.recog != nil {
		if opts.AudioDetached {
			duration := opts.STTFlushDuration
			if duration <= 0 {
				duration = 2 * time.Second
			}

			// push silence frames
			sampleRate := 16000 // default

			// 0.2s chunk size
			numSamples := int(float64(sampleRate) * 0.2)
			frameSize := numSamples * 2 // 16-bit
			frameData := make([]byte, frameSize)

			numFrames := int(duration.Seconds() / 0.2)
			if numFrames < 1 {
				numFrames = 1
			}

			for i := 0; i < numFrames; i++ {
				_ = a.recog.PushAudio(&model.AudioFrame{
					Data:              frameData,
					SampleRate:        uint32(sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(numSamples),
				})
			}
		}

		// Explicitly flush the STT stream to force final transcript generation
		_ = a.recog.Flush()
	}
	a.runEOUDetection(EndOfTurnInfo{
		TranscriptTimeout: opts.TranscriptTimeout,
		STTFlushDuration:  opts.STTFlushDuration,
		SkipReply:         opts.SkipReply,
	})
}

func (a *AgentActivity) Pause() error {
	a.PauseScheduling()
	return a.Interrupt(false)
}

func (a *AgentActivity) Resume() error {
	a.ResumeScheduling()
	return nil
}

func (a *AgentActivity) UpdateOptions(opts AgentSessionOptions) {
	// For now just copy them
	a.Session.Options = opts
}

