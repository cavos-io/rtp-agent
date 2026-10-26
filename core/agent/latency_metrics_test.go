package agent

import (
	"context"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type latencyLLM struct {
	fakeGenerationLLM
	setup time.Duration
}

func (l *latencyLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	time.Sleep(l.setup)
	return l.stream, nil
}

type latencyLLMStream struct {
	chunks []*llm.ChatChunk
	delays []time.Duration
	index  int
}

func (s *latencyLLMStream) Next() (*llm.ChatChunk, error) {
	if s.index == len(s.chunks) {
		return nil, io.EOF
	}
	i := s.index
	s.index++
	time.Sleep(s.delays[i])
	return s.chunks[i], nil
}
func (*latencyLLMStream) Close() error { return nil }
func TestLatencyTTFTIncludesChatSetup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &latencyLLM{setup: 300 * time.Millisecond}
		l.stream = &latencyLLMStream{chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: "hello"}}}, delays: []time.Duration{200 * time.Millisecond}}
		data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
		if err != nil {
			t.Fatal(err)
		}
		<-data.Done
		if data.TTFT != 500*time.Millisecond {
			t.Fatalf("TTFT=%s; want 500ms including 300ms Chat setup", data.TTFT)
		}
	})
}
func TestLatencyTTFTIgnoresMetadata(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &latencyLLM{}
		l.stream = &latencyLLMStream{chunks: []*llm.ChatChunk{{Usage: &llm.CompletionUsage{}}, {Delta: &llm.ChoiceDelta{Content: "hello"}}}, delays: []time.Duration{100 * time.Millisecond, 400 * time.Millisecond}}
		data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
		if err != nil {
			t.Fatal(err)
		}
		<-data.Done
		if data.TTFT != 500*time.Millisecond {
			t.Fatalf("TTFT=%s; want 500ms at first response, metadata arrived at 100ms", data.TTFT)
		}
	})
}
func TestLatencySTTModeKeepsWordEndTimestamp(t *testing.T) {
	a := NewAgent("review")
	a.STT = &fakePipelineSTT{}
	a.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(a, nil, AgentSessionOptions{MinEndpointingDelay: 60, MaxEndpointingDelay: 60})
	activity := NewAgentActivity(a, session)
	session.activity = activity
	a.activity = activity
	defer activity.Stop()
	origin := time.Now().Add(-1400 * time.Millisecond)
	onset := timeToUnixSeconds(origin)
	va := NewPipelineAgent(nil, a.STT, nil, nil, nil)
	va.session = session
	va.ctx = context.Background()
	stream := &fakePipelineRecognizeStream{startTime: onset, events: []*stt.SpeechEvent{
		{Type: stt.SpeechEventStartOfSpeech, SpeechStartTime: &onset},
		{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "hello", Confidence: 0.9, StartTime: 0, EndTime: 1.0}}},
		{Type: stt.SpeechEventEndOfSpeech, Alternatives: []stt.SpeechData{{EndTime: 1.0}}},
	}}
	va.sttLoop(stream)
	info := activity.pendingFinalEndOfTurnInfo()
	if !info.TranscriptionDelaySet || info.TranscriptionDelay < 0.35 || info.TranscriptionDelay > 0.5 {
		t.Fatalf("STT delay=%.6fs set=%v; want about 0.4s from provider word end", info.TranscriptionDelay, info.TranscriptionDelaySet)
	}
}

type latencyChunkedTTS struct{ fakeGenerationChunkedTTS }

func (*latencyChunkedTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return &latencyChunkedStream{}, nil
}

type latencyChunkedStream struct{ n int }

func (s *latencyChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.n == 2 {
		return nil, io.EOF
	}
	if s.n == 0 {
		time.Sleep(100 * time.Millisecond)
	} else {
		time.Sleep(300 * time.Millisecond)
	}
	s.n++
	return &tts.SynthesizedAudio{Frame: &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}}, nil
}
func (*latencyChunkedStream) Close() error { return nil }
func TestLatencyBatchTTFBUsesFirstFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		input := make(chan string, 1)
		input <- "Hello."
		close(input)
		data, err := PerformTTSInference(context.Background(), &latencyChunkedTTS{}, input, WithTTSPreserveTimedTranscript())
		if err != nil {
			t.Fatal(err)
		}
		for range data.AudioCh {
		}
		if data.TTFB != 100*time.Millisecond {
			t.Fatalf("TTFB=%s; want 100ms at first audio, last audio arrived at 400ms", data.TTFB)
		}
	})
}
func TestLatencyRealtimeReplyHasLatencyMetrics(t *testing.T) {
	session := NewAgentSession(NewAgent("review"), nil, AgentSessionOptions{})
	ma := NewMultimodalAgent(nil, llm.NewChatContext())
	ma.session = session
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }
	text := make(chan string, 1)
	text <- "Hello"
	close(text)
	audio := make(chan *model.AudioFrame, 1)
	audio <- &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	close(audio)
	modes := make(chan []string, 1)
	modes <- []string{"audio"}
	close(modes)
	ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{MessageID: "review", TextCh: text, AudioCh: audio, ModalitiesCh: modes})
	for _, item := range ma.chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleAssistant {
			if len(msg.Metrics) == 0 {
				t.Fatal("realtime assistant reply has no Metrics; no per-turn latency fields can reach report/OTel")
			}
			return
		}
	}
	t.Fatal("missing assistant reply")
}

func TestLatencyUnknownEOTIsOmitted(t *testing.T) {
	report := metricsReportFromEndOfTurn(EndOfTurnInfo{}, 0, nil)
	if v, ok := report["end_of_turn_delay"]; ok {
		t.Fatalf("unknown EOT exported as %v; want omitted like Python", v)
	}
}

func TestLatencyE2EUsesPlaybackStartInsteadOfPublishCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := NewAgentSession(NewAgent("review"), nil, AgentSessionOptions{})
		va := NewPipelineAgent(nil, nil, nil, nil, nil)
		va.session = session
		var playbackStarted time.Time
		playback := &latencyPlaybackController{}
		session.SetAudioPlaybackController(playback)
		va.PublishAudio = func(context.Context, *model.AudioFrame) error {
			playbackStarted = time.Now()
			playback.notify(playbackStarted)
			time.Sleep(300 * time.Millisecond)
			return nil
		}
		audio := make(chan *model.AudioFrame, 1)
		audio <- &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
		close(audio)
		data := &TTSGenerationData{AudioCh: audio}
		_, err := va.playTTSGenerationWithTranscript(context.Background(), session, data, NewTranscriptSynchronizer(0), closedChannel(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := unixSecondsToTime(data.StartedSpeakingAt)
		if got.Sub(playbackStarted) > time.Millisecond {
			t.Fatalf("speech start is %s after playback started; includes PublishAudio processing", got.Sub(playbackStarted))
		}
	})
}

func TestLatencySayIncludesMeasuredTTFB(t *testing.T) {
	session := NewAgentSession(NewAgent("review"), nil, AgentSessionOptions{})
	chat := llm.NewChatContext()
	va := NewPipelineAgent(nil, nil, nil, &fakeGenerationTTS{stream: func() *endInputGenerationTTSStream {
		s := newEndInputGenerationTTSStream()
		s.emitAfterPush = true
		return s
	}()}, chat)
	va.session = session
	va.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.Text = "Hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "Hello"}}}
	va.OnSpeechScheduled(context.Background(), speech)
	for _, item := range chat.Items {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleAssistant {
			if _, ok := msg.Metrics["tts_node_ttfb"]; !ok {
				t.Fatal("Say assistant message omits measured tts_node_ttfb")
			}
			return
		}
	}
	t.Fatal("missing Say message")
}

// The output starts playback before completing a potentially large publication.
type latencyPlaybackController struct {
	notify func(time.Time)
	onWait func()
}

func (*latencyPlaybackController) ClearBuffer() {}
func (p *latencyPlaybackController) WaitForPlayout(context.Context) (AudioPlaybackResult, error) {
	if p.onWait != nil {
		p.onWait()
	}
	return AudioPlaybackResult{}, nil
}
func (p *latencyPlaybackController) OnPlaybackStarted(handler func(time.Time)) func() {
	p.notify = handler
	return func() { p.notify = nil }
}

func TestLatencyPlaybackStartAfterAudioEOF(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
		playback := &latencyPlaybackController{}
		session.SetAudioPlaybackController(playback)
		var started time.Time
		playback.onWait = func() {
			time.Sleep(100 * time.Millisecond)
			started = time.Now()
			if playback.notify != nil {
				playback.notify(started)
			}
		}
		va := NewPipelineAgent(nil, nil, nil, &fakeGenerationTTS{stream: func() *endInputGenerationTTSStream {
			s := newEndInputGenerationTTSStream()
			s.emitAfterPush = true
			return s
		}()}, llm.NewChatContext())
		va.session = session
		va.ctx = context.Background()
		va.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }
		speech := NewSpeechHandle(true, DefaultInputDetails())
		speech.Generation.Text = "hello"
		speech.Generation.AssistantMessage = &llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hello"}}}
		va.OnSpeechScheduled(context.Background(), speech)
		got, _ := speech.Generation.AssistantMessage.Metrics["started_speaking_at"].(float64)
		if got != timeToUnixSeconds(started) {
			t.Fatalf("speech start=%v, want delayed playback event %v", got, timeToUnixSeconds(started))
		}
		if playback.notify != nil {
			t.Fatal("playback subscription leaked after speech completed")
		}
	})
}

func TestLatencyPipelineE2EIsRecordedOnceAcrossTools(t *testing.T) {
	chat := llm.NewChatContext()
	user := &llm.ChatMessage{ID: "user-timing", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "question"}}, Metrics: map[string]any{"stopped_speaking_at": timeToUnixSeconds(time.Now().Add(-time.Second))}}
	provider := &fakeGenerationLLM{streams: []llm.LLMStream{
		&fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: "checking", ToolCalls: []llm.FunctionToolCall{{Type: "function", Name: "lookup", CallID: "lookup", Arguments: "{}"}}}}}},
		&fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: "done"}}}},
	}}
	frame := &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "result"}}
	va := NewPipelineAgent(nil, nil, provider, &fakePipelineTTS{streams: []*fakePipelineTTSStream{{frames: []*model.AudioFrame{frame}}, {frames: []*model.AudioFrame{frame}}}}, chat)
	va.session = session
	va.ctx = context.Background()
	va.generateReplyWithOptions(pipelineReplyOptions{UserMessage: user})
	count := 0
	messages := 0
	for _, item := range chat.Items {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleAssistant {
			messages++
			if _, ok := msg.Metrics["e2e_latency"]; ok {
				count++
			}
		}
	}
	if messages != 2 || count != 1 {
		t.Fatalf("assistant messages=%d, E2E samples=%d; want 2 and 1", messages, count)
	}
}

// These fake providers must receive input before producing a response, just as
// the real provider nodes do. Other tests still exercise unsolicited audio.
type latencyInputGatedTTSStream struct {
	tts.SynthesizeStream
	ready chan struct{}
	once  sync.Once
}

func gateLatencyTTSInput(stream tts.SynthesizeStream) tts.SynthesizeStream {
	return &latencyInputGatedTTSStream{SynthesizeStream: stream, ready: make(chan struct{})}
}
func (s *latencyInputGatedTTSStream) PushText(text string) error {
	err := s.SynthesizeStream.PushText(text)
	s.once.Do(func() { close(s.ready) })
	return err
}
func (s *latencyInputGatedTTSStream) Next() (*tts.SynthesizedAudio, error) {
	<-s.ready
	return s.SynthesizeStream.Next()
}
func (s *latencyInputGatedTTSStream) Close() error {
	s.once.Do(func() { close(s.ready) })
	return s.SynthesizeStream.Close()
}

func TestLatencyTTFTZeroIsNotOverwritten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &latencyLLM{}
		l.stream = &latencyLLMStream{chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: "first"}}, {Delta: &llm.ChoiceDelta{Content: "tail"}}}, delays: []time.Duration{0, time.Second}}
		data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
		if err != nil {
			t.Fatal(err)
		}
		<-data.Done
		if data.TTFT != 0 {
			t.Fatalf("TTFT=%v, want genuine zero at first content", data.TTFT)
		}
	})
}

func TestLatencyRealtimeE2EAnswersUserOnce(t *testing.T) {
	session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
	ma := NewMultimodalAgent(nil, llm.NewChatContext())
	ma.session = session
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }
	ma.lastUserSpeechStoppedAt = time.Now().Add(-time.Second)
	for _, id := range []string{"first", "tool-continuation"} {
		text := make(chan string, 1)
		text <- id
		close(text)
		audio := make(chan *model.AudioFrame, 1)
		audio <- &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
		close(audio)
		modes := make(chan []string, 1)
		modes <- []string{"audio"}
		close(modes)
		ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{MessageID: id, TextCh: text, AudioCh: audio, ModalitiesCh: modes})
	}
	count := 0
	for _, item := range ma.chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok {
			if _, ok := msg.Metrics["e2e_latency"]; ok {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("realtime E2E samples=%d, want one", count)
	}
}

func TestLatencyUnknownAndZeroSurviveCloudMapping(t *testing.T) {
	unknown := metricsReportFromEndOfTurn(EndOfTurnInfo{}, 0, nil)
	report := telemetryMetricsReport(unknown)
	if report == nil || report.EndOfTurnDelay != nil || report.TranscriptionDelay != nil {
		t.Fatal("unknown timing must be absent from protobuf")
	}
	known := metricsReportFromEndOfTurn(EndOfTurnInfo{EndOfTurnDelaySet: true, TranscriptionDelaySet: true}, 0, nil)
	known["llm_node_ttft"] = 0.0
	known["tts_node_ttfb"] = 0.0
	known["e2e_latency"] = 0.0
	report = telemetryMetricsReport(known)
	for name, value := range map[string]*float64{"EOT": report.EndOfTurnDelay, "STT": report.TranscriptionDelay, "TTFT": report.LlmNodeTtft, "TTFB": report.TtsNodeTtfb, "E2E": report.E2ELatency} {
		if value == nil || *value != 0 {
			t.Fatalf("%s lost valid zero", name)
		}
	}
}

type latencyInputGatedTTS struct{ tts.TTS }

func (p latencyInputGatedTTS) Unwrap() tts.TTS { return p.TTS }
func (p latencyInputGatedTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	stream, err := p.TTS.Stream(ctx)
	if err != nil {
		return nil, err
	}
	return gateLatencyTTSInput(stream), nil
}

func TestLatencyRealtimeMetricsStayWithResponseAfterGenerationEnds(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	old := telemetry.Tracer
	telemetry.Tracer = provider.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = provider.Shutdown(context.Background()) })
	session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
	ma := NewMultimodalAgent(nil, llm.NewChatContext())
	ma.session = session
	messages := make(chan llm.MessageGeneration)
	close(messages)
	ma.consumeRealtimeGeneration(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), &llm.GenerationCreatedEvent{ResponseID: "response-timing", MessageCh: messages})
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("inference spans=%d", len(spans))
	}
	parent := spans[0].SpanContext()
	ma.recordRealtimeMetrics(&telemetry.RealtimeModelMetrics{RequestID: "response-timing", TTFT: 0.25})
	spans = recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("total spans=%d", len(spans))
	}
	child := spans[1]
	if child.Parent().SpanID() != parent.SpanID() {
		t.Fatal("late realtime metrics lost response correlation")
	}
	attrs := spanAttributeValues(child.Attributes())
	if attrs[telemetry.AttrGenAIResponseTTFC].AsFloat64() != 0.25 {
		t.Fatal("realtime TTFT was not recorded")
	}
	if len(ma.realtimeSpans) != 0 {
		t.Fatal("completed response correlation was retained")
	}
}

func TestLatencySTTOriginStartsAtFirstAudio(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := &fakePipelineRecognizeStream{}
		va := NewPipelineAgent(nil, nil, nil, nil, nil)
		va.sttStream = stream
		time.Sleep(time.Second)
		want := timeToUnixSeconds(time.Now())
		if err := va.pushSTTFrame(&model.AudioFrame{Data: []byte{0, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
			t.Fatal(err)
		}
		if stream.StartTime() != want {
			t.Fatalf("origin=%v, want first audio at %v", stream.StartTime(), want)
		}
	})
}

func TestLatencyFirstAudibleSegmentOwnsMetrics(t *testing.T) {
	session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
	va := NewPipelineAgent(nil, nil, nil, nil, nil)
	va.session = session
	va.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }
	empty := make(chan *model.AudioFrame)
	close(empty)
	audio := make(chan *model.AudioFrame, 1)
	audio <- &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	close(audio)
	first := &TTSGenerationData{AudioCh: empty}
	audible := &TTSGenerationData{AudioCh: audio, TTFB: 100 * time.Millisecond, TTFBSet: true}
	segments := make(chan precomputedTTSSegment, 2)
	for _, data := range []*TTSGenerationData{first, audible} {
		segments <- precomputedTTSSegment{ttsGen: data, transcriptSync: NewTranscriptSynchronizer(0), transcriptionDone: closedChannel()}
	}
	close(segments)
	got, err := va.playPrecomputedTTSSegments(context.Background(), session, segments, NewSpeechHandle(true, DefaultInputDetails()))
	if err != nil {
		t.Fatal(err)
	}
	defer finalizeTTSPlaybackTiming(got)
	if got != audible || got.StartedSpeakingAt == 0 || got.TTFB != 100*time.Millisecond {
		t.Fatal("silent first segment replaced the first audible segment's timing")
	}
}

func TestLatencyRealtimeQueuedReplyKeepsOriginalUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
		ma := NewMultimodalAgent(nil, llm.NewChatContext())
		ma.session = session
		ma.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }
		ma.lastUserSpeechStoppedAt = time.Now().Add(-100 * time.Millisecond)
		original := timeToUnixSeconds(time.Now().Add(-time.Second))
		speech := NewSpeechHandle(true, DefaultInputDetails())
		speech.Generation.RealtimeGeneration = &llm.GenerationCreatedEvent{UserSpeechStoppedAt: &original}
		text := make(chan string, 1)
		text <- "reply"
		close(text)
		audio := make(chan *model.AudioFrame, 1)
		audio <- &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
		close(audio)
		modes := make(chan []string, 1)
		modes <- []string{"audio"}
		close(modes)
		ma.consumeRealtimeMessage(context.Background(), speech, llm.MessageGeneration{MessageID: "reply", TextCh: text, AudioCh: audio, ModalitiesCh: modes})
		msg := ma.chatCtx.Items[0].(*llm.ChatMessage)
		if msg.Metrics["e2e_latency"] != 1.0 {
			t.Fatalf("E2E=%v, want original user's 1s delay", msg.Metrics["e2e_latency"])
		}
	})
}

func TestLatencyFirstAudibleLiveSegmentOwnsMetrics(t *testing.T) {
	session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
	provider := &fakePipelineTTS{streams: []*fakePipelineTTSStream{{}, {frames: []*model.AudioFrame{{Data: []byte{0, 0}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}}}}}
	va := NewPipelineAgent(nil, nil, nil, provider, nil)
	va.session = session
	va.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }
	input := make(chan LLMTextEvent, 3)
	input <- LLMTextEvent{Text: "silent segment"}
	input <- LLMTextEvent{Flush: true}
	input <- LLMTextEvent{Text: "audible segment"}
	close(input)
	data, err := va.synthesizeSegmentedSpeech(context.Background(), session, input, NewSpeechHandle(true, DefaultInputDetails()))
	if err != nil {
		t.Fatal(err)
	}
	defer finalizeTTSPlaybackTiming(data)
	if data == nil || data.StartedSpeakingAt == 0 {
		t.Fatal("live first audible segment lost its playback timestamp")
	}
}

func TestLatencyRealtimeSayIncludesTTSMetrics(t *testing.T) {
	session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
	stream := newEndInputGenerationTTSStream()
	stream.emitAfterPush = true
	session.TTS = &fakeGenerationTTS{stream: stream}
	ma := NewMultimodalAgent(nil, llm.NewChatContext())
	ma.session = session
	ma.rtSession = &fakeRealtimeSession{}
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hello"}}}
	speech.Generation.UserMessage = &llm.ChatMessage{Metrics: map[string]any{"stopped_speaking_at": timeToUnixSeconds(time.Now().Add(-time.Second))}}
	ma.OnSpeechScheduled(context.Background(), speech)
	metrics := speech.Generation.AssistantMessage.Metrics
	if _, ok := metrics["tts_node_ttfb"]; !ok {
		t.Fatal("realtime Say lost its measured TTS latency")
	}
	if _, ok := metrics["e2e_latency"]; !ok {
		t.Fatal("realtime Say lost its response latency")
	}
}

func TestLatencySTTAnchorsSurviveVADUpdates(t *testing.T) {
	a := NewAgent("timing")
	a.STT = &fakePipelineSTT{}
	a.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(a, nil, AgentSessionOptions{MinEndpointingDelay: 60, MaxEndpointingDelay: 60})
	activity := NewAgentActivity(a, session)
	a.activity = activity
	session.activity = activity
	defer activity.Stop()
	activity.userSpeechStartedAt = time.Now().Add(-time.Second)
	end := timeToUnixSeconds(time.Now().Add(-400 * time.Millisecond))
	activity.OnFinalTranscript(&stt.SpeechEvent{SpeechEndTime: &end, Alternatives: []stt.SpeechData{{Text: "hello", Confidence: 0.9}}})
	activity.OnVADInferenceDone(&vad.VADEvent{RawAccumulatedSpeech: 0.3})
	activity.OnEndOfSpeech(&vad.VADEvent{SilenceDuration: 0.1})
	info := activity.pendingFinalEndOfTurnInfo()
	if info.StoppedSpeakingAt == nil || *info.StoppedSpeakingAt != end {
		t.Fatal("VAD overwrote the STT-owned speech-end anchor")
	}
}

func TestLatencySTTFinalCorrectsDelayedStartReceipt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := NewAgent("timing")
		a.STT = &fakePipelineSTT{}
		a.TurnDetection = TurnDetectionModeSTT
		session := NewAgentSession(a, nil, AgentSessionOptions{MinEndpointingDelay: 60, MaxEndpointingDelay: 60})
		activity := NewAgentActivity(a, session)
		a.activity = activity
		session.activity = activity
		defer activity.Stop()
		activity.OnSTTStartOfSpeech(&stt.SpeechEvent{})
		start := timeToUnixSeconds(time.Now().Add(-time.Second))
		end := timeToUnixSeconds(time.Now().Add(-400 * time.Millisecond))
		activity.OnFinalTranscript(&stt.SpeechEvent{SpeechStartTime: &start, SpeechEndTime: &end, Alternatives: []stt.SpeechData{{Text: "hello", Confidence: 0.9}}})
		info := activity.pendingFinalEndOfTurnInfo()
		if info.StoppedSpeakingAt == nil || *info.StoppedSpeakingAt != end || !info.TranscriptionDelaySet || info.TranscriptionDelay != 0.4 {
			t.Fatalf("delayed STT start discarded word timing: %+v", info)
		}
	})
}
