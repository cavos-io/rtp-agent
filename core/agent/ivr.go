package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
)

type IVRActivity struct {
	Session *AgentSession

	ctx    context.Context
	cancel context.CancelFunc

	mu                      sync.Mutex
	maxSilenceDuration      time.Duration
	currentUserState        UserState
	currentAgentState       AgentState
	silenceTimer            *time.Timer
	lastShouldScheduleCheck bool
	loopDetector            *ivrLoopDetector
}

func NewIVRActivity(session *AgentSession) *IVRActivity {
	ctx, cancel := context.WithCancel(context.Background())
	maxSilenceDuration := session.Options.IVRSilenceDuration
	if maxSilenceDuration == 0 {
		maxSilenceDuration = 5 * time.Second
	}
	return &IVRActivity{
		Session:            session,
		ctx:                ctx,
		cancel:             cancel,
		maxSilenceDuration: maxSilenceDuration,
		currentUserState:   session.UserStateValue(),
		currentAgentState:  session.AgentStateValue(),
		loopDetector:       newIVRLoopDetector(20, 3),
	}
}

func (i *IVRActivity) Start() {
	userStateEvents := i.Session.UserStateChangedEvents()
	agentStateEvents := i.Session.AgentStateChangedEvents()
	userTranscriptEvents := i.Session.UserInputTranscribedEvents()
	go i.watchUserState(userStateEvents)
	go i.watchAgentState(agentStateEvents)
	go i.watchUserInputTranscripts(userTranscriptEvents)
	i.scheduleSilenceCheck()
}

func (i *IVRActivity) Stop() {
	i.cancel()
	i.mu.Lock()
	if i.silenceTimer != nil {
		i.silenceTimer.Stop()
		i.silenceTimer = nil
	}
	i.mu.Unlock()
}

func (i *IVRActivity) watchUserState(events <-chan UserStateChangedEvent) {
	for {
		select {
		case <-i.ctx.Done():
			return
		case ev := <-events:
			i.mu.Lock()
			i.currentUserState = ev.NewState
			i.mu.Unlock()
			i.scheduleSilenceCheck()
		}
	}
}

func (i *IVRActivity) watchAgentState(events <-chan AgentStateChangedEvent) {
	for {
		select {
		case <-i.ctx.Done():
			return
		case ev := <-events:
			i.mu.Lock()
			i.currentAgentState = ev.NewState
			i.mu.Unlock()
			i.scheduleSilenceCheck()
		}
	}
}

func (i *IVRActivity) watchUserInputTranscripts(events <-chan UserInputTranscribedEvent) {
	for {
		select {
		case <-i.ctx.Done():
			return
		case ev := <-events:
			i.onUserInputTranscribed(ev)
		}
	}
}

func (i *IVRActivity) onUserInputTranscribed(ev UserInputTranscribedEvent) {
	if !ev.IsFinal {
		return
	}
	i.mu.Lock()
	loopDetected := i.loopDetector.addAndCheck(ev.Transcript)
	i.mu.Unlock()
	if !loopDetected {
		return
	}
	logger.Logger.Debugw("IVRActivity: speech loop detected; sending notification")
	allowInterruptions := false
	if _, err := i.Session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		AllowInterruptions: &allowInterruptions,
	}); err != nil && err != ErrAgentSessionNotRunning {
		i.Session.EmitError(ErrorEvent{Error: err, CreatedAt: time.Now()})
	}
	i.mu.Lock()
	i.loopDetector.reset()
	i.mu.Unlock()
}

func (i *IVRActivity) scheduleSilenceCheck() {
	i.mu.Lock()
	shouldSchedule := i.shouldScheduleCheckLocked()
	if shouldSchedule {
		if i.lastShouldScheduleCheck {
			i.mu.Unlock()
			return
		}
		if i.silenceTimer != nil {
			i.silenceTimer.Stop()
		}
		i.silenceTimer = time.AfterFunc(i.maxSilenceDuration, i.onSilenceDetected)
	} else if i.silenceTimer != nil {
		i.silenceTimer.Stop()
		i.silenceTimer = nil
	}
	i.lastShouldScheduleCheck = shouldSchedule
	i.mu.Unlock()
}

func (i *IVRActivity) shouldScheduleCheckLocked() bool {
	isUserSilent := i.currentUserState == UserStateListening || i.currentUserState == UserStateAway
	isAgentSilent := i.currentAgentState == AgentStateIdle || i.currentAgentState == AgentStateListening
	return isUserSilent && isAgentSilent
}

func (i *IVRActivity) onSilenceDetected() {
	i.mu.Lock()
	i.silenceTimer = nil
	i.lastShouldScheduleCheck = false
	i.mu.Unlock()

	logger.Logger.Debugw("IVRActivity: silence detected; sending notification")
	if _, err := i.Session.GenerateReply(context.Background(), ""); err != nil && err != ErrAgentSessionNotRunning {
		i.Session.EmitError(ErrorEvent{Error: err, CreatedAt: time.Now()})
	}
	i.scheduleSilenceCheck()
}

type ivrLoopDetector struct {
	windowSize           int
	consecutiveThreshold int
	chunks               []string
	consecutiveSimilar   int
}

func newIVRLoopDetector(windowSize, consecutiveThreshold int) *ivrLoopDetector {
	return &ivrLoopDetector{
		windowSize:           windowSize,
		consecutiveThreshold: consecutiveThreshold,
		chunks:               make([]string, 0, windowSize),
	}
}

func (d *ivrLoopDetector) addAndCheck(chunk string) bool {
	normalized := strings.Join(strings.Fields(strings.ToLower(chunk)), " ")
	if normalized == "" {
		return false
	}
	previous := ""
	if len(d.chunks) > 0 {
		previous = d.chunks[len(d.chunks)-1]
	}
	d.chunks = append(d.chunks, normalized)
	if len(d.chunks) > d.windowSize {
		d.chunks = d.chunks[len(d.chunks)-d.windowSize:]
	}
	if previous != "" && previous == normalized {
		d.consecutiveSimilar++
	} else {
		d.consecutiveSimilar = 0
	}
	return d.consecutiveSimilar >= d.consecutiveThreshold
}

func (d *ivrLoopDetector) reset() {
	d.chunks = d.chunks[:0]
	d.consecutiveSimilar = 0
}
