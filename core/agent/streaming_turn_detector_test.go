package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/vad"
	"math"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

type legacyTurnDetector struct{}

func (legacyTurnDetector) PredictEndOfTurn(context.Context, *llm.ChatContext) (float64, error) {
	return .7, nil
}
func (legacyTurnDetector) UnlikelyThreshold(string) (float64, bool) { return .6, true }
func TestStreamingTurnTextCompatibility(t *testing.T) {
	d := WrapTextTurnDetector(legacyTurnDetector{})
	a := NewAgent("test")
	a.TurnDetector = d
	if a.TurnDetector.Capabilities().Input != TurnDetectorInputText {
		t.Fatal("wrong capability")
	}
	p, err := d.PredictEndOfTurn(context.Background(), llm.NewChatContext())
	if err != nil || p != .7 {
		t.Fatalf("prediction: %v %v", p, err)
	}
	if threshold, ok := d.UnlikelyThreshold("en"); !ok || threshold != .6 {
		t.Fatal("lost threshold")
	}
}

type streamTurnCall struct {
	ctx   context.Context
	reply chan AudioTurnResult
	fail  chan error
}
type fakeTurnStream struct {
	pushErr      error
	mu           sync.Mutex
	frames       []byte
	positions    []time.Duration
	resets       []time.Duration
	discards     [][2]time.Duration
	closes       int
	ends         int
	calls        chan streamTurnCall
	pushed       chan struct{}
	ignoreCancel bool
}

func newFakeTurnStream() *fakeTurnStream {
	return &fakeTurnStream{calls: make(chan streamTurnCall, 10), pushed: make(chan struct{}, 10)}
}
func (s *fakeTurnStream) PushAudio(f *model.AudioFrame, p time.Duration) error {
	s.mu.Lock()
	if s.pushErr != nil {
		err := s.pushErr
		s.mu.Unlock()
		return err
	}
	s.frames = append(s.frames, f.Data...)
	s.positions = append(s.positions, p)
	s.mu.Unlock()
	s.pushed <- struct{}{}
	return nil
}
func (s *fakeTurnStream) Predict(ctx context.Context) (AudioTurnResult, error) {
	call := streamTurnCall{ctx: ctx, reply: make(chan AudioTurnResult, 1), fail: make(chan error, 1)}
	s.calls <- call
	if s.ignoreCancel {
		return <-call.reply, nil
	}
	select {
	case <-ctx.Done():
		return AudioTurnResult{}, ctx.Err()
	case err := <-call.fail:
		return AudioTurnResult{}, err
	case result := <-call.reply:
		return result, nil
	}
}
func (s *fakeTurnStream) Reset(p time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resets = append(s.resets, p)
	return nil
}
func (s *fakeTurnStream) DiscardAudio(start, end time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discards = append(s.discards, [2]time.Duration{start, end})
	return nil
}
func (*fakeTurnStream) SupportsLanguage(l string) bool           { return l != "unsupported" }
func (*fakeTurnStream) UnlikelyThreshold(string) (float64, bool) { return .5, true }
func (*fakeTurnStream) PredictionTimeout() time.Duration         { return 20 * time.Millisecond }
func (*fakeTurnStream) Model() string                            { return "fake" }
func (*fakeTurnStream) Provider() string                         { return "test" }
func (s *fakeTurnStream) EndInput() error                        { s.mu.Lock(); defer s.mu.Unlock(); s.ends++; return nil }
func (s *fakeTurnStream) Close() error                           { s.mu.Lock(); defer s.mu.Unlock(); s.closes++; return nil }

type fakeStreamDetector struct{ streams []*fakeTurnStream }

func (*fakeStreamDetector) Capabilities() TurnDetectorCapabilities {
	return TurnDetectorCapabilities{Input: TurnDetectorInputAudio, MinSilenceDuration: 200 * time.Millisecond}
}
func (d *fakeStreamDetector) Stream(context.Context) (TurnDetectorStream, error) {
	s := newFakeTurnStream()
	d.streams = append(d.streams, s)
	return s, nil
}
func setupTurnStream(t *testing.T) (*AgentActivity, *fakeStreamDetector, *fakeTurnStream) {
	t.Helper()
	d := &fakeStreamDetector{}
	a := NewAgent("test")
	a.TurnDetection = TurnDetectionModeVAD
	a.TurnDetector = d
	s := NewAgentSession(a, nil, AgentSessionOptions{})
	activity := NewAgentActivity(a, s)
	if err := s.turnStream.configure(activity.ctx, d); err != nil {
		t.Fatal(err)
	}
	s.turnStream.activate(activity)
	t.Cleanup(func() { activity.Stop(); _ = s.turnStream.close() })
	return activity, d, d.streams[0]
}
func turnVAD(a *AgentActivity, kind vad.VADEventType, at, speech, silence float64) {
	a.Session.turnStream.vadEvent(a, &vad.VADEvent{Type: kind, Timestamp: at, SpeechDuration: speech, RawAccumulatedSpeech: speech, RawAccumulatedSilence: silence}, 0)
}
func nextTurnCall(t *testing.T, s *fakeTurnStream) streamTurnCall {
	t.Helper()
	select {
	case c := <-s.calls:
		return c
	case <-time.After(time.Second):
		t.Fatal("no prediction")
		return streamTurnCall{}
	}
}
func TestStreamingTurnPauseResumeAndLateResult(t *testing.T) {
	a, _, s := setupTurnStream(t)
	s.ignoreCancel = true
	turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
	turnVAD(a, vad.VADEventInferenceDone, 1.2, 0, .2)
	first := nextTurnCall(t, s)
	old, _, _ := a.Session.turnStream.snapshot(a)
	turnVAD(a, vad.VADEventInferenceDone, 1.3, .03, 0)
	select {
	case <-first.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("not canceled")
	}
	turnVAD(a, vad.VADEventInferenceDone, 1.5, 0, .2)
	second := nextTurnCall(t, s)
	first.reply <- AudioTurnResult{Probability: 1, IsComplete: true}
	if a.Session.turnStream.current(a, old) {
		t.Fatal("old generation remains current")
	}
	second.reply <- AudioTurnResult{Probability: .1}
	if got := a.streamingEndpointingDelay(context.Background(), "en", .3, 2.5); got != 2.5 {
		t.Fatalf("delay %v", got)
	}
}
func TestStreamingTurnPredictionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  AudioTurnResult
		err     error
		timeout bool
		want    float64
	}{
		{name: "complete", result: AudioTurnResult{Probability: .9, IsComplete: true}, want: .3},
		{name: "incomplete", result: AudioTurnResult{Probability: .1}, want: 2.5},
		{name: "error", err: errors.New("inference failed"), want: .3},
		{name: "timeout", timeout: true, want: .3},
		{name: "invalid", result: AudioTurnResult{Probability: math.NaN()}, want: .3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, s := setupTurnStream(t)
			turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
			turnVAD(a, vad.VADEventInferenceDone, 1.2, 0, .2)
			call := nextTurnCall(t, s)
			if tc.err != nil {
				call.fail <- tc.err
			} else if !tc.timeout {
				call.reply <- tc.result
			}
			if got := a.streamingEndpointingDelay(context.Background(), "en", .3, 2.5); got != tc.want {
				t.Fatalf("delay %v want %v", got, tc.want)
			}
			if tc.timeout {
				select {
				case <-call.ctx.Done():
				case <-time.After(time.Second):
					t.Fatal("timeout failed to cancel")
				}
			}
		})
	}
}
func TestStreamingTurnResetDiscardAndHandoff(t *testing.T) {
	a, d, s := setupTurnStream(t)
	turnVAD(a, vad.VADEventStartOfSpeech, 1.1, .1, 0)
	turnVAD(a, vad.VADEventEndOfSpeech, 1.5, .1, .3)
	a.Session.turnStream.discardBackchannel(a)
	if len(s.discards) != 1 || s.discards[0] != [2]time.Duration{time.Second, 1500 * time.Millisecond} {
		t.Fatalf("discard %v", s.discards)
	}
	a.ClearUserTurn()
	if len(s.resets) != 1 {
		t.Fatal("clear did not reset")
	}
	turnVAD(a, vad.VADEventStartOfSpeech, 2, .1, 0)
	turnVAD(a, vad.VADEventInferenceDone, 2.2, 0, .2)
	call := nextTurnCall(t, s)
	generation, _, _ := a.Session.turnStream.snapshot(a)
	next := NewAgentActivity(a.Agent, a.Session)
	defer next.Stop()
	if err := a.Session.turnStream.configure(next.ctx, d); err != nil {
		t.Fatal(err)
	}
	a.Session.turnStream.activate(next)
	a.Stop()
	if len(d.streams) != 1 || s.closes != 0 {
		t.Fatal("same detector not reused")
	}
	select {
	case <-call.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("old request still live")
	}
	if a.Session.turnStream.claim(a, generation, context.Background(), func() { t.Fatal("stale clear") }) {
		t.Fatal("stale claim")
	}
	other := &fakeStreamDetector{}
	if err := a.Session.turnStream.configure(next.ctx, other); err != nil {
		t.Fatal(err)
	}
	if s.closes != 1 || s.ends != 1 {
		t.Fatalf("old stream lifecycle %d %d", s.closes, s.ends)
	}
	_ = a.Session.turnStream.close()
	_ = a.Session.turnStream.close()
	if other.streams[0].closes != 1 {
		t.Fatal("double close")
	}
}
func TestStreamingTurnCommitRejectsNewSpeech(t *testing.T) {
	a, _, _ := setupTurnStream(t)
	turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
	turnVAD(a, vad.VADEventEndOfSpeech, 1.3, .1, .2)
	old, _, _ := a.Session.turnStream.snapshot(a)
	turnVAD(a, vad.VADEventStartOfSpeech, 1.4, .1, 0)
	if a.Session.turnStream.claim(a, old, context.Background(), func() { t.Fatal("cleared newer turn") }) {
		t.Fatal("stale commit")
	}
	turnVAD(a, vad.VADEventEndOfSpeech, 1.8, .1, .3)
	current, _, _ := a.Session.turnStream.snapshot(a)
	cleared := false
	if !a.Session.turnStream.claim(a, current, context.Background(), func() { cleared = true }) || !cleared {
		t.Fatal("current turn not committed")
	}
	if a.Session.turnStream.claim(a, current, context.Background(), func() { t.Fatal("double commit") }) {
		t.Fatal("double claim")
	}
}
func TestStreamingTurnRealPipelineOrderingAndIsolation(t *testing.T) {
	a, d, stream := setupTurnStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	pipeline.SetAudioInputHook(func(_ context.Context, f *model.AudioFrame) *model.AudioFrame {
		if f.Data[0] == 0 {
			return nil
		}
		return f
	})
	if err := pipeline.Start(ctx, a.Session); err != nil {
		t.Fatal(err)
	}
	for _, b := range []byte{0, 1, 2} {
		pipeline.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{b, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	}
	for range 2 {
		select {
		case <-stream.pushed:
		case <-time.After(time.Second):
			t.Fatal("missing audio")
		}
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if !reflect.DeepEqual(stream.frames, []byte{1, 0, 2, 0}) || stream.positions[1] != time.Second/16000 {
		t.Fatalf("ordering %v %v", stream.frames, stream.positions)
	}
	var other turnDetectorRuntime
	if err := other.configure(ctx, d); err != nil {
		t.Fatal(err)
	}
	defer other.close()
	if len(d.streams) != 2 || len(d.streams[1].frames) != 0 {
		t.Fatal("sessions share stream")
	}
}
func TestStreamingTurnDefaultsAndValidation(t *testing.T) {
	a, _, _ := setupTurnStream(t)
	a.applyDetectorEndpointingDefaults()
	if got := a.EndpointingOpts(); got.MinDelay != .3 || got.MaxDelay != 2.5 {
		t.Fatalf("stream defaults %+v", got)
	}
	a.Agent.TurnDetector = WrapTextTurnDetector(legacyTurnDetector{})
	a.applyDetectorEndpointingDefaults()
	if got := a.EndpointingOpts(); got.MinDelay != .5 || got.MaxDelay != 3 {
		t.Fatalf("text defaults %+v", got)
	}
	if err := validateTurnDetector(&fakeStreamDetector{}, nil); err == nil {
		t.Fatal("accepted missing VAD")
	}
	if err := validateTurnDetector(WrapTextTurnDetector(nil), nil); err == nil {
		t.Fatal("accepted nil text")
	}
}

// TestStreamingTurnReferenceScenario executes the same VAD schedule as the
// pinned Python AudioRecognition runner; default tests require no Python.
func TestStreamingTurnReferenceScenario(t *testing.T) {
	path := os.Getenv("TURN_DETECTOR_SCENARIO")
	if path == "" {
		path = "../../scripts/parity-fixtures/streaming-turn.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	type event struct {
		Type        string  `json:"type"`
		Speech      float64 `json:"speech,omitempty"`
		Silence     float64 `json:"silence,omitempty"`
		Pending     bool    `json:"pending"`
		Predictions int     `json:"predictions"`
	}
	var scenario struct {
		Contract string  `json:"contract"`
		Events   []event `json:"events"`
	}
	if err = json.Unmarshal(data, &scenario); err != nil {
		t.Fatal(err)
	}
	a, _, stream := setupTurnStream(t)
	predictions := 0
	events := make([]event, 0, len(scenario.Events))
	for _, input := range scenario.Events {
		_, before, _ := a.Session.turnStream.snapshot(a)
		if input.Type == "clear" {
			a.ClearUserTurn()
		} else {
			turnVAD(a, vad.VADEventType(input.Type), 1, input.Speech, input.Silence)
		}
		_, after, _ := a.Session.turnStream.snapshot(a)
		if after != nil && before != after {
			_ = nextTurnCall(t, stream)
			predictions++
		}
		got := event{Type: input.Type, Pending: after != nil, Predictions: predictions}
		if got.Pending != input.Pending || got.Predictions != input.Predictions {
			t.Fatalf("event %s: %+v expected %+v", input.Type, got, input)
		}
		events = append(events, got)
	}
	scenario.Events = events
	data, err = json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TURN_TRACE %s", data)
}

func TestStreamingTurnEOUCommitAndLateResult(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprint(late), func(t *testing.T) {
			d := &fakeStreamDetector{}
			user := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
			user.TurnDetector = d
			user.TurnDetection = TurnDetectionModeVAD
			session := NewAgentSession(user, nil, AgentSessionOptions{MinEndpointingDelay: .001, MaxEndpointingDelay: .002})
			a := NewAgentActivity(user, session)
			defer a.Stop()
			if err := session.turnStream.configure(a.ctx, d); err != nil {
				t.Fatal(err)
			}
			defer session.turnStream.close()
			session.turnStream.activate(a)
			stream := d.streams[0]
			stream.ignoreCancel = late
			turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
			turnVAD(a, vad.VADEventInferenceDone, 1.2, 0, .2)
			call := nextTurnCall(t, stream)
			turnVAD(a, vad.VADEventEndOfSpeech, 1.3, 0, .3)
			a.runEOUDetection(EndOfTurnInfo{NewTranscript: "accepted", TranscriptConfidence: 1, Language: "en"})
			a.eouMu.Lock()
			done := a.eouDone
			a.eouMu.Unlock()
			if late {
				turnVAD(a, vad.VADEventStartOfSpeech, 1.4, .1, 0)
				a.userTurnMu.Lock()
				a.pendingUserTranscript = "new turn"
				a.userTurnMu.Unlock()
			}
			call.reply <- AudioTurnResult{Probability: .9, IsComplete: true}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("EOU blocked")
			}
			if late {
				select {
				case <-user.turns:
					t.Fatal("late prediction committed")
				default:
				}
				if a.pendingUserTranscript != "new turn" {
					t.Fatal("late result cleared next transcript")
				}
			} else {
				select {
				case msg := <-user.turns:
					if msg.TextContent() != "accepted" {
						t.Fatal(msg.TextContent())
					}
				default:
					t.Fatal("turn not committed")
				}
				if len(stream.resets) != 1 {
					t.Fatalf("commit resets %v", stream.resets)
				}
			}
		})
	}
}
func TestStreamingTurnAdaptiveBackchannelBoundary(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(fmt.Sprint(early), func(t *testing.T) {
			a, recorder := newBackchannelTurnActivity(t)
			defer a.Stop()
			a.Agent.TurnDetection = TurnDetectionModeVAD
			d := &fakeStreamDetector{}
			a.Agent.TurnDetector = d
			if err := a.Session.turnStream.configure(a.ctx, d); err != nil {
				t.Fatal(err)
			}
			defer a.Session.turnStream.close()
			a.Session.turnStream.activate(a)
			turnVAD(a, vad.VADEventStartOfSpeech, 1.1, .1, 0)
			a.OnStartOfSpeech(nil)
			started := a.userSpeechStartedAt
			verdict := OverlappingSpeechEvent{OverlapStartedAt: &started, DetectedAt: time.Now()}
			if early {
				a.OnOverlapSpeechEnded(verdict)
				if len(d.streams[0].discards) != 0 {
					t.Fatal("discard during speech")
				}
			}
			turnVAD(a, vad.VADEventEndOfSpeech, 1.5, 0, .3)
			a.OnEndOfSpeech(nil)
			if !early {
				a.OnOverlapSpeechEnded(verdict)
			}
			if len(d.streams[0].discards) != 1 {
				t.Fatalf("discard count %v", d.streams[0].discards)
			}
			if a.Session.userTurnSpan != nil || !a.userTurnStartedAt.IsZero() {
				t.Fatal("backchannel turn retained")
			}
			if len(recorder.Ended()) == 0 {
				t.Fatal("missing span closure")
			}
			old := verdict
			turnVAD(a, vad.VADEventStartOfSpeech, 2.1, .1, 0)
			a.OnStartOfSpeech(nil)
			a.OnOverlapSpeechEnded(old)
			if len(d.streams[0].discards) != 1 {
				t.Fatal("stale verdict discarded next audio")
			}
		})
	}
}

func TestStreamingTurnModeChangeAndSyntheticEOS(t *testing.T) {
	a, _, stream := setupTurnStream(t)
	turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
	turnVAD(a, vad.VADEventInferenceDone, 1.2, 0, .2)
	call := nextTurnCall(t, stream)
	mode := TurnDetectionModeManual
	if err := a.UpdateOptions(AgentSessionUpdateOptions{TurnDetection: &mode}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-call.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("mode change left prediction active")
	}
	a.Session.turnStream.syntheticEnd(a)
	generation, _, _ := a.Session.turnStream.snapshot(a)
	if !a.Session.turnStream.claim(a, generation, context.Background(), func() {}) {
		t.Fatal("synthetic EOS prevented fallback commit")
	}
}
func TestStreamingTurnHandoffDuringSpeechRearms(t *testing.T) {
	a, d, stream := setupTurnStream(t)
	turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
	turnVAD(a, vad.VADEventInferenceDone, 1.2, 0, .2)
	old := nextTurnCall(t, stream)
	next := NewAgentActivity(a.Agent, a.Session)
	defer next.Stop()
	if err := a.Session.turnStream.configure(next.ctx, d); err != nil {
		t.Fatal(err)
	}
	a.Session.turnStream.activate(next)
	turnVAD(next, vad.VADEventInferenceDone, 1.3, 0, .3)
	current := nextTurnCall(t, stream)
	select {
	case <-old.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("old owner inference active")
	}
	current.reply <- AudioTurnResult{Probability: 1, IsComplete: true}
	if _, ok := next.streamingTurnPrediction(context.Background()); !ok {
		t.Fatal("new owner has no prediction")
	}
}
func TestStreamingTurnExplicitEndpointingPreserved(t *testing.T) {
	user := NewAgent("test")
	user.TurnDetector = &fakeStreamDetector{}
	s := NewAgentSession(user, nil, AgentSessionOptions{MinEndpointingDelay: .7, MaxEndpointingDelay: 4})
	a := NewAgentActivity(user, s)
	defer a.Stop()
	a.applyDetectorEndpointingDefaults()
	if got := a.EndpointingOpts(); got.MinDelay != .7 || got.MaxDelay != 4 {
		t.Fatalf("overrode explicit delays: %+v", got)
	}
}

type unavailableStreamDetector struct{ nilStream bool }

func (*unavailableStreamDetector) Capabilities() TurnDetectorCapabilities {
	return TurnDetectorCapabilities{Input: TurnDetectorInputAudio, MinSilenceDuration: 200 * time.Millisecond}
}
func (d *unavailableStreamDetector) Stream(context.Context) (TurnDetectorStream, error) {
	if d.nilStream {
		return nil, nil
	}
	return nil, errors.New("backend unavailable")
}
func TestStreamingTurnSessionOwnershipAndUnavailableFallback(t *testing.T) {
	for _, failure := range []string{"", "error", "nil"} {
		t.Run(failure, func(t *testing.T) {
			d := &fakeStreamDetector{}
			a := NewAgent("test")
			a.TurnDetector = d
			a.VAD = vad.NewSimpleVAD(.1)
			if failure != "" {
				a.TurnDetector = &unavailableStreamDetector{nilStream: failure == "nil"}
			}
			s := NewAgentSession(a, nil, AgentSessionOptions{DisableUserAwayTimeout: true})
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.Stop(context.Background())
			if failure != "" {
				_, _, stream := s.turnStream.snapshot(s.currentActivity())
				if stream != nil {
					t.Fatal("failed stream was installed")
				}
				return
			}
			if len(d.streams) != 1 {
				t.Fatal("missing session stream")
			}
			next := NewAgent("handoff")
			next.TurnDetector = d
			s.UpdateAgent(next)
			if len(d.streams) != 1 || d.streams[0].closes != 0 {
				t.Fatal("same instance not reused")
			}
			replacement := &fakeStreamDetector{}
			third := NewAgent("replacement")
			third.TurnDetector = replacement
			s.UpdateAgent(third)
			if d.streams[0].closes != 1 || len(replacement.streams) != 1 {
				t.Fatal("replacement ownership")
			}
			if err := s.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if replacement.streams[0].closes != 1 || replacement.streams[0].ends != 1 {
				t.Fatal("shutdown ownership")
			}
		})
	}
}

func TestStreamingTurnInputFailureDoesNotStrandEOU(t *testing.T) {
	d := &fakeStreamDetector{}
	user := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	user.TurnDetector = d
	user.TurnDetection = TurnDetectionModeVAD
	s := NewAgentSession(user, nil, AgentSessionOptions{MinEndpointingDelay: .001, MaxEndpointingDelay: .002})
	a := NewAgentActivity(user, s)
	defer a.Stop()
	defer s.turnStream.close()
	if err := s.turnStream.configure(a.ctx, d); err != nil {
		t.Fatal(err)
	}
	s.turnStream.activate(a)
	stream := d.streams[0]
	turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
	turnVAD(a, vad.VADEventInferenceDone, 1.2, 0, .2)
	_ = nextTurnCall(t, stream)
	turnVAD(a, vad.VADEventEndOfSpeech, 1.3, 0, .3)
	a.runEOUDetection(EndOfTurnInfo{NewTranscript: "fallback", TranscriptConfidence: 1})
	stream.mu.Lock()
	stream.pushErr = errors.New("broken audio input")
	stream.mu.Unlock()
	if err := s.turnStream.push(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("missing input error")
	}
	a.eouMu.Lock()
	done := a.eouDone
	a.eouMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("EOU stranded")
		}
	}
	select {
	case <-user.turns:
	default:
		t.Fatal("input failure prevented fallback commit")
	}
}
