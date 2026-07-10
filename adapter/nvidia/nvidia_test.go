package nvidia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
	"github.com/hraban/opus"
)

func concatNvidiaRealtimeOutboundAudioData(frames []*model.AudioFrame) []byte {
	var data []byte
	for _, frame := range frames {
		if frame != nil {
			data = append(data, frame.Data...)
		}
	}
	return data
}

func newNvidiaRealtimeModelWithoutPreconnect(opts ...NvidiaRealtimeOption) *NvidiaRealtimeModel {
	model := NewNvidiaRealtimeModel(opts...)
	model.preconnect = false
	return model
}

func expectNvidiaRealtimeAcquireMetrics(t *testing.T, events <-chan llm.RealtimeEvent) {
	t.Helper()
	select {
	case ev := <-events:
		if ev.Type != llm.RealtimeEventTypeMetricsCollected || ev.Metrics == nil {
			t.Fatalf("event = %+v, want connection-acquire metrics", ev)
		}
		if ev.Metrics.RequestID != "" || ev.Metrics.AcquireTime <= 0 || ev.Metrics.ConnectionReused {
			t.Fatalf("metrics = %+v, want acquire metric with empty request id, positive acquire time, reused=false", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connection-acquire metrics")
	}
}

func expectNvidiaRealtimeReconnectSkippingAcquireMetrics(t *testing.T, events <-chan llm.RealtimeEvent) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type == llm.RealtimeEventTypeMetricsCollected && ev.Metrics != nil && ev.Metrics.RequestID == "" {
				continue
			}
			if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
				t.Fatalf("event = %+v, want session_reconnected", ev)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for session_reconnected")
		}
	}
}

func TestNvidiaPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.nvidia" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.nvidia", PluginTitle)
	}
	if PluginVersion == "" {
		t.Fatalf("PluginVersion = %q, want non-empty project release version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.nvidia" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.nvidia", PluginPackage)
	}
}

func TestNvidiaRealtimeDefaultsMatchReference(t *testing.T) {
	t.Setenv("PERSONAPLEX_URL", "")

	model := NewNvidiaRealtimeModel()

	if got, want := model.Model(), "personaplex-7b"; got != want {
		t.Fatalf("Model() = %q, want %q", got, want)
	}
	if got, want := model.Provider(), "nvidia"; got != want {
		t.Fatalf("Provider() = %q, want %q", got, want)
	}
	if got, want := model.Label(), "personaplex-NATF2"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := model.baseURL, "localhost:8998"; got != want {
		t.Fatalf("baseURL = %q, want %q", got, want)
	}
	if got, want := model.voice, "NATF2"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := model.textPrompt, "You are a helpful assistant."; got != want {
		t.Fatalf("textPrompt = %q, want %q", got, want)
	}
	if model.seed != nil {
		t.Fatalf("seed = %v, want nil", *model.seed)
	}
	if got, want := model.silenceThresholdMS, 500; got != want {
		t.Fatalf("silenceThresholdMS = %d, want %d", got, want)
	}
	if !model.preconnect {
		t.Fatal("preconnect = false, want default session connection started like reference")
	}
	if model.useSSL {
		t.Fatal("useSSL = true, want false for reference localhost default")
	}
	if got, want := model.InputSampleRate(), 24000; got != want {
		t.Fatalf("InputSampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := model.OutputSampleRate(), 24000; got != want {
		t.Fatalf("OutputSampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := model.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want mono", got)
	}
	caps := model.Capabilities()
	if caps.MessageTruncation || caps.TurnDetection || caps.UserTranscription || caps.AutoToolReplyGeneration || !caps.AudioOutput || caps.ManualFunctionCalls || caps.PerResponseToolChoice {
		t.Fatalf("Capabilities() = %+v, want PersonaPlex reference audio-output-only realtime flags", caps)
	}
	var realtime llm.RealtimeModel = model
	if err := realtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNvidiaRealtimeOptionsMatchReference(t *testing.T) {
	seed := 42
	model := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL("wss://personaplex.example:9443"),
		WithNvidiaRealtimeVoice("VARF1"),
		WithNvidiaRealtimeTextPrompt("Speak tersely."),
		WithNvidiaRealtimeSeed(seed),
		WithNvidiaRealtimeSilenceThresholdMS(750),
	)

	if got, want := model.baseURL, "personaplex.example:9443"; got != want {
		t.Fatalf("baseURL = %q, want stripped host %q", got, want)
	}
	if !model.useSSL {
		t.Fatal("useSSL = false, want true for wss URL")
	}
	if got, want := model.voice, "VARF1"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := model.textPrompt, "Speak tersely."; got != want {
		t.Fatalf("textPrompt = %q, want %q", got, want)
	}
	if model.seed == nil || *model.seed != seed {
		t.Fatalf("seed = %v, want %d", model.seed, seed)
	}
	if got, want := model.silenceThresholdMS, 750; got != want {
		t.Fatalf("silenceThresholdMS = %d, want %d", got, want)
	}
	if session, err := model.Session(); err != nil || session == nil {
		t.Fatalf("Session() = (%v, %v), want constructed realtime session", session, err)
	}
}

func TestNvidiaRealtimeSessionLifecycleMatchesReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL("https://personaplex.example:9443"),
		WithNvidiaRealtimeVoice("VARF1"),
		WithNvidiaRealtimeTextPrompt("old prompt"),
		WithNvidiaRealtimeSeed(7),
		WithNvidiaRealtimeSilenceThresholdMS(250),
	)
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions() error = %v", err)
	}
	if got, want := realtimeModel.textPrompt, "old prompt"; got != want {
		t.Fatalf("model textPrompt = %q, want unchanged reference prompt %q", got, want)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}
	if got, want := concrete.textPrompt, "new prompt"; got != want {
		t.Fatalf("session textPrompt = %q, want %q", got, want)
	}
	if ev := <-session.EventCh(); ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
		t.Fatalf("instruction update event = %+v, want session_reconnected", ev)
	}
	if got, want := concrete.voice, "VARF1"; got != want {
		t.Fatalf("session voice = %q, want reference snapshot %q", got, want)
	}
	if got, want := concrete.silenceThresholdMS, 250; got != want {
		t.Fatalf("session silenceThresholdMS = %d, want reference snapshot %d", got, want)
	}
	if concrete.seed == nil || *concrete.seed != 7 {
		t.Fatalf("session seed = %v, want reference snapshot 7", concrete.seed)
	}
	if got, want := concrete.websocketURL(), "wss://personaplex.example:9443/api/chat?voice_prompt=VARF1.pt&text_prompt=new%20prompt&seed=7"; got != want {
		t.Fatalf("session websocketURL() = %q, want %q", got, want)
	}
	if got, want := realtimeModel.websocketURL(), "wss://personaplex.example:9443/api/chat?voice_prompt=VARF1.pt&text_prompt=old%20prompt&seed=7"; got != want {
		t.Fatalf("model websocketURL() after session update = %q, want unchanged reference URL %q", got, want)
	}
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{ID: "first", Role: llm.ChatRoleUser, Text: "hello"})
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext() error = %v", err)
	}
	chatCtx.AddMessage(llm.ChatMessageArgs{ID: "second", Role: llm.ChatRoleUser, Text: "late"})
	if concrete.chatCtx == chatCtx {
		t.Fatal("session chatCtx aliases source, want reference copy")
	}
	if got, want := len(concrete.chatCtx.Items), 1; got != want {
		t.Fatalf("session chatCtx item count = %d, want copied snapshot count %d", got, want)
	}
	if err := session.PushAudio(&model.AudioFrame{SampleRate: 24000, NumChannels: 1}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	if got, want := len(concrete.outboundAudio), 0; got != want {
		t.Fatalf("outboundAudio after empty PushAudio = %d, want %d", got, want)
	}
	frame := &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio(non-empty) error = %v", err)
	}
	frame.Data[0] = 9
	if got, want := len(concrete.outboundAudio), 1; got != want {
		t.Fatalf("outboundAudio count = %d, want copied frame count %d", got, want)
	}
	if got, want := concrete.outboundAudio[0].Data[0], byte(1); got != want {
		t.Fatalf("outboundAudio copied data[0] = %d, want immutable copy %d", got, want)
	}
	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio() error = %v", err)
	}
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio() error = %v", err)
	}
	if err := session.Truncate(llm.RealtimeTruncateOptions{MessageID: "msg", Modalities: []string{"audio"}, AudioEndMillis: 12}); err != nil {
		t.Fatalf("Truncate() error = %v", err)
	}
	generateReplyErr := "generate_reply is not yet supported by the PersonaPlex realtime model."
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err == nil || err.Error() != generateReplyErr {
		t.Fatalf("GenerateReply() error = %v, want reference unsupported generation error", err)
	}
	if err := session.Say("hello"); err == nil || err.Error() != "RealtimeSession does not implement say(). use a TTS model instead" {
		t.Fatalf("Say() error = %v, want reference unsupported say error", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, ok := <-session.EventCh(); ok {
		t.Fatal("EventCh() open after Close, want closed")
	}
	if err := session.PushAudio(&model.AudioFrame{Data: []byte{3, 4}, SampleRate: 24000, NumChannels: 1}); err != nil {
		t.Fatalf("PushAudio() after Close error = %v, want nil ignored input", err)
	}
	if got, want := len(concrete.outboundAudio), 1; got != want {
		t.Fatalf("outboundAudio after Close = %d, want unchanged count %d", got, want)
	}
}

func TestNvidiaRealtimePushAudioNormalizesReferenceInput(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	stereo := make([]int16, 0, 960*2)
	for i := 0; i < 960; i++ {
		stereo = append(stereo, int16(i), int16(-i))
	}
	frame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(stereo),
		SampleRate:        16000,
		NumChannels:       2,
		SamplesPerChannel: 960,
		ParticipantID:     "caller-1",
	}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	frame.Data[0] = 99

	if got, want := len(concrete.outboundAudio), 1; got != want {
		t.Fatalf("outboundAudio count = %d, want %d", got, want)
	}
	got := concrete.outboundAudio[0]
	if got.SampleRate != 24000 || got.NumChannels != 1 {
		t.Fatalf("outbound audio format = %d Hz/%d ch, want 24000 Hz/1 ch", got.SampleRate, got.NumChannels)
	}
	if got.SamplesPerChannel == 0 {
		t.Fatal("outbound SamplesPerChannel = 0, want resampled output after reference buffering threshold")
	}
	if got.ParticipantID != "caller-1" {
		t.Fatalf("outbound ParticipantID = %q, want caller-1", got.ParticipantID)
	}
	if len(got.Data) != int(got.SamplesPerChannel)*2 {
		t.Fatalf("outbound data len = %d, want samples_per_channel*2", len(got.Data))
	}
	if got.Data[0] == 99 {
		t.Fatal("outbound audio aliases source frame, want immutable copy")
	}
}

func TestNvidiaRealtimePushAudioPreservesResampleRemainderLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	samples := make([]int16, 960)
	for i := range samples {
		samples[i] = int16(100 + i)
	}
	for i := 0; i < 3; i++ {
		chunk := samples[i*320 : (i+1)*320]
		frame := &model.AudioFrame{
			Data:              int16SliceToLittleEndianBytes(chunk),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: uint32(len(chunk)),
		}
		if err := session.PushAudio(frame); err != nil {
			t.Fatalf("PushAudio(%d) error = %v", i, err)
		}
		if i < 2 && len(concrete.outboundAudio) != 0 {
			t.Fatalf("outboundAudio after partial chunk %d = %d, want buffered input until reference resampler emits", i, len(concrete.outboundAudio))
		}
	}

	var total uint32
	for _, frame := range concrete.outboundAudio {
		total += frame.SamplesPerChannel
	}
	if total == 0 {
		t.Fatal("total resampled samples = 0, want output after accumulated 16 kHz input reaches reference buffering threshold")
	}
}

func TestNvidiaRealtimePushAudioPreservesResamplePhaseLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	wholeSession, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("whole Session() error = %v", err)
	}
	whole, ok := wholeSession.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("whole session type = %T, want *nvidiaRealtimeSession", wholeSession)
	}
	splitSession, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("split Session() error = %v", err)
	}
	split, ok := splitSession.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("split session type = %T, want *nvidiaRealtimeSession", splitSession)
	}

	samples := make([]int16, 960)
	for i := range samples {
		samples[i] = int16((i%64 - 32) * 32)
	}
	if err := wholeSession.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(samples),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(samples)),
	}); err != nil {
		t.Fatalf("whole PushAudio() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		chunk := samples[i*480 : (i+1)*480]
		if err := splitSession.PushAudio(&model.AudioFrame{
			Data:              int16SliceToLittleEndianBytes(chunk),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: uint32(len(chunk)),
		}); err != nil {
			t.Fatalf("split PushAudio(%d) error = %v", i, err)
		}
	}

	if len(whole.outboundAudio) == 0 || len(split.outboundAudio) == 0 {
		t.Fatalf("resampled output missing: whole=%d split=%d", len(whole.outboundAudio), len(split.outboundAudio))
	}
	if got, want := concatNvidiaRealtimeOutboundAudioData(split.outboundAudio), concatNvidiaRealtimeOutboundAudioData(whole.outboundAudio); !bytes.Equal(got, want) {
		t.Fatalf("split resampled PCM = %v, want whole-frame PCM %v from stateful resampler phase", littleEndianBytesToInt16Slice(got), littleEndianBytesToInt16Slice(want))
	}
}

func TestNvidiaRealtimeInputResamplerInterpolatesBetweenSamplesLikeReference(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{0, 1000}),
		SampleRate:        12000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	resampled, err := resampleNvidiaRealtimeInputFrame(frame, 24000, 4, 0, 0, nil)
	if err != nil {
		t.Fatalf("resampleNvidiaRealtimeInputFrame() error = %v", err)
	}
	got := littleEndianBytesToInt16Slice(resampled.Data)
	if len(got) != 4 {
		t.Fatalf("resampled samples = %v, want 4 samples", got)
	}
	if got[1] <= got[0] || got[1] >= got[2] {
		t.Fatalf("resampled samples = %v, want interpolated sample between source endpoints", got)
	}
}

func TestNvidiaRealtimeInputResamplerInterpolatesAcrossFrameBoundaryLikeReference(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{1000, 2000}),
		SampleRate:        44100,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	previous := int16SliceToLittleEndianBytes([]int16{0})

	resampled, err := resampleNvidiaRealtimeInputFrame(frame, 24000, 1, 10, 5, previous)
	if err != nil {
		t.Fatalf("resampleNvidiaRealtimeInputFrame() error = %v", err)
	}
	got := littleEndianBytesToInt16Slice(resampled.Data)
	if len(got) != 1 {
		t.Fatalf("resampled samples = %v, want 1 sample", got)
	}
	if got[0] <= 0 || got[0] >= 1000 {
		t.Fatalf("resampled boundary sample = %v, want interpolation between previous and current frame", got)
	}
}

func TestNvidiaRealtimePushAudioBuffersSmallResampledFramesLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sampleRate uint32
		samples    int
	}{
		{name: "sixteen-kilohertz", sampleRate: 16000, samples: 160},
		{name: "forty-eight-kilohertz", sampleRate: 48000, samples: 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			realtimeModel := NewNvidiaRealtimeModel()
			session, err := realtimeModel.Session()
			if err != nil {
				t.Fatalf("Session() error = %v", err)
			}
			concrete, ok := session.(*nvidiaRealtimeSession)
			if !ok {
				t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
			}

			samples := make([]int16, tc.samples)
			for i := range samples {
				samples[i] = int16(i)
			}
			if err := session.PushAudio(&model.AudioFrame{
				Data:              int16SliceToLittleEndianBytes(samples),
				SampleRate:        tc.sampleRate,
				NumChannels:       1,
				SamplesPerChannel: uint32(len(samples)),
			}); err != nil {
				t.Fatalf("PushAudio(small resampled frame) error = %v", err)
			}
			if len(concrete.outboundAudio) != 0 || len(concrete.outboundMessages) != 0 || len(concrete.inputAudioBuffer) != 0 {
				t.Fatalf("small resampled frame queued audio=%d messages=%d buffered=%d, want pending resampler input only", len(concrete.outboundAudio), len(concrete.outboundMessages), len(concrete.inputAudioBuffer))
			}
		})
	}
}

func TestNvidiaRealtimePushAudioSkipsMalformedFramesLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3},
		SampleRate:        24000,
		NumChannels:       2,
		SamplesPerChannel: 1,
	}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio(malformed) error = %v, want nil skipped frame", err)
	}
	if len(concrete.outboundAudio) != 0 || len(concrete.outboundMessages) != 0 || len(concrete.inputAudioBuffer) != 0 {
		t.Fatalf("malformed frame queued audio=%d messages=%d buffered=%d, want all empty", len(concrete.outboundAudio), len(concrete.outboundMessages), len(concrete.inputAudioBuffer))
	}
}

func TestNvidiaRealtimePushAudioSkipsZeroRateFramesLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	frame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{7}),
		SampleRate:        0,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio(zero-rate) error = %v, want nil skipped frame", err)
	}
	if len(concrete.outboundAudio) != 0 || len(concrete.outboundMessages) != 0 || len(concrete.inputAudioBuffer) != 0 {
		t.Fatalf("zero-rate frame queued audio=%d messages=%d buffered=%d, want all empty", len(concrete.outboundAudio), len(concrete.outboundMessages), len(concrete.inputAudioBuffer))
	}
}

func TestNvidiaRealtimePushAudioSkipsOddBytePCMFramesLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	frame := &model.AudioFrame{
		Data:              []byte{7},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio(odd-byte PCM) error = %v, want nil skipped frame", err)
	}
	if len(concrete.outboundAudio) != 0 || len(concrete.outboundMessages) != 0 || len(concrete.inputAudioBuffer) != 0 {
		t.Fatalf("odd-byte frame queued audio=%d messages=%d buffered=%d, want all empty", len(concrete.outboundAudio), len(concrete.outboundMessages), len(concrete.inputAudioBuffer))
	}
}

func TestNvidiaRealtimePushAudioQueuesReferenceOpusMessage(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	pcm := makeNvidiaRealtimePCMInputFrame()
	frame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	if got, want := len(concrete.outboundMessages), 1; got != want {
		t.Fatalf("outboundMessages count = %d, want %d", got, want)
	}
	message := concrete.outboundMessages[0]
	if len(message) < 2 {
		t.Fatalf("outbound message len = %d, want audio type + opus payload", len(message))
	}
	if message[0] != nvidiaRealtimeMsgAudio {
		t.Fatalf("outbound message type = 0x%02x, want audio 0x%02x", message[0], nvidiaRealtimeMsgAudio)
	}
	decoder, err := opus.NewDecoder(defaultNvidiaRealtimeSampleRate, defaultNvidiaRealtimeNumChannels)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}
	decoded := make([]int16, 5760)
	n, err := decoder.Decode(message[1:], decoded)
	if err != nil {
		t.Fatalf("Decode(outbound opus) error = %v", err)
	}
	if n == 0 {
		t.Fatal("Decode(outbound opus) samples = 0, want speech packet")
	}
}

func TestNvidiaRealtimePushAudioSendsAfterHandshakeLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan struct{}, 1)
	received := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		connected <- struct{}{}

		readDone := make(chan struct{})
		go func() {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				serverErr <- err
				close(readDone)
				return
			}
			if msgType != websocket.BinaryMessage {
				serverErr <- errors.New("received non-binary websocket message")
				close(readDone)
				return
			}
			received <- payload
			close(readDone)
		}()
		select {
		case <-readDone:
			serverErr <- errors.New("received audio before handshake")
			return
		case <-time.After(75 * time.Millisecond):
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		<-readDone
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}

	select {
	case <-connected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before connect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PersonaPlex websocket connection")
	}

	select {
	case payload := <-received:
		if len(payload) <= 1 || payload[0] != nvidiaRealtimeMsgAudio {
			t.Fatalf("websocket payload = %x, want audio message with 0x01 prefix", payload)
		}
	case err := <-serverErr:
		t.Fatalf("websocket server error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio after handshake")
	}
}

func TestNvidiaRealtimeSessionPreconnectsConfiguredProviderLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		connected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	select {
	case <-connected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before preconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for configured PersonaPlex session preconnect")
	}
}

func TestNvidiaRealtimePreconnectEmitsAcquireMetricsLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		connected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	select {
	case <-connected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before preconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for configured PersonaPlex session preconnect")
	}

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeMetricsCollected || ev.Metrics == nil {
			t.Fatalf("event after preconnect = %+v, want metrics_collected", ev)
		}
		if ev.Metrics.RequestID != "" {
			t.Fatalf("metrics request id = %q, want empty connection-acquire metric", ev.Metrics.RequestID)
		}
		if ev.Metrics.AcquireTime <= 0 {
			t.Fatalf("metrics acquire_time = %v, want positive duration", ev.Metrics.AcquireTime)
		}
		if ev.Metrics.ConnectionReused {
			t.Fatal("metrics connection_reused = true, want false for new websocket")
		}
		if ev.Metrics.Metadata == nil || ev.Metrics.Metadata.ModelName != "personaplex-7b" || ev.Metrics.Metadata.ModelProvider != "nvidia" {
			t.Fatalf("metrics metadata = %+v, want personaplex-7b/nvidia", ev.Metrics.Metadata)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for connection-acquire metrics")
	}
}

func TestNvidiaRealtimeDialFailureEmitsRecoverableErrorLikeReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeError || ev.Error == nil {
			t.Fatalf("event = %+v, want realtime error event", ev)
		}
		var modelErr *llm.RealtimeModelError
		if !errors.As(ev.Error, &modelErr) {
			t.Fatalf("error = %T %v, want RealtimeModelError", ev.Error, ev.Error)
		}
		if !modelErr.Recoverable {
			t.Fatalf("Recoverable = false, want true")
		}
		if !strings.Contains(modelErr.Error(), "Connection failed:") {
			t.Fatalf("error = %v, want Connection failed wrapper", modelErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dial failure error event")
	}
}

func TestNvidiaRealtimeDialFailureRetriesLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var attempts atomic.Int32
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeError || ev.Error == nil {
			t.Fatalf("event = %+v, want initial dial error event", ev)
		}
	case err := <-serverErr:
		t.Fatalf("websocket server error before dial failure event: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial dial failure event")
	}
	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before reconnect: %v", err)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timed out waiting for retry reconnect after recoverable dial failure")
	}
}

func TestNvidiaRealtimeProviderWriteFailureEmitsRecoverableErrorLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	accepted := make(chan struct{}, 1)
	release := make(chan struct{})
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		accepted <- struct{}{}
		<-release
		_ = conn.Close()
	}))
	defer server.Close()
	defer close(release)

	clientConn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	select {
	case <-accepted:
	case err := <-serverErr:
		t.Fatalf("websocket server error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket accept")
	}
	if err := clientConn.Close(); err != nil {
		t.Fatalf("client Close() error = %v", err)
	}

	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()
	concrete := session.(*nvidiaRealtimeSession)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	concrete.mu.Lock()
	concrete.transportStarted = true
	concrete.transportCtx = ctx
	concrete.transportCancel = cancel
	concrete.outboundMessages = [][]byte{{nvidiaRealtimeMsgAudio, 1, 2, 3}}
	concrete.mu.Unlock()

	done := make(chan struct{})
	go func() {
		concrete.sendRealtimeTransport(ctx, clientConn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendRealtimeTransport blocked on closed websocket")
	}
	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeError || ev.Error == nil {
			t.Fatalf("event = %+v, want realtime error event", ev)
		}
		var modelErr *llm.RealtimeModelError
		if !errors.As(ev.Error, &modelErr) {
			t.Fatalf("error = %T %v, want RealtimeModelError", ev.Error, ev.Error)
		}
		if !modelErr.Recoverable {
			t.Fatalf("Recoverable = false, want true")
		}
		if !strings.Contains(modelErr.Error(), "Connection failed:") {
			t.Fatalf("error = %v, want Connection failed wrapper", modelErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for write failure error event")
	}
}

func TestNvidiaRealtimeProviderWriteFailureRetriesLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()
	concrete := session.(*nvidiaRealtimeSession)
	concrete.baseURL, concrete.useSSL = normalizeNvidiaRealtimeBaseURL(server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	concrete.mu.Lock()
	concrete.transportStarted = true
	concrete.transportCtx = ctx
	concrete.transportCancel = cancel
	concrete.outboundMessages = [][]byte{{nvidiaRealtimeMsgAudio, 1, 2, 3}}
	concrete.mu.Unlock()

	closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		_ = conn.Close()
	}))
	defer closedServer.Close()
	clientConn, _, err := websocket.DefaultDialer.Dial(strings.Replace(closedServer.URL, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatalf("Dial(closed server) error = %v", err)
	}
	_ = clientConn.Close()

	done := make(chan struct{})
	go func() {
		concrete.sendRealtimeTransport(ctx, clientConn)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendRealtimeTransport blocked on closed websocket")
	}
	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeError || ev.Error == nil {
			t.Fatalf("event = %+v, want realtime error event", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for write failure error event")
	}
	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before write-failure retry: %v", err)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timed out waiting for retry reconnect after provider write failure")
	}
}

func TestNvidiaRealtimeHandshakeAbnormalCloseEmitsRecoverableErrorLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "boom"), time.Now().Add(time.Second)); err != nil {
			serverErr <- err
			return
		}
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	expectNvidiaRealtimeAcquireMetrics(t, session.EventCh())

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeError || ev.Error == nil {
			t.Fatalf("event = %+v, want realtime error event", ev)
		}
		var modelErr *llm.RealtimeModelError
		if !errors.As(ev.Error, &modelErr) {
			t.Fatalf("error = %T %v, want RealtimeModelError", ev.Error, ev.Error)
		}
		if !modelErr.Recoverable {
			t.Fatalf("Recoverable = false, want true")
		}
		if !strings.Contains(modelErr.Error(), "PersonaPlex connection closed unexpectedly") {
			t.Fatalf("error = %v, want PersonaPlex connection closed unexpectedly", modelErr)
		}
	case err := <-serverErr:
		t.Fatalf("websocket server error before realtime error event: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pre-handshake close error event")
	}
}

func TestNvidiaRealtimeHandshakeNormalCloseClearsPendingAudioLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if attempt == 1 {
			if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
				serverErr <- err
				return
			}
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()
	concrete := session.(*nvidiaRealtimeSession)

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}

	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before normal-close cleanup: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pre-handshake normal close cleanup")
	}

	concrete.mu.Lock()
	pendingMessages := len(concrete.outboundMessages)
	pendingAudio := len(concrete.inputAudioBuffer)
	encoder := concrete.opusEncoder
	concrete.mu.Unlock()
	if pendingMessages != 0 {
		t.Fatalf("outboundMessages after pre-handshake normal close = %d, want cleared", pendingMessages)
	}
	if pendingAudio != 0 {
		t.Fatalf("inputAudioBuffer after pre-handshake normal close = %d, want cleared", pendingAudio)
	}
	if encoder != nil {
		t.Fatal("opusEncoder after pre-handshake normal close != nil, want reset")
	}
}

func TestNvidiaRealtimeHandshakeNormalCloseReconnectsLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if attempt == 1 {
			if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
				serverErr <- err
				return
			}
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}

	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before reconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect after pre-handshake normal close")
	}
}

func TestNvidiaRealtimeProviderNormalCloseFinalizesGenerationLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			serverErr <- err
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgText, 'o', 'k'}); err != nil {
			serverErr <- err
			return
		}
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
			serverErr <- err
			return
		}
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	expectNvidiaRealtimeAcquireMetrics(t, session.EventCh())

	var ev llm.RealtimeEvent
	select {
	case ev = <-session.EventCh():
	case err := <-serverErr:
		t.Fatalf("websocket server error before generation: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generation_created")
	}
	if ev.Type != llm.RealtimeEventTypeGenerationCreated || ev.Generation == nil {
		t.Fatalf("event = %+v, want generation_created", ev)
	}
	msg := <-ev.Generation.MessageCh
	if got := <-msg.TextCh; got != "ok" {
		t.Fatalf("text delta = %q, want provider text", got)
	}

	select {
	case metricsEvent := <-session.EventCh():
		if metricsEvent.Type != llm.RealtimeEventTypeMetricsCollected || metricsEvent.Metrics == nil {
			t.Fatalf("event after provider close = %+v, want metrics_collected", metricsEvent)
		}
		if metricsEvent.Metrics.Cancelled {
			t.Fatalf("metrics.Cancelled = true, want false for normal provider close")
		}
	case err := <-serverErr:
		t.Fatalf("websocket server error before metrics: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal-close metrics")
	}
	if _, ok := <-msg.TextCh; ok {
		t.Fatal("TextCh open after provider normal close, want closed")
	}
}

func TestNvidiaRealtimeProviderNormalCloseReconnectsLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		if attempt == 1 {
			if _, _, err := conn.ReadMessage(); err != nil {
				serverErr <- err
				return
			}
			if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
				serverErr <- err
				return
			}
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}

	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before reconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect after provider normal close")
	}
}

func TestNvidiaRealtimeProviderAbnormalCloseInterruptsGenerationLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			serverErr <- err
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgText, 'b', 'a', 'd'}); err != nil {
			serverErr <- err
			return
		}
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "boom"), time.Now().Add(time.Second)); err != nil {
			serverErr <- err
			return
		}
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	expectNvidiaRealtimeAcquireMetrics(t, session.EventCh())

	var ev llm.RealtimeEvent
	select {
	case ev = <-session.EventCh():
	case err := <-serverErr:
		t.Fatalf("websocket server error before generation: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generation_created")
	}
	if ev.Type != llm.RealtimeEventTypeGenerationCreated || ev.Generation == nil {
		t.Fatalf("event = %+v, want generation_created", ev)
	}
	msg := <-ev.Generation.MessageCh
	if got := <-msg.TextCh; got != "bad" {
		t.Fatalf("text delta = %q, want provider text", got)
	}

	select {
	case metricsEvent := <-session.EventCh():
		if metricsEvent.Type != llm.RealtimeEventTypeMetricsCollected || metricsEvent.Metrics == nil {
			t.Fatalf("event after provider close = %+v, want metrics_collected", metricsEvent)
		}
		if !metricsEvent.Metrics.Cancelled {
			t.Fatalf("metrics.Cancelled = false, want true for abnormal provider close")
		}
	case err := <-serverErr:
		t.Fatalf("websocket server error before metrics: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for abnormal-close metrics")
	}
	select {
	case errorEvent := <-session.EventCh():
		if errorEvent.Type != llm.RealtimeEventTypeError || errorEvent.Error == nil {
			t.Fatalf("event after abnormal close metrics = %+v, want error event", errorEvent)
		}
		if !strings.Contains(errorEvent.Error.Error(), "PersonaPlex connection closed unexpectedly") {
			t.Fatalf("error event = %v, want PersonaPlex connection closed unexpectedly", errorEvent.Error)
		}
	case err := <-serverErr:
		t.Fatalf("websocket server error before error event: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for abnormal-close error event")
	}
	if _, ok := <-msg.TextCh; ok {
		t.Fatal("TextCh open after provider abnormal close, want closed")
	}
	if got := len(concrete.outboundMessages); got != 0 {
		t.Fatalf("outboundMessages after abnormal close = %d, want cleared stale transport audio", got)
	}
	if concrete.opusEncoder != nil {
		t.Fatal("opusEncoder after abnormal close != nil, want fresh encoder on reconnect")
	}
}

func TestNvidiaRealtimeAbnormalCloseRetriesLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		if attempt == 1 {
			if _, _, err := conn.ReadMessage(); err != nil {
				serverErr <- err
				return
			}
			if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "boom"), time.Now().Add(time.Second)); err != nil {
				serverErr <- err
				return
			}
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL(server.URL))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}

	for {
		select {
		case ev := <-session.EventCh():
			if ev.Type == llm.RealtimeEventTypeError {
				goto waitReconnect
			}
		case err := <-serverErr:
			t.Fatalf("websocket server error before abnormal-close error event: %v", err)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for abnormal-close error event")
		}
	}

waitReconnect:
	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before abnormal-close retry: %v", err)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timed out waiting for retry reconnect after abnormal provider close")
	}
}

func TestNvidiaRealtimeSessionGenerationEventsMatchReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	concrete.handleBinaryMessage([]byte{nvidiaRealtimeMsgHandshake})
	concrete.handleBinaryMessage([]byte{nvidiaRealtimeMsgText, 0})
	concrete.handleBinaryMessage([]byte{nvidiaRealtimeMsgText, 3})
	concrete.handleBinaryMessage([]byte{nvidiaRealtimeMsgText, 0xff})
	select {
	case ev := <-session.EventCh():
		t.Fatalf("event after handshake/special/invalid payload = %+v, want none", ev)
	default:
	}

	concrete.handleBinaryMessage([]byte{nvidiaRealtimeMsgText, 'h', 'e', 'l'})

	ev := <-session.EventCh()
	if ev.Type != llm.RealtimeEventTypeGenerationCreated || ev.Generation == nil {
		t.Fatalf("event = %+v, want generation_created", ev)
	}
	msg := <-ev.Generation.MessageCh
	if msg.MessageID != ev.Generation.ResponseID {
		t.Fatalf("MessageID = %q, want response id %q", msg.MessageID, ev.Generation.ResponseID)
	}
	modalities := <-msg.ModalitiesCh
	if len(modalities) != 2 || modalities[0] != "audio" || modalities[1] != "text" {
		t.Fatalf("modalities = %v, want [audio text]", modalities)
	}
	if got, want := <-msg.TextCh, "hel"; got != want {
		t.Fatalf("text delta = %q, want %q", got, want)
	}

	frame := &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	concrete.handleAudioFrame(frame)
	if got := <-msg.AudioCh; got != frame {
		t.Fatalf("audio frame = %p, want original frame %p", got, frame)
	}
	concrete.handleTextToken("lo")
	if got, want := <-msg.TextCh, "lo"; got != want {
		t.Fatalf("second text delta = %q, want %q", got, want)
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	metricsEvent := <-session.EventCh()
	if metricsEvent.Type != llm.RealtimeEventTypeMetricsCollected || metricsEvent.Metrics == nil {
		t.Fatalf("metrics event = %+v, want metrics_collected", metricsEvent)
	}
	if metricsEvent.Metrics.RequestID != ev.Generation.ResponseID || !metricsEvent.Metrics.Cancelled {
		t.Fatalf("metrics = %+v, want response id %q and cancelled=true", metricsEvent.Metrics, ev.Generation.ResponseID)
	}
	if metricsEvent.Metrics.Metadata == nil || metricsEvent.Metrics.Metadata.ModelName != "personaplex-7b" || metricsEvent.Metrics.Metadata.ModelProvider != "nvidia" {
		t.Fatalf("metrics metadata = %+v, want personaplex-7b/nvidia", metricsEvent.Metrics.Metadata)
	}
	if _, ok := <-msg.TextCh; ok {
		t.Fatal("TextCh open after interrupt, want closed")
	}
	if _, ok := <-msg.AudioCh; ok {
		t.Fatal("AudioCh open after interrupt, want closed")
	}
	if got, want := len(concrete.chatCtx.Items), 1; got != want {
		t.Fatalf("chatCtx item count = %d, want assistant output appended", got)
	}
	if got, want := concrete.chatCtx.Items[0].GetID(), ev.Generation.ResponseID; got != want {
		t.Fatalf("assistant item id = %q, want response id %q", got, want)
	}
}

func TestNvidiaRealtimeTextOnlyMetricsUseReferenceTTFT(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	concrete.handleTextToken("hello")
	ev := <-session.EventCh()
	if ev.Type != llm.RealtimeEventTypeGenerationCreated || ev.Generation == nil {
		t.Fatalf("event = %+v, want generation_created", ev)
	}
	msg := <-ev.Generation.MessageCh
	if got, want := <-msg.TextCh, "hello"; got != want {
		t.Fatalf("text delta = %q, want %q", got, want)
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	metricsEvent := <-session.EventCh()
	if metricsEvent.Type != llm.RealtimeEventTypeMetricsCollected || metricsEvent.Metrics == nil {
		t.Fatalf("metrics event = %+v, want metrics_collected", metricsEvent)
	}
	if got, want := metricsEvent.Metrics.TTFT, -1.0; got != want {
		t.Fatalf("text-only TTFT = %v, want reference %v until audio frame arrives", got, want)
	}
}

func TestNvidiaRealtimeFinalizeClearsCurrentGenerationLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	concrete.handleTextToken("hello")
	ev := <-session.EventCh()
	if ev.Type != llm.RealtimeEventTypeGenerationCreated || ev.Generation == nil {
		t.Fatalf("event = %+v, want generation_created", ev)
	}
	msg := <-ev.Generation.MessageCh
	if got, want := <-msg.TextCh, "hello"; got != want {
		t.Fatalf("text delta = %q, want %q", got, want)
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	<-session.EventCh()
	if concrete.currentGeneration != nil {
		t.Fatalf("currentGeneration after finalize = %+v, want nil like reference", concrete.currentGeneration)
	}
}

func TestNvidiaRealtimeTextDeltasDoNotBlockBeforeConsumerLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 64; i++ {
			concrete.handleTextToken("x")
		}
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleTextToken blocked with unread deltas, want reference unbounded stream behavior")
	}
}

func TestNvidiaRealtimeTextDeltasDoNotBlockPastBufferLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	const extra = 128
	total := nvidiaRealtimeGenerationStreamBuffer + extra
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			concrete.handleTextToken("x")
		}
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		ev := <-session.EventCh()
		msg := <-ev.Generation.MessageCh
		for i := 0; i < total; i++ {
			<-msg.TextCh
		}
		<-done
		t.Fatal("handleTextToken blocked after fixed stream buffer filled, want reference unbounded stream behavior")
	}
}

func TestNvidiaRealtimeAudioFramesDoNotBlockPastBufferLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	pcm := makeNvidiaRealtimePCMFrame()
	frame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        defaultNvidiaRealtimeSampleRate,
		NumChannels:       defaultNvidiaRealtimeNumChannels,
		SamplesPerChannel: uint32(len(pcm) / defaultNvidiaRealtimeNumChannels),
	}
	const extra = 128
	total := nvidiaRealtimeGenerationStreamBuffer + extra
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			concrete.handleAudioFrame(frame)
		}
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		ev := <-session.EventCh()
		msg := <-ev.Generation.MessageCh
		for i := 0; i < total; i++ {
			<-msg.AudioCh
		}
		<-done
		t.Fatal("handleAudioFrame blocked after fixed stream buffer filled, want reference unbounded stream behavior")
	}
}

func TestNvidiaRealtimeTurnEventsDoNotBlockBeforeConsumerLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			concrete.handleTextToken("x")
			_ = session.Interrupt()
		}
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("generation event emission blocked before consumer drained events, want reference nonblocking emit behavior")
	}
}

func TestNvidiaRealtimeTurnEventsDoNotBlockPastBufferLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	turns := nvidiaRealtimeEventBuffer/2 + 128
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < turns; i++ {
			concrete.handleTextToken("x")
			_ = session.Interrupt()
		}
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		for i := 0; i < turns*2; i++ {
			<-session.EventCh()
		}
		<-done
		t.Fatal("generation event emission blocked after fixed event buffer filled, want reference callback-style event behavior")
	}
}

func TestNvidiaRealtimeSessionBinaryAudioDecodesReferenceOpus(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeSilenceThresholdMS(5))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	packet := encodeNvidiaRealtimeOpusPacket(t, makeNvidiaRealtimePCMFrame())
	message := append([]byte{nvidiaRealtimeMsgAudio}, packet...)
	concrete.handleBinaryMessage(message)

	ev := <-session.EventCh()
	if ev.Type != llm.RealtimeEventTypeGenerationCreated || ev.Generation == nil {
		t.Fatalf("event = %+v, want generation_created", ev)
	}
	msg := <-ev.Generation.MessageCh
	frame := <-msg.AudioCh
	if frame == nil {
		t.Fatal("audio frame = nil, want decoded PCM frame")
	}
	if frame.SampleRate != 24000 || frame.NumChannels != 1 {
		t.Fatalf("audio format = %d Hz/%d ch, want 24000 Hz/1 ch", frame.SampleRate, frame.NumChannels)
	}
	if frame.SamplesPerChannel == 0 || len(frame.Data) == 0 {
		t.Fatalf("audio payload = %d samples/%d bytes, want decoded PCM", frame.SamplesPerChannel, len(frame.Data))
	}
	if len(frame.Data) != int(frame.SamplesPerChannel)*2 {
		t.Fatalf("audio bytes = %d, want samples_per_channel*2 (%d)", len(frame.Data), frame.SamplesPerChannel*2)
	}

	var metricsEvent llm.RealtimeEvent
	select {
	case metricsEvent = <-session.EventCh():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for silence metrics event")
	}
	if metricsEvent.Type != llm.RealtimeEventTypeMetricsCollected || metricsEvent.Metrics == nil {
		t.Fatalf("metrics event = %+v, want metrics_collected", metricsEvent)
	}
	if metricsEvent.Metrics.RequestID != ev.Generation.ResponseID || metricsEvent.Metrics.Cancelled {
		t.Fatalf("metrics = %+v, want response id %q and cancelled=false", metricsEvent.Metrics, ev.Generation.ResponseID)
	}
	if _, ok := <-msg.AudioCh; ok {
		t.Fatal("AudioCh open after silence finalization, want closed")
	}
}

func TestNvidiaRealtimeInstructionUpdateInterruptsGenerationLikeReference(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeTextPrompt("old prompt"))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	concrete.handleTextToken("draft")
	ev := <-session.EventCh()
	msg := <-ev.Generation.MessageCh
	if got, want := <-msg.TextCh, "draft"; got != want {
		t.Fatalf("text delta = %q, want %q", got, want)
	}

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions() error = %v", err)
	}
	if got, want := concrete.textPrompt, "new prompt"; got != want {
		t.Fatalf("session textPrompt = %q, want %q", got, want)
	}
	metricsEvent := <-session.EventCh()
	if metricsEvent.Type != llm.RealtimeEventTypeMetricsCollected || metricsEvent.Metrics == nil {
		t.Fatalf("metrics event = %+v, want metrics_collected", metricsEvent)
	}
	if !metricsEvent.Metrics.Cancelled || metricsEvent.Metrics.RequestID != ev.Generation.ResponseID {
		t.Fatalf("metrics = %+v, want cancelled active generation %q", metricsEvent.Metrics, ev.Generation.ResponseID)
	}
	if _, ok := <-msg.TextCh; ok {
		t.Fatal("TextCh open after instruction update, want closed")
	}
}

func TestNvidiaRealtimeInstructionUpdateEmitsReconnectLikeReference(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeTextPrompt("old prompt"))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions() error = %v", err)
	}
	if got, want := concrete.textPrompt, "new prompt"; got != want {
		t.Fatalf("session textPrompt = %q, want %q", got, want)
	}

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
			t.Fatalf("event after instruction update = %+v, want session_reconnected", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for session_reconnected after instruction update")
	}
}

func TestNvidiaRealtimeInstructionUpdatesCoalescePendingReconnectLikeReference(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeTextPrompt("old prompt"))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("first UpdateInstructions() error = %v", err)
	}
	if err := session.UpdateInstructions("newer prompt"); err != nil {
		t.Fatalf("second UpdateInstructions() error = %v", err)
	}
	if got, want := concrete.textPrompt, "newer prompt"; got != want {
		t.Fatalf("session textPrompt = %q, want latest prompt %q", got, want)
	}

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
			t.Fatalf("first event after instruction updates = %+v, want session_reconnected", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for coalesced session_reconnected event")
	}
	select {
	case ev := <-session.EventCh():
		t.Fatalf("second event after instruction updates = %+v, want none while restart pending", ev)
	default:
	}
}

func TestNvidiaRealtimeInstructionUpdatesCoalesceBufferedReconnectLikeReference(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeTextPrompt("old prompt"))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("first UpdateInstructions() error = %v", err)
	}
	deadline := time.After(200 * time.Millisecond)
	for len(concrete.events.out) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for buffered session_reconnected event")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := session.UpdateInstructions("newer prompt"); err != nil {
		t.Fatalf("second UpdateInstructions() error = %v", err)
	}

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
			t.Fatalf("first event after buffered instruction updates = %+v, want session_reconnected", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for coalesced session_reconnected event")
	}
	select {
	case ev := <-session.EventCh():
		t.Fatalf("second event after buffered instruction updates = %+v, want none", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNvidiaRealtimeInstructionUpdateAfterReconnectEventRestartsAgainLikeReference(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeTextPrompt("old prompt"))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("first UpdateInstructions() error = %v", err)
	}
	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
			t.Fatalf("first event after instruction update = %+v, want session_reconnected", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for first session_reconnected")
	}

	if err := session.UpdateInstructions("newer prompt"); err != nil {
		t.Fatalf("second UpdateInstructions() error = %v", err)
	}
	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
			t.Fatalf("second event after instruction update = %+v, want session_reconnected", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for second session_reconnected")
	}
}

func TestNvidiaRealtimeInstructionUpdatesCoalesceActiveRestartLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	firstConnected := make(chan struct{}, 1)
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		if attempt == 1 {
			if _, _, err := conn.ReadMessage(); err != nil {
				serverErr <- err
				return
			}
			firstConnected <- struct{}{}
			<-r.Context().Done()
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL(server.URL),
		WithNvidiaRealtimeTextPrompt("old prompt"),
	)
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	select {
	case <-firstConnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before first connection: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first PersonaPlex connection")
	}
	expectNvidiaRealtimeAcquireMetrics(t, session.EventCh())

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("first UpdateInstructions() error = %v", err)
	}
	if err := session.UpdateInstructions("newer prompt"); err != nil {
		t.Fatalf("second UpdateInstructions() error = %v", err)
	}
	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before instruction reconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for instruction-triggered provider reconnect")
	}
	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
			t.Fatalf("first event after instruction updates = %+v, want session_reconnected", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for coalesced session_reconnected")
	}
	select {
	case ev := <-session.EventCh():
		t.Fatalf("second event after active instruction updates = %+v, want none while restart pending", ev)
	default:
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("provider connection attempts = %d, want one reconnect attempt", got)
	}
}

func TestNvidiaRealtimeInstructionUpdateEmitsReconnectAfterHandshakeLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan struct{}, 1)
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		if attempt == 1 {
			if _, _, err := conn.ReadMessage(); err != nil {
				serverErr <- err
				return
			}
			connected <- struct{}{}
			<-r.Context().Done()
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL(server.URL),
		WithNvidiaRealtimeTextPrompt("old prompt"),
	)
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	select {
	case <-connected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before first connection: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first PersonaPlex connection")
	}
	expectNvidiaRealtimeAcquireMetrics(t, session.EventCh())

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions() error = %v", err)
	}
	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before instruction reconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for instruction-triggered provider reconnect")
	}
	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeSessionReconnected || ev.Reconnect == nil {
			t.Fatalf("event after instruction reconnect = %+v, want session_reconnected", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session_reconnected after provider reconnect")
	}
}

func TestNvidiaRealtimeInstructionUpdateDuringReconnectRestartsAgainLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	firstConnected := make(chan struct{}, 1)
	secondConnected := make(chan struct{}, 1)
	thirdConnected := make(chan struct{}, 1)
	releaseSecond := make(chan struct{})
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		switch attempt {
		case 1:
			if _, _, err := conn.ReadMessage(); err != nil {
				serverErr <- err
				return
			}
			firstConnected <- struct{}{}
			<-r.Context().Done()
		case 2:
			if !strings.Contains(r.URL.RawQuery, "text_prompt=new%20prompt") {
				serverErr <- errors.New("second reconnect did not use first updated prompt")
				return
			}
			secondConnected <- struct{}{}
			select {
			case <-releaseSecond:
			case <-r.Context().Done():
			}
		case 3:
			if !strings.Contains(r.URL.RawQuery, "text_prompt=newer%20prompt") {
				serverErr <- errors.New("third reconnect did not use latest updated prompt")
				return
			}
			thirdConnected <- struct{}{}
			<-r.Context().Done()
		}
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL(server.URL),
		WithNvidiaRealtimeTextPrompt("old prompt"),
	)
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	pcm := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(pcm),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(pcm)),
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	select {
	case <-firstConnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before first connection: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first PersonaPlex connection")
	}
	expectNvidiaRealtimeAcquireMetrics(t, session.EventCh())

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("first UpdateInstructions() error = %v", err)
	}
	select {
	case <-secondConnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before first instruction reconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first instruction reconnect")
	}
	deadline := time.After(time.Second)
	for {
		concrete.mu.Lock()
		ready := concrete.transportReady
		concrete.mu.Unlock()
		if ready {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first instruction reconnect handshake")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := session.UpdateInstructions("newer prompt"); err != nil {
		t.Fatalf("second UpdateInstructions() error = %v", err)
	}
	close(releaseSecond)
	select {
	case <-thirdConnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before second instruction reconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second instruction reconnect with latest prompt")
	}
}

func TestNvidiaRealtimeInstructionUpdateDuringRetryReconnectsLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	requestPaths := make(chan string, 4)
	reconnected := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPaths <- r.URL.RawQuery
		if attempts.Add(1) == 1 {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		reconnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL(server.URL),
		WithNvidiaRealtimeTextPrompt("old prompt"),
	)
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	select {
	case <-session.EventCh():
	case err := <-serverErr:
		t.Fatalf("websocket server error before retry update: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial retryable dial error")
	}
	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions() error = %v", err)
	}
	select {
	case <-reconnected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before retry reconnect: %v", err)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timed out waiting for reconnect after instruction update during retry")
	}

	var sawNewPrompt bool
	for {
		select {
		case query := <-requestPaths:
			if strings.Contains(query, "text_prompt=new%20prompt") {
				sawNewPrompt = true
			}
		default:
			if !sawNewPrompt {
				t.Fatal("reconnect did not use updated text_prompt")
			}
			return
		}
	}
}

func TestNvidiaRealtimeInstructionUpdateAfterRetryReconnectRestartsAgainLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	requestPaths := make(chan string, 6)
	connected := make(chan struct{}, 2)
	serverErr := make(chan error, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPaths <- r.URL.RawQuery
		if attempts.Add(1) == 1 {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{nvidiaRealtimeMsgHandshake}); err != nil {
			serverErr <- err
			return
		}
		connected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	realtimeModel := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL(server.URL),
		WithNvidiaRealtimeTextPrompt("old prompt"),
	)
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	defer session.Close()

	select {
	case <-session.EventCh():
	case err := <-serverErr:
		t.Fatalf("websocket server error before retry update: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial retryable dial error")
	}
	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("first UpdateInstructions() error = %v", err)
	}
	select {
	case <-connected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before retry reconnect: %v", err)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timed out waiting for reconnect after instruction update during retry")
	}

	if err := session.UpdateInstructions("newer prompt"); err != nil {
		t.Fatalf("second UpdateInstructions() error = %v", err)
	}
	select {
	case <-connected:
	case err := <-serverErr:
		t.Fatalf("websocket server error before second instruction reconnect: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect after post-retry instruction update")
	}
	expectNvidiaRealtimeReconnectSkippingAcquireMetrics(t, session.EventCh())

	var sawNewerPrompt bool
	for {
		select {
		case query := <-requestPaths:
			if strings.Contains(query, "text_prompt=newer%20prompt") {
				sawNewerPrompt = true
			}
		default:
			if !sawNewerPrompt {
				t.Fatal("second reconnect did not use newer text_prompt")
			}
			return
		}
	}
}

func TestNvidiaRealtimeInstructionUpdateClearsPendingAudioLikeReference(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeTextPrompt("old prompt"))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	partial := makeNvidiaRealtimePCMInputFrame()[:960]
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(partial),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(partial)),
	}); err != nil {
		t.Fatalf("PushAudio(partial) error = %v", err)
	}
	if len(concrete.inputAudioBuffer) == 0 {
		t.Fatal("inputAudioBuffer empty before instruction update, want pending partial audio")
	}

	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions() error = %v", err)
	}
	if got := len(concrete.inputAudioBuffer); got != 0 {
		t.Fatalf("inputAudioBuffer after instruction update = %d, want cleared", got)
	}
	if got := len(concrete.outboundMessages); got != 0 {
		t.Fatalf("outboundMessages after instruction update = %d, want cleared", got)
	}
	if concrete.opusEncoder != nil {
		t.Fatal("opusEncoder after instruction update != nil, want reset")
	}
	if concrete.opusDecoder != nil {
		t.Fatal("opusDecoder after instruction update != nil, want reset")
	}
}

func TestNvidiaRealtimeCloseClearsPendingAudioLikeReference(t *testing.T) {
	realtimeModel := NewNvidiaRealtimeModel()
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	concrete.transportStarted = true
	concrete.transportCtx = ctx
	concrete.transportCancel = cancel

	full := makeNvidiaRealtimePCMInputFrame()
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(full),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(full)),
	}); err != nil {
		t.Fatalf("PushAudio(full) error = %v", err)
	}
	partial := makeNvidiaRealtimePCMInputFrame()[:960]
	if err := session.PushAudio(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes(partial),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(partial)),
	}); err != nil {
		t.Fatalf("PushAudio(partial) error = %v", err)
	}
	concrete.handleBinaryMessage(append([]byte{nvidiaRealtimeMsgAudio}, encodeNvidiaRealtimeOpusPacket(t, makeNvidiaRealtimePCMFrame())...))
	<-session.EventCh()

	if len(concrete.outboundMessages) == 0 || len(concrete.inputAudioBuffer) == 0 || concrete.opusEncoder == nil || concrete.opusDecoder == nil {
		t.Fatalf("pre-close state = messages %d buffer %d encoder %v decoder %v, want pending transport state", len(concrete.outboundMessages), len(concrete.inputAudioBuffer), concrete.opusEncoder != nil, concrete.opusDecoder != nil)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := len(concrete.outboundMessages); got != 0 {
		t.Fatalf("outboundMessages after Close = %d, want cleared", got)
	}
	if got := len(concrete.inputAudioBuffer); got != 0 {
		t.Fatalf("inputAudioBuffer after Close = %d, want cleared", got)
	}
	if concrete.opusEncoder != nil {
		t.Fatal("opusEncoder after Close != nil, want reset")
	}
	if concrete.opusDecoder != nil {
		t.Fatal("opusDecoder after Close != nil, want reset")
	}
}

func TestNvidiaRealtimeSessionFinalizesOnSilenceLikeReference(t *testing.T) {
	realtimeModel := newNvidiaRealtimeModelWithoutPreconnect(WithNvidiaRealtimeSilenceThresholdMS(5))
	session, err := realtimeModel.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	concrete, ok := session.(*nvidiaRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *nvidiaRealtimeSession", session)
	}

	frame := &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	concrete.handleAudioFrame(frame)

	ev := <-session.EventCh()
	if ev.Type != llm.RealtimeEventTypeGenerationCreated || ev.Generation == nil {
		t.Fatalf("event = %+v, want generation_created", ev)
	}
	msg := <-ev.Generation.MessageCh
	if got := <-msg.AudioCh; got != frame {
		t.Fatalf("audio frame = %p, want original frame %p", got, frame)
	}

	var metricsEvent llm.RealtimeEvent
	select {
	case metricsEvent = <-session.EventCh():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for silence metrics event")
	}
	if metricsEvent.Type != llm.RealtimeEventTypeMetricsCollected || metricsEvent.Metrics == nil {
		t.Fatalf("metrics event = %+v, want metrics_collected", metricsEvent)
	}
	if metricsEvent.Metrics.RequestID != ev.Generation.ResponseID || metricsEvent.Metrics.Cancelled {
		t.Fatalf("metrics = %+v, want response id %q and cancelled=false", metricsEvent.Metrics, ev.Generation.ResponseID)
	}
	if _, ok := <-msg.AudioCh; ok {
		t.Fatal("AudioCh open after silence finalization, want closed")
	}
	if _, ok := <-msg.TextCh; ok {
		t.Fatal("TextCh open after silence finalization, want closed")
	}
}

func TestNvidiaRealtimeAllowsZeroSilenceThresholdLikeReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(WithNvidiaRealtimeSilenceThresholdMS(0))

	if got, want := model.silenceThresholdMS, 0; got != want {
		t.Fatalf("silenceThresholdMS = %d, want explicit reference override %d", got, want)
	}
}

func TestNvidiaRealtimeAllowsEmptyTextPromptLikeReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL("ws://personaplex.example:8998"),
		WithNvidiaRealtimeTextPrompt(""),
	)

	if got, want := model.textPrompt, ""; got != want {
		t.Fatalf("textPrompt = %q, want explicit empty prompt", got)
	}
	if got, want := model.websocketURL(), "ws://personaplex.example:8998/api/chat?voice_prompt=NATF2.pt&text_prompt="; got != want {
		t.Fatalf("websocketURL() = %q, want %q", got, want)
	}
}

func TestNvidiaRealtimeAllowsEmptyVoiceLikeReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL("ws://personaplex.example:8998"),
		WithNvidiaRealtimeVoice(""),
	)

	if got, want := model.voice, ""; got != want {
		t.Fatalf("voice = %q, want explicit empty voice", got)
	}
	if got, want := model.Label(), "personaplex-"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := model.websocketURL(), "ws://personaplex.example:8998/api/chat?voice_prompt=.pt&text_prompt=You%20are%20a%20helpful%20assistant."; got != want {
		t.Fatalf("websocketURL() = %q, want %q", got, want)
	}
}

func TestNvidiaRealtimeStripsOnlyFirstURLSchemeLikeReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL("wss://http://personaplex.local:8998"))

	if got, want := model.baseURL, "http://personaplex.local:8998"; got != want {
		t.Fatalf("baseURL = %q, want one reference scheme stripped to %q", got, want)
	}
	if !model.useSSL {
		t.Fatal("useSSL = false, want true from first wss scheme")
	}
}

func TestNvidiaRealtimeWebsocketURLMatchesReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL("https://personaplex.example:9443"),
		WithNvidiaRealtimeVoice("VARF1"),
		WithNvidiaRealtimeTextPrompt("Speak tersely & listen."),
		WithNvidiaRealtimeSeed(7),
	)

	got := model.websocketURL()
	want := "wss://personaplex.example:9443/api/chat?voice_prompt=VARF1.pt&text_prompt=Speak%20tersely%20%26%20listen.&seed=7"
	if got != want {
		t.Fatalf("websocketURL() = %q, want %q", got, want)
	}
	if voicePos, textPos := strings.Index(got, "voice_prompt="), strings.Index(got, "text_prompt="); voicePos < 0 || textPos < 0 || voicePos > textPos {
		t.Fatalf("websocketURL() query order = %q, want voice_prompt before text_prompt like reference", got)
	}
}

func TestNvidiaTTSReferenceDefaultsAndCapabilities(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	if provider.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", provider.apiKey)
	}
	if got, want := provider.voice, "Magpie-Multilingual.EN-US.Leo"; got != want {
		t.Fatalf("voice = %q, want reference default voice %q", got, want)
	}
	if got, want := provider.server, "grpc.nvcf.nvidia.com:443"; got != want {
		t.Fatalf("server = %q, want reference default server %q", got, want)
	}
	if got, want := provider.functionID, "877104f7-e885-42b9-8de8-f6e4c6303969"; got != want {
		t.Fatalf("functionID = %q, want reference function id %q", got, want)
	}
	if got, want := provider.languageCode, "en-US"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
	if !provider.useSSL {
		t.Fatal("useSSL = false, want reference default true")
	}
	if got, want := provider.Label(), "nvidia.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := tts.Model(provider), "Magpie-Multilingual.EN-US.Leo"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "nvidia"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := provider.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want %d", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
}

func TestNvidiaTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "env-secret")

	provider, err := NewNvidiaTTS("", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	if got, want := provider.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestNvidiaTTSAPIKeyEmptyStringFallsBackToEnvironmentLikeReference(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "env-secret")

	provider, err := NewNvidiaTTS("", "", WithNvidiaTTSAPIKey(""))
	if err != nil {
		t.Fatalf("NewNvidiaTTS empty api key error = %v, want nil env fallback like reference", err)
	}
	if got, want := provider.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key for empty api_key %q", got, want)
	}

	t.Setenv("NVIDIA_API_KEY", "")
	_, err = NewNvidiaTTS("", "", WithNvidiaTTSAPIKey(""))
	wantErr := "NVIDIA_API_KEY is not set while using SSL. Either pass api_key parameter, set NVIDIA_API_KEY environment variable or disable SSL and use a locally hosted Riva NIM service."
	if err == nil || err.Error() != wantErr {
		t.Fatalf("NewNvidiaTTS empty api_key without env error = %v, want missing key error", err)
	}
}

func TestNvidiaTTSRequiresAPIKeyWhenUsingSSL(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	_, err := NewNvidiaTTS("", "")

	wantErr := "NVIDIA_API_KEY is not set while using SSL. Either pass api_key parameter, set NVIDIA_API_KEY environment variable or disable SSL and use a locally hosted Riva NIM service."
	if err == nil || err.Error() != wantErr {
		t.Fatalf("NewNvidiaTTS error = %v, want missing key error", err)
	}
}

func TestNvidiaTTSAllowsLocalRivaWithoutAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	provider, err := NewNvidiaTTS("", "", WithNvidiaTTSUseSSL(false))
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v, want local Riva config without key", err)
	}

	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
	if provider.apiKey != "" {
		t.Fatalf("apiKey = %q, want empty local key", provider.apiKey)
	}
}

func TestNvidiaTTSOptionsMatchReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "Magpie-Multilingual.ID-ID.Ayu",
		WithNvidiaTTSServer("localhost:50051"),
		WithNvidiaTTSFunctionID("local-function"),
		WithNvidiaTTSLanguageCode("id-ID"),
		WithNvidiaTTSUseSSL(false),
	)
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	if got, want := provider.voice, "Magpie-Multilingual.ID-ID.Ayu"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := provider.server, "localhost:50051"; got != want {
		t.Fatalf("server = %q, want %q", got, want)
	}
	if got, want := provider.functionID, "local-function"; got != want {
		t.Fatalf("functionID = %q, want %q", got, want)
	}
	if got, want := provider.languageCode, "id-ID"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
}

func TestNvidiaTTSAllowsEmptyVoiceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "", WithNvidiaTTSVoice(""))
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	if got := provider.voice; got != "" {
		t.Fatalf("voice = %q, want explicit empty voice", got)
	}
	if got := provider.Model(); got != "" {
		t.Fatalf("Model() = %q, want explicit empty voice", got)
	}
}

func TestNvidiaTTSAllowsEmptyLanguageCodeLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "", WithNvidiaTTSLanguageCode(""))
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	if got, want := provider.languageCode, ""; got != want {
		t.Fatalf("languageCode = %q, want explicit empty language code", got)
	}
}

func TestNvidiaTTSAllowsEmptyRoutingOptionsLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "",
		WithNvidiaTTSServer(""),
		WithNvidiaTTSFunctionID(""),
	)
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	if got, want := provider.server, ""; got != want {
		t.Fatalf("server = %q, want explicit empty server", got)
	}
	if got, want := provider.functionID, ""; got != want {
		t.Fatalf("functionID = %q, want explicit empty function id", got)
	}
}

func TestNvidiaTTSReportsUnsupportedRivaCalls(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v, want chunked stream before native transport", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.ExceptionStream")
	}
	concrete, ok := stream.(*nvidiaTTSChunkedStream)
	if !ok {
		t.Fatalf("chunked stream type = %T, want *nvidiaTTSChunkedStream", stream)
	}
	if got, want := concrete.text, "hello"; got != want {
		t.Fatalf("chunked stream input text = %q, want %q", got, want)
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before synthesis output")
	}
	if audio, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") || audio != nil {
		t.Fatalf("Next() = (%v, %v), want nil unsupported streaming error from synthesize-with-stream path", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after synthesis error")
	}
	if err := exceptionStream.Exception(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") {
		t.Fatalf("Exception() after synthesis error = %v, want unsupported streaming error", err)
	}
	if audio, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") || audio != nil {
		t.Fatalf("Next() after synthesis error = (%v, %v), want repeated reference task exception", audio, err)
	}
}

func TestNvidiaTTSSynthesizeEmptyTextEndsWithoutTransport(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before empty input EOF")
	}
	if audio, err := stream.Next(); err != io.EOF || audio != nil {
		t.Fatalf("Next() = (%v, %v), want nil EOF for empty input", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after empty input EOF")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after empty input EOF = %v, want nil", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNvidiaTTSStreamConstructsBeforeUnsupportedTransport(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v, want stream construction before native transport", err)
	}
	if err := stream.PushText(""); err != nil {
		t.Fatalf("PushText(empty) error = %v, want nil", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v, want nil", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText(non-empty) error = %v, want nil before native transport", err)
	}
	if err := stream.PushText(" again"); err != nil {
		t.Fatalf("PushText(second) error = %v, want nil before native transport", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before stream output")
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() before output error = %v", err)
	}
	if audio, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") || audio != nil {
		t.Fatalf("Next() = (%v, %v), want nil explicit unsupported stream error", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after stream output error")
	}
	if err := exceptionStream.Exception(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") {
		t.Fatalf("Exception() after stream output error = %v, want unsupported stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText() after Close error = %v, want nil like reference", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() after Close error = %v, want nil like reference", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("synthesize stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() after Close error = %v, want nil like reference", err)
	}
	if audio, err := stream.Next(); err != io.EOF || audio != nil {
		t.Fatalf("Next() after Close = (%v, %v), want nil EOF", audio, err)
	}
}

func TestNvidiaTTSStreamNextWaitsForInputLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	select {
	case got := <-done:
		t.Fatalf("Next() before input returned (%v, %v), want wait for input like reference", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err != io.EOF {
			t.Fatalf("Next() after Close = (%v, %v), want nil EOF", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not unblock after Close")
	}
}

func TestNvidiaTTSStreamNextWaitsForFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after unflushed text returned (%v, %v), want wait for flush like reference", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not unblock after Flush")
	}
}

func TestNvidiaTTSStreamStartsCompletedSentenceBeforeFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("This sentence is long enough. Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after completed sentence = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after completed sentence before Flush")
	}
}

func TestNvidiaTTSStreamKeepsSentenceTailPendingLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long enough. Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if got, want := concrete.text, "This sentence is long enough."; got != want {
		t.Fatalf("released text = %q, want completed sentence only %q", got, want)
	}
	if got, want := concrete.pendingText, "Next"; got != want {
		t.Fatalf("pending text = %q, want unfinished tail %q", got, want)
	}
}

func TestNvidiaTTSStreamFlushQueuesPendingSentenceTailLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long enough. Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if got, want := concrete.readyText, []string{"This sentence is long enough.", "Next"}; !slices.Equal(got, want) {
		t.Fatalf("readyText after Flush = %q, want completed sentence then pending tail %q", got, want)
	}
	if got := concrete.pendingText; got != "" {
		t.Fatalf("pendingText after Flush = %q, want empty after tail queued", got)
	}
}

func TestNvidiaTTSStreamQueuesMultipleCompletedSentencesBeforeFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long enough. Next sentence is long enough. Third"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	wantReady := []string{"This sentence is long enough.", "Next sentence is long enough."}
	if got := concrete.readyText; !slices.Equal(got, wantReady) {
		t.Fatalf("readyText = %q, want all completed sentences before flush %q", got, wantReady)
	}
	if got, want := concrete.pendingText, "Third"; got != want {
		t.Fatalf("pendingText = %q, want unfinished tail %q", got, want)
	}
}

func TestNvidiaTTSStreamNextConsumesQueuedSegmentLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long enough. Next sentence is long enough. Third"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if _, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") {
		t.Fatalf("Next() error = %v, want unsupported transport after first queued segment starts", err)
	}
	wantReady := []string{"Next sentence is long enough."}
	if got := concrete.readyText; !slices.Equal(got, wantReady) {
		t.Fatalf("readyText after Next = %q, want first queued segment consumed and remaining %q", got, wantReady)
	}
}

func TestNvidiaTTSStreamNormalizesNewlineSentenceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long\nenough. Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if got, want := concrete.text, "This sentence is long enough."; got != want {
		t.Fatalf("released text = %q, want normalized completed sentence %q", got, want)
	}
	if got, want := concrete.pendingText, "Next"; got != want {
		t.Fatalf("pending text = %q, want unfinished tail %q", got, want)
	}
}

func TestNvidiaTTSStreamCollapsesNewlineWhitespaceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long \n enough. Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if got, want := concrete.text, "This sentence is long enough."; got != want {
		t.Fatalf("released text = %q, want collapsed newline whitespace %q", got, want)
	}
	if got, want := concrete.pendingText, "Next"; got != want {
		t.Fatalf("pending text = %q, want unfinished tail %q", got, want)
	}
}

func TestNvidiaTTSStreamCollapsesSplitNewlineWhitespaceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long "); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.PushText("\n enough. Next"); err != nil {
		t.Fatalf("PushText(second) error = %v", err)
	}
	if got, want := concrete.text, "This sentence is long enough."; got != want {
		t.Fatalf("released text = %q, want collapsed split newline whitespace %q", got, want)
	}
	if got, want := concrete.pendingText, "Next"; got != want {
		t.Fatalf("pending text = %q, want unfinished tail %q", got, want)
	}
}

func TestNvidiaTTSStreamNormalizesCRLFSentenceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long\r\nenough. Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if got, want := concrete.text, "This sentence is long enough."; got != want {
		t.Fatalf("released text = %q, want normalized CRLF completed sentence %q", got, want)
	}
	if got, want := concrete.pendingText, "Next"; got != want {
		t.Fatalf("pending text = %q, want unfinished tail %q", got, want)
	}
}

func TestNvidiaTTSStreamAppendsTextToPendingTailLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("This sentence is long enough. Next"); err != nil {
		t.Fatalf("PushText(initial) error = %v", err)
	}
	if err := stream.PushText(" piece"); err != nil {
		t.Fatalf("PushText(tail) error = %v", err)
	}
	if got, want := concrete.text, "This sentence is long enough."; got != want {
		t.Fatalf("released text = %q, want completed sentence only %q", got, want)
	}
	if got, want := concrete.pendingText, "Next piece"; got != want {
		t.Fatalf("pending text = %q, want tail plus later delta %q", got, want)
	}
}

func TestNvidiaTTSStreamDoesNotSplitAbbreviationLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("Please connect me to Dr. Smith tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after abbreviation, want wait for real sentence boundary")
	}
	if got, want := concrete.text, "Please connect me to Dr. Smith tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit abbreviation text %q", got, want)
	}
}

func TestNvidiaTTSStreamDoesNotSplitTitleAbbreviationsLikeReference(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "professor", text: "Please consult Prof. Smith for details"},
		{name: "captain", text: "Please consult Capt. Smith for details"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tt.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatalf("flushed = true after %s title abbreviation, want NVIDIA blingfire tokenizer to keep sentence pending", tt.name)
			}
			if got := concrete.text; got != tt.text {
				t.Fatalf("text = %q, want unsplit title abbreviation text %q", got, tt.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitInitialLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("Please connect me to agent A. tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after initial, want wait for real sentence boundary")
	}
	if got, want := concrete.text, "Please connect me to agent A. tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit initial text %q", got, want)
	}
}

func TestNvidiaTTSStreamStartsInitialBeforeCapitalLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}
	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please choose option A. Next step"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if !concrete.flushed {
		t.Fatal("flushed = false after initial before capitalized sentence, want NVIDIA blingfire sentence boundary")
	}
	if got, want := concrete.text, "Please choose option A."; got != want {
		t.Fatalf("text = %q, want first sentence %q", got, want)
	}
	if got, want := concrete.pendingText, "Next step"; got != want {
		t.Fatalf("pendingText = %q, want tail %q", got, want)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after initial-capital boundary = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after initial-capital boundary")
	}
}

func TestNvidiaTTSStreamDoesNotSplitInitialWithoutSpaceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}
	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please choose option A.Next step"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after initial without space, want reference initial protection")
	}
	if got, want := concrete.text, "Please choose option A.Next step"; got != want {
		t.Fatalf("text = %q, want unsplit initial text %q", got, want)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after initial without space returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Flush")
	}
}

func TestNvidiaTTSStreamDoesNotSplitTabInitialLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}
	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please choose option\tA.Next step"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after tab initial without space, want NVIDIA blingfire tokenizer to keep sentence pending")
	}
	if got, want := concrete.text, "Please choose option\tA.Next step"; got != want {
		t.Fatalf("text = %q, want unsplit tab initial text %q", got, want)
	}
	if got := concrete.pendingText; got != "" {
		t.Fatalf("pendingText = %q, want empty pending tail", got)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after tab initial returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Flush")
	}
}

func TestNvidiaTTSStreamStartsCJKSentenceBeforeFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("这是一个足够长的中文句子用于测试语音边界处理。next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after CJK sentence = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after CJK sentence before Flush")
	}
}

func TestNvidiaTTSStreamDoesNotStartShortCJKSentenceBeforeFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("这是一个中文短句。next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after short CJK sentence, want wait for more context like reference")
	}
	if got, want := concrete.text, "这是一个中文短句。next"; got != want {
		t.Fatalf("text = %q, want unsplit short CJK text %q", got, want)
	}
}

func TestNvidiaTTSStreamStartsQuotedSentenceBeforeFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("He said this sentence is ready.\" next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after quoted sentence = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after quoted sentence before Flush")
	}
}

func TestNvidiaTTSStreamStartsSingleQuotedSentenceBeforeFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("He said this sentence is ready.' Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after single-quoted sentence = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after single-quoted sentence before Flush")
	}
}

func TestNvidiaTTSStreamStartsUnicodeQuotedSentenceBeforeFlushLikeReference(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "right curly single quote", text: "He said this sentence is ready.’ Next starts now"},
		{name: "guillemet", text: "He said this sentence is ready.» Next starts now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tt.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after Unicode closing quote, want completed sentence boundary")
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after Unicode quoted sentence before Flush")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsUnicodeBracketedSentenceBeforeFlushLikeReference(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "single guillemet", text: "He said this sentence is ready.› Next starts now"},
		{name: "corner quote", text: "He said this sentence is ready.」 Next starts now"},
		{name: "white corner quote", text: "He said this sentence is ready.』 Next starts now"},
		{name: "angle bracket", text: "He said this sentence is ready.》 Next starts now"},
		{name: "fullwidth parenthesis", text: "He said this sentence is ready.） Next starts now"},
		{name: "fullwidth bracket", text: "He said this sentence is ready.】 Next starts now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tt.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after Unicode closing bracket, want completed sentence boundary")
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after Unicode bracketed sentence before Flush")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsParentheticalSentenceBeforeFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("He said this sentence is ready.) Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after parenthetical sentence = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after parenthetical sentence before Flush")
	}
}

func TestNvidiaTTSStreamStartsBracketedSentenceBeforeFlushLikeReference(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "square bracket", text: "He said this sentence is ready.] Next"},
		{name: "brace", text: "He said this sentence is ready.} Next"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			type result struct {
				audio *tts.SynthesizedAudio
				err   error
			}
			done := make(chan result, 1)
			go func() {
				audio, err := stream.Next()
				done <- result{audio: audio, err: err}
			}()

			if err := stream.PushText(tt.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			select {
			case got := <-done:
				if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
					t.Fatalf("Next() after %s = (%v, %v), want unsupported stream error", tt.name, got.audio, got.err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("Next() did not start after %s before Flush", tt.name)
			}
		})
	}
}

func TestNvidiaTTSStreamStartsStackedClosingSentenceBeforeFlushLikeReference(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "quoted parenthesis", text: "Please read (\"Long enough sentence.\") Next starts now"},
		{name: "quoted bracket", text: "Please read [\"Long enough sentence.\"] Next starts now"},
		{name: "curly quote parenthesis", text: "Please read “Long enough sentence.”) Next starts now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tt.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after stacked closing punctuation, want completed sentence boundary")
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after stacked closing sentence before Flush")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsEllipsisBeforeCapitalLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("This sentence is ready... Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after ellipsis before capital = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after ellipsis before capital")
	}
}

func TestNvidiaTTSStreamDoesNotSplitAdjacentSentenceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("This sentence is ready.Next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after adjacent ASCII sentence text, want NVIDIA blingfire tokenizer to keep sentence pending")
	}
	if got, want := concrete.text, "This sentence is ready.Next"; got != want {
		t.Fatalf("text = %q, want unsplit adjacent sentence text %q", got, want)
	}
	if got := concrete.pendingText; got != "" {
		t.Fatalf("pendingText = %q, want empty pending tail", got)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after adjacent sentence returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Flush")
	}
}

func TestNvidiaTTSStreamDoesNotSplitLowercaseSentenceTailLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}
	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("This sentence is ready. next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after lowercase sentence tail, want NVIDIA blingfire tokenizer to keep sentence pending")
	}
	if got, want := concrete.text, "This sentence is ready. next"; got != want {
		t.Fatalf("text = %q, want unsplit lowercase sentence tail %q", got, want)
	}
	if got := concrete.pendingText; got != "" {
		t.Fatalf("pendingText = %q, want empty pending tail", got)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after lowercase sentence tail returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Flush")
	}
}

func TestNvidiaTTSStreamDoesNotSplitCurlyQuotedLowercaseTailLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}
	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("He said this sentence is ready.” next"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after curly quoted lowercase tail, want NVIDIA blingfire tokenizer to keep sentence pending")
	}
	if got, want := concrete.text, "He said this sentence is ready.” next"; got != want {
		t.Fatalf("text = %q, want unsplit curly quoted lowercase tail %q", got, want)
	}
	if got := concrete.pendingText; got != "" {
		t.Fatalf("pendingText = %q, want empty pending tail", got)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after curly quoted lowercase tail returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Flush")
	}
}

func TestNvidiaTTSStreamDoesNotSplitProtectedPeriodsLikeReference(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "decimal", text: "Please read version 3.14 tomorrow"},
		{name: "website", text: "Please visit example.com tomorrow"},
		{name: "ellipsis", text: "Please wait... tomorrow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tt.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatalf("flushed = true for %s protected period, want wait for real sentence boundary", tt.name)
			}
			if got := concrete.text; got != tt.text {
				t.Fatalf("text = %q, want unsplit protected period text %q", got, tt.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitUppercaseWebsiteLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}
	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please visit longdomain.COM tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after uppercase website suffix, want NVIDIA blingfire tokenizer to keep sentence pending")
	}
	if got, want := concrete.text, "Please visit longdomain.COM tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit uppercase website text %q", got, want)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after uppercase website suffix returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNvidiaTTSStreamDoesNotSplitCompanySuffixLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("Please call Acme Inc. tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after company suffix, want wait for real sentence boundary")
	}
	if got, want := concrete.text, "Please call Acme Inc. tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit company suffix text %q", got, want)
	}
}

func TestNvidiaTTSStreamDoesNotSplitLLCCorpSuffixLikeReference(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "llc", text: "Please contact Foo LLC. Tomorrow"},
		{name: "corp", text: "Please contact Foo Corp. Tomorrow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tt.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatalf("flushed = true after %s suffix, want NVIDIA blingfire tokenizer to keep sentence pending", tt.name)
			}
			if got := concrete.text; got != tt.text {
				t.Fatalf("text = %q, want unsplit company suffix text %q", got, tt.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitTabCompanySuffixLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}
	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please call Acme\tInc. tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after tab company suffix, want NVIDIA blingfire tokenizer to keep sentence pending")
	}
	if got, want := concrete.text, "Please call Acme\tInc. tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit tab company suffix text %q", got, want)
	}
	if got := concrete.pendingText; got != "" {
		t.Fatalf("pendingText = %q, want empty pending tail", got)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after tab company suffix returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Flush")
	}
}

func TestNvidiaTTSStreamDoesNotSplitAdditionalTitleAbbreviationsLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Rev", text: "Please confirm with Rev. Tomorrow follows"},
		{name: "Gen", text: "Please confirm with Gen. Tomorrow follows"},
		{name: "Sen", text: "Please confirm with Sen. Tomorrow follows"},
		{name: "Sgt", text: "Please confirm with Sgt. Tomorrow follows"},
		{name: "Adm", text: "Please confirm with Adm. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after additional title abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit additional title abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitMonthAbbreviationsLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Jan", text: "Your appointment is Jan. Tomorrow follows"},
		{name: "Sept", text: "Your appointment is Sept. Tomorrow follows"},
		{name: "Dec", text: "Your appointment is Dec. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after month abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit month abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamStartsTitleBeforeStarterLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please confirm with Dr. Next step follows"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after title starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after title starter boundary")
	}
}

func TestNvidiaTTSStreamStartsCompanySuffixBeforeNextLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please contact Foo Inc. Next sentence follows"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after company suffix before Next = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after company suffix before Next")
	}
}

func TestNvidiaTTSStreamStartsCommaSuffixBeforeStarterLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please contact Foo Inc., Next step follows"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after comma suffix starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after comma suffix starter boundary")
	}
}

func TestNvidiaTTSStreamStartsPunctuatedSuffixBeforeStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "colon", text: "Please contact Foo Inc.: Next step follows"},
		{name: "semicolon", text: "Please contact Foo Inc.; Next step follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			type result struct {
				audio *tts.SynthesizedAudio
				err   error
			}
			done := make(chan result, 1)
			go func() {
				audio, err := stream.Next()
				done <- result{audio: audio, err: err}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			select {
			case got := <-done:
				if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
					t.Fatalf("Next() after punctuated suffix starter = (%v, %v), want unsupported stream error", got.audio, got.err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after punctuated suffix starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsClosedSuffixBeforeStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "paren", text: "Please contact Foo Inc.) Next step follows"},
		{name: "bracket", text: "Please contact Foo Inc.] Next step follows"},
		{name: "single_quote", text: "Please contact Foo Inc.' Next step follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			type result struct {
				audio *tts.SynthesizedAudio
				err   error
			}
			done := make(chan result, 1)
			go func() {
				audio, err := stream.Next()
				done <- result{audio: audio, err: err}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			select {
			case got := <-done:
				if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
					t.Fatalf("Next() after closed suffix starter = (%v, %v), want unsupported stream error", got.audio, got.err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after closed suffix starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsQuotedStarterAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "ascii_quote", text: "Please contact Foo Inc. \"Next follows now\""},
		{name: "curly_single_quote", text: "Please contact Foo Inc. ‘Next follows now’"},
		{name: "curly_double_quote", text: "Please contact Foo Inc. “Next follows now”"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after quoted starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix quoted starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsOpenedStarterAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "paren", text: "Please contact Foo Inc. (Next follows now)"},
		{name: "bracket", text: "Please contact Foo Inc. [Next follows now]"},
		{name: "brace", text: "Please contact Foo Inc. {Next follows now}"},
		{name: "dash", text: "Please contact Foo Inc. - Next follows now"},
		{name: "emdash", text: "Please contact Foo Inc. — Next follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after opened starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix opened starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsMarkedStarterAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "hash", text: "Please contact Foo Inc. # Next follows now"},
		{name: "asterisk", text: "Please contact Foo Inc. * Next follows now"},
		{name: "bullet", text: "Please contact Foo Inc. • Next follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after marked starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix marked starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsSymbolStarterAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "slash", text: "Please contact Foo Inc. / Next follows now"},
		{name: "backslash", text: "Please contact Foo Inc. \\ Next follows now"},
		{name: "at", text: "Please contact Foo Inc. @ Next follows now"},
		{name: "ampersand", text: "Please contact Foo Inc. & Next follows now"},
		{name: "percent", text: "Please contact Foo Inc. % Next follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after symbol starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix symbol starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsCommonStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "I", text: "Please contact Foo Inc. I will follow now"},
		{name: "You", text: "Please contact Foo Inc. You will follow now"},
		{name: "Why", text: "Please contact Foo Inc. Why will follow now"},
		{name: "Then", text: "Please contact Foo Inc. Then will follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			type result struct {
				audio *tts.SynthesizedAudio
				err   error
			}
			done := make(chan result, 1)
			go func() {
				audio, err := stream.Next()
				done <- result{audio: audio, err: err}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			select {
			case got := <-done:
				if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
					t.Fatalf("Next() after suffix starter = (%v, %v), want unsupported stream error", got.audio, got.err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsAdditionalStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "No", text: "Please contact Foo Inc. No will follow now"},
		{name: "Not", text: "Please contact Foo Inc. Not will follow now"},
		{name: "If", text: "Please contact Foo Inc. If will follow now"},
		{name: "As", text: "Please contact Foo Inc. As will follow now"},
		{name: "For", text: "Please contact Foo Inc. For will follow now"},
		{name: "On", text: "Please contact Foo Inc. On will follow now"},
		{name: "In", text: "Please contact Foo Inc. In will follow now"},
		{name: "At", text: "Please contact Foo Inc. At will follow now"},
		{name: "To", text: "Please contact Foo Inc. To will follow now"},
		{name: "Let", text: "Please contact Foo Inc. Let will follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			type result struct {
				audio *tts.SynthesizedAudio
				err   error
			}
			done := make(chan result, 1)
			go func() {
				audio, err := stream.Next()
				done <- result{audio: audio, err: err}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			select {
			case got := <-done:
				if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
					t.Fatalf("Next() after additional starter = (%v, %v), want unsupported stream error", got.audio, got.err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after additional starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsTemporalStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Should", text: "Please contact Foo Inc. Should follow now"},
		{name: "Now", text: "Please contact Foo Inc. Now follows now"},
		{name: "Today", text: "Please contact Foo Inc. Today follows now"},
		{name: "After", text: "Please contact Foo Inc. After follows now"},
		{name: "Before", text: "Please contact Foo Inc. Before follows now"},
		{name: "Because", text: "Please contact Foo Inc. Because follows now"},
		{name: "Since", text: "Please contact Foo Inc. Since follows now"},
		{name: "While", text: "Please contact Foo Inc. While follows now"},
		{name: "Once", text: "Please contact Foo Inc. Once follows now"},
		{name: "Maybe", text: "Please contact Foo Inc. Maybe follows now"},
		{name: "Yes", text: "Please contact Foo Inc. Yes follows now"},
		{name: "First", text: "Please contact Foo Inc. First follows now"},
		{name: "Second", text: "Please contact Foo Inc. Second follows now"},
		{name: "Finally", text: "Please contact Foo Inc. Finally follows now"},
		{name: "Take", text: "Please contact Foo Inc. Take follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix temporal starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsCommandStarterAfterSuffixLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	done := make(chan struct{}, 1)
	go func() {
		_, _ = stream.Next()
		done <- struct{}{}
	}()

	if err := stream.PushText("Please contact Foo Inc. Go follows now"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if !concrete.flushed {
		t.Fatal("flushed = false after command starter, want completed sentence boundary")
	}
	if got, want := concrete.text, "Please contact Foo Inc."; got != want {
		t.Fatalf("text = %q, want first sentence %q", got, want)
	}
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after suffix command starter boundary")
	}
}

func TestNvidiaTTSStreamStartsConjunctionStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "And", text: "Please contact Foo Inc. And follows now"},
		{name: "Or", text: "Please contact Foo Inc. Or follows now"},
		{name: "Yet", text: "Please contact Foo Inc. Yet follows now"},
		{name: "Still", text: "Please contact Foo Inc. Still follows now"},
		{name: "Instead", text: "Please contact Foo Inc. Instead follows now"},
		{name: "Meanwhile", text: "Please contact Foo Inc. Meanwhile follows now"},
		{name: "Later", text: "Please contact Foo Inc. Later follows now"},
		{name: "Soon", text: "Please contact Foo Inc. Soon follows now"},
		{name: "There", text: "Please contact Foo Inc. There follows now"},
		{name: "Here", text: "Please contact Foo Inc. Here follows now"},
		{name: "These", text: "Please contact Foo Inc. These follows now"},
		{name: "Those", text: "Please contact Foo Inc. Those follows now"},
		{name: "Another", text: "Please contact Foo Inc. Another follows now"},
		{name: "Any", text: "Please contact Foo Inc. Any follows now"},
		{name: "Some", text: "Please contact Foo Inc. Some follows now"},
		{name: "All", text: "Please contact Foo Inc. All follows now"},
		{name: "Each", text: "Please contact Foo Inc. Each follows now"},
		{name: "Every", text: "Please contact Foo Inc. Every follows now"},
		{name: "Most", text: "Please contact Foo Inc. Most follows now"},
		{name: "Many", text: "Please contact Foo Inc. Many follows now"},
		{name: "Much", text: "Please contact Foo Inc. Much follows now"},
		{name: "Several", text: "Please contact Foo Inc. Several follows now"},
		{name: "Both", text: "Please contact Foo Inc. Both follows now"},
		{name: "Neither", text: "Please contact Foo Inc. Neither follows now"},
		{name: "One", text: "Please contact Foo Inc. One follows now"},
		{name: "Two", text: "Please contact Foo Inc. Two follows now"},
		{name: "Three", text: "Please contact Foo Inc. Three follows now"},
		{name: "Last", text: "Please contact Foo Inc. Last follows now"},
		{name: "Previous", text: "Please contact Foo Inc. Previous follows now"},
		{name: "New", text: "Please contact Foo Inc. New follows now"},
		{name: "Only", text: "Please contact Foo Inc. Only follows now"},
		{name: "Other", text: "Please contact Foo Inc. Other follows now"},
		{name: "More", text: "Please contact Foo Inc. More follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after conjunction starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix conjunction starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsAuxiliaryStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Will", text: "Please contact Foo Inc. Will follows now"},
		{name: "Have", text: "Please contact Foo Inc. Have follows now"},
		{name: "Had", text: "Please contact Foo Inc. Had follows now"},
		{name: "Did", text: "Please contact Foo Inc. Did follows now"},
		{name: "Does", text: "Please contact Foo Inc. Does follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after auxiliary starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix auxiliary starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsContractedStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "we_will", text: "Please contact Foo Inc. We'll follow now"},
		{name: "we_curly_will", text: "Please contact Foo Inc. We’ll follow now"},
		{name: "you_will", text: "Please contact Foo Inc. You'll follow now"},
		{name: "you_curly_will", text: "Please contact Foo Inc. You’ll follow now"},
		{name: "i_will", text: "Please contact Foo Inc. I'll follow now"},
		{name: "i_curly_will", text: "Please contact Foo Inc. I’ll follow now"},
		{name: "i_would", text: "Please contact Foo Inc. I'd follow now"},
		{name: "i_have", text: "Please contact Foo Inc. I've follow now"},
		{name: "i_am", text: "Please contact Foo Inc. I'm follow now"},
		{name: "it_is", text: "Please contact Foo Inc. It's follow now"},
		{name: "that_is", text: "Please contact Foo Inc. That's follow now"},
		{name: "there_is", text: "Please contact Foo Inc. There's follow now"},
		{name: "they_are", text: "Please contact Foo Inc. They're follow now"},
		{name: "we_have", text: "Please contact Foo Inc. We've follow now"},
		{name: "you_are", text: "Please contact Foo Inc. You're follow now"},
		{name: "we_are", text: "Please contact Foo Inc. We're follow now"},
		{name: "can_not", text: "Please contact Foo Inc. Can't follow now"},
		{name: "let_us", text: "Please contact Foo Inc. Let's follow now"},
		{name: "do_not", text: "Please contact Foo Inc. Don't follow now"},
		{name: "do_curly_not", text: "Please contact Foo Inc. Don’t follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after contracted starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix contracted starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsDeterminerStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "A", text: "Please contact Foo Inc. A follow-up arrives now"},
		{name: "The", text: "Please contact Foo Inc. The update arrives now"},
		{name: "My", text: "Please contact Foo Inc. My update arrives now"},
		{name: "Your", text: "Please contact Foo Inc. Your update arrives now"},
		{name: "His", text: "Please contact Foo Inc. His update arrives now"},
		{name: "Her", text: "Please contact Foo Inc. Her update arrives now"},
		{name: "Its", text: "Please contact Foo Inc. Its update arrives now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after determiner starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix determiner starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsQuestionStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "What", text: "Please contact Foo Inc. What follows now"},
		{name: "Who", text: "Please contact Foo Inc. Who follows now"},
		{name: "Which", text: "Please contact Foo Inc. Which follows now"},
		{name: "How", text: "Please contact Foo Inc. How follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after question starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix question starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsPrepositionStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "About", text: "Please contact Foo Inc. About follows now"},
		{name: "Over", text: "Please contact Foo Inc. Over follows now"},
		{name: "Under", text: "Please contact Foo Inc. Under follows now"},
		{name: "Through", text: "Please contact Foo Inc. Through follows now"},
		{name: "From", text: "Please contact Foo Inc. From follows now"},
		{name: "By", text: "Please contact Foo Inc. By follows now"},
		{name: "With", text: "Please contact Foo Inc. With follows now"},
		{name: "Without", text: "Please contact Foo Inc. Without follows now"},
		{name: "During", text: "Please contact Foo Inc. During follows now"},
		{name: "Until", text: "Please contact Foo Inc. Until follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after preposition starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix preposition starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsClauseStartersAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Though", text: "Please contact Foo Inc. Though follows now"},
		{name: "Although", text: "Please contact Foo Inc. Although follows now"},
		{name: "Whenever", text: "Please contact Foo Inc. Whenever follows now"},
		{name: "Whatever", text: "Please contact Foo Inc. Whatever follows now"},
		{name: "Whether", text: "Please contact Foo Inc. Whether follows now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			done := make(chan struct{}, 1)
			go func() {
				_, _ = stream.Next()
				done <- struct{}{}
			}()

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if !concrete.flushed {
				t.Fatal("flushed = false after clause starter, want completed sentence boundary")
			}
			if got, want := concrete.text, "Please contact Foo Inc."; got != want {
				t.Fatalf("text = %q, want first sentence %q", got, want)
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Next() did not start after suffix clause starter boundary")
			}
		})
	}
}

func TestNvidiaTTSStreamStartsStarterAfterRepeatedSpacesLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please contact Foo Inc.  Next step follows"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after repeated-space starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after repeated-space starter boundary")
	}
}

func TestNvidiaTTSStreamStartsStarterAfterTabLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please contact Foo Inc.\tNext step follows"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after tab starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after tab starter boundary")
	}
}

func TestNvidiaTTSStreamDoesNotSplitNonStarterAfterSuffixLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Would", text: "Please contact Foo Inc. Would will follow now"},
		{name: "Thanks", text: "Please contact Foo Inc. Thanks will follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after suffix non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit suffix non-starter text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitCommonLowercaseSuffixNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "etc", text: "Use a longer list etc. Tomorrow follows"},
		{name: "vs", text: "Use a longer comparison vs. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after lowercase suffix non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit lowercase suffix text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitCommonLowercaseAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "approx", text: "Use a longer approx approx. Tomorrow follows"},
		{name: "dept", text: "Use a longer dept dept. Tomorrow follows"},
		{name: "fig", text: "Use a longer fig fig. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after lowercase abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit lowercase abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitLowercaseBusinessAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "co", text: "Please contact foo co. Tomorrow follows"},
		{name: "inc", text: "Please contact foo inc. Tomorrow follows"},
		{name: "ltd", text: "Please contact foo ltd. Tomorrow follows"},
		{name: "corp", text: "Please contact foo corp. Tomorrow follows"},
		{name: "jr", text: "Please contact foo jr. Tomorrow follows"},
		{name: "sr", text: "Please contact foo sr. Tomorrow follows"},
		{name: "mfg", text: "Please ask about mfg. Tomorrow follows"},
		{name: "intl", text: "Please ask about intl. Tomorrow follows"},
		{name: "univ", text: "Please ask about univ. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after lowercase business abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit lowercase business abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitLowercaseOrgAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "agric", text: "Please ask about agric. Tomorrow follows"},
		{name: "chem", text: "Please ask about chem. Tomorrow follows"},
		{name: "comm", text: "Please ask about comm. Tomorrow follows"},
		{name: "comp", text: "Please ask about comp. Tomorrow follows"},
		{name: "dist", text: "Please ask about dist. Tomorrow follows"},
		{name: "div", text: "Please ask about div. Tomorrow follows"},
		{name: "eng", text: "Please ask about eng. Tomorrow follows"},
		{name: "engr", text: "Please ask about engr. Tomorrow follows"},
		{name: "mach", text: "Please ask about mach. Tomorrow follows"},
		{name: "mktg", text: "Please ask about mktg. Tomorrow follows"},
		{name: "natl", text: "Please ask about natl. Tomorrow follows"},
		{name: "org", text: "Please ask about org. Tomorrow follows"},
		{name: "serv", text: "Please ask about serv. Tomorrow follows"},
		{name: "tech", text: "Please ask about tech. Tomorrow follows"},
		{name: "tel", text: "Please ask about tel. Tomorrow follows"},
		{name: "trans", text: "Please ask about trans. Tomorrow follows"},
		{name: "util", text: "Please ask about util. Tomorrow follows"},
		{name: "assoc", text: "Please ask about assoc. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after lowercase org abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit lowercase org abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitAdministrativeAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "adj", text: "Please ask about adj. Tomorrow follows"},
		{name: "adv", text: "Please ask about adv. Tomorrow follows"},
		{name: "Assn", text: "Please ask about Assn. Tomorrow follows"},
		{name: "assn", text: "Please ask about assn. Tomorrow follows"},
		{name: "Bldg", text: "Please ask about Bldg. Tomorrow follows"},
		{name: "bldg", text: "Please ask about bldg. Tomorrow follows"},
		{name: "Cmdr", text: "Please ask about Cmdr. Tomorrow follows"},
		{name: "Cong", text: "Please ask about Cong. Tomorrow follows"},
		{name: "cong", text: "Please ask about cong. Tomorrow follows"},
		{name: "Govt", text: "Please ask about Govt. Tomorrow follows"},
		{name: "govt", text: "Please ask about govt. Tomorrow follows"},
		{name: "Hr", text: "Please ask about Hr. Tomorrow follows"},
		{name: "hr", text: "Please ask about hr. Tomorrow follows"},
		{name: "Inst", text: "Please ask about Inst. Tomorrow follows"},
		{name: "inst", text: "Please ask about inst. Tomorrow follows"},
		{name: "Mtg", text: "Please ask about Mtg. Tomorrow follows"},
		{name: "mtg", text: "Please ask about mtg. Tomorrow follows"},
		{name: "Rd", text: "Please ask about Rd. Tomorrow follows"},
		{name: "rd", text: "Please ask about rd. Tomorrow follows"},
		{name: "rev", text: "Please ask about rev. Tomorrow follows"},
		{name: "Sta", text: "Please ask about Sta. Tomorrow follows"},
		{name: "sta", text: "Please ask about sta. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after administrative abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit administrative abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitStateAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Ala", text: "Please ask about Ala. Tomorrow follows"},
		{name: "Ariz", text: "Please ask about Ariz. Tomorrow follows"},
		{name: "Ark", text: "Please ask about Ark. Tomorrow follows"},
		{name: "Calif", text: "Please ask about Calif. Tomorrow follows"},
		{name: "Colo", text: "Please ask about Colo. Tomorrow follows"},
		{name: "Conn", text: "Please ask about Conn. Tomorrow follows"},
		{name: "Del", text: "Please ask about Del. Tomorrow follows"},
		{name: "Fla", text: "Please ask about Fla. Tomorrow follows"},
		{name: "Ga", text: "Please ask about Ga. Tomorrow follows"},
		{name: "Ill", text: "Please ask about Ill. Tomorrow follows"},
		{name: "Ind", text: "Please ask about Ind. Tomorrow follows"},
		{name: "Kans", text: "Please ask about Kans. Tomorrow follows"},
		{name: "Ky", text: "Please ask about Ky. Tomorrow follows"},
		{name: "Mass", text: "Please ask about Mass. Tomorrow follows"},
		{name: "Mich", text: "Please ask about Mich. Tomorrow follows"},
		{name: "Minn", text: "Please ask about Minn. Tomorrow follows"},
		{name: "Miss", text: "Please ask about Miss. Tomorrow follows"},
		{name: "Mont", text: "Please ask about Mont. Tomorrow follows"},
		{name: "Neb", text: "Please ask about Neb. Tomorrow follows"},
		{name: "Nev", text: "Please ask about Nev. Tomorrow follows"},
		{name: "Okla", text: "Please ask about Okla. Tomorrow follows"},
		{name: "Ore", text: "Please ask about Ore. Tomorrow follows"},
		{name: "Penn", text: "Please ask about Penn. Tomorrow follows"},
		{name: "Tenn", text: "Please ask about Tenn. Tomorrow follows"},
		{name: "Tex", text: "Please ask about Tex. Tomorrow follows"},
		{name: "Va", text: "Please ask about Va. Tomorrow follows"},
		{name: "Vt", text: "Please ask about Vt. Tomorrow follows"},
		{name: "Wash", text: "Please ask about Wash. Tomorrow follows"},
		{name: "Wis", text: "Please ask about Wis. Tomorrow follows"},
		{name: "Wyo", text: "Please ask about Wyo. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after state abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit state abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitLowercaseStateAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "del", text: "Please ask about del. Tomorrow follows"},
		{name: "ga", text: "Please ask about ga. Tomorrow follows"},
		{name: "ind", text: "Please ask about ind. Tomorrow follows"},
		{name: "mo", text: "Please ask about mo. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after lowercase state abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit lowercase state abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitCapitalAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Fig", text: "Please review detailed Fig. Tomorrow follows"},
		{name: "Ref", text: "Please review detailed Ref. Would follow now"},
		{name: "Sec", text: "Please review detailed Sec. Thanks follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after capital abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit capital abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitReferenceAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "No", text: "Please review detailed No. Tomorrow follows"},
		{name: "Ch", text: "Please review detailed Ch. Would follow now"},
		{name: "pp", text: "Please review detailed pp. Thanks follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after reference abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit reference abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitPlaceAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Mt", text: "Please meet near quiet Mt. Tomorrow follows"},
		{name: "Ave", text: "Please meet near quiet Ave. Would follow now"},
		{name: "Blvd", text: "Please drive near quiet Blvd. Thanks follow now"},
		{name: "Assoc", text: "Please ask senior Assoc. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after place abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit place abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitCapitalBusinessAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Mfg", text: "Please ask about Mfg. Tomorrow follows"},
		{name: "Intl", text: "Please ask about Intl. Tomorrow follows"},
		{name: "Univ", text: "Please ask about Univ. Tomorrow follows"},
		{name: "Bros", text: "Please ask about Bros. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after capital business abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit capital business abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitCapitalOrgAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Agric", text: "Please ask about Agric. Tomorrow follows"},
		{name: "Chem", text: "Please ask about Chem. Tomorrow follows"},
		{name: "Comm", text: "Please ask about Comm. Tomorrow follows"},
		{name: "Comp", text: "Please ask about Comp. Tomorrow follows"},
		{name: "Dist", text: "Please ask about Dist. Tomorrow follows"},
		{name: "Div", text: "Please ask about Div. Tomorrow follows"},
		{name: "Eng", text: "Please ask about Eng. Tomorrow follows"},
		{name: "Engr", text: "Please ask about Engr. Tomorrow follows"},
		{name: "Lab", text: "Please ask about Lab. Tomorrow follows"},
		{name: "Mach", text: "Please ask about Mach. Tomorrow follows"},
		{name: "Mktg", text: "Please ask about Mktg. Tomorrow follows"},
		{name: "Natl", text: "Please ask about Natl. Tomorrow follows"},
		{name: "Org", text: "Please ask about Org. Tomorrow follows"},
		{name: "Serv", text: "Please ask about Serv. Tomorrow follows"},
		{name: "Tech", text: "Please ask about Tech. Tomorrow follows"},
		{name: "Tel", text: "Please ask about Tel. Tomorrow follows"},
		{name: "Trans", text: "Please ask about Trans. Tomorrow follows"},
		{name: "Util", text: "Please ask about Util. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after capital org abbreviation, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit capital org abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitRoleAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Asst", text: "Please ask senior Asst. Tomorrow follows"},
		{name: "Supt", text: "Please ask senior Supt. Would follow now"},
		{name: "Mgr", text: "Please ask senior Mgr. Thanks follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after role abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit role abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitDepartmentAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Dir", text: "Please ask senior Dir. Tomorrow follows"},
		{name: "DirWould", text: "Please ask senior Dir. Would follow now"},
		{name: "Dept", text: "Please ask senior Dept. Thanks follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after department abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit department abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitLegalAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Hon", text: "Please ask senior Hon. Tomorrow follows"},
		{name: "Atty", text: "Please ask senior Atty. Would follow now"},
		{name: "Esq", text: "Please ask senior Esq. Thanks follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after legal abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit legal abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitMedicalAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Med", text: "Please route medical Med. Tomorrow follows"},
		{name: "MedWould", text: "Please route medical Med. Would follow now"},
		{name: "Hosp", text: "Please route medical Hosp. Thanks follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after medical abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit medical abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitFormalTitleAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "Messrs", text: "Please ask senior Messrs. Tomorrow follows"},
		{name: "Mmes", text: "Please ask senior Mmes. Would follow now"},
		{name: "Msgr", text: "Please ask senior Msgr. Thanks follow now"},
		{name: "Fr", text: "Please ask senior Fr. Tomorrow follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after formal title abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit formal title abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitLowercaseTitleAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "dr", text: "Please ask senior dr. Tomorrow follows"},
		{name: "prof", text: "Please ask senior prof. Would follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after lowercase title abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit lowercase title abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitMeasurementAbbreviationNonStarterLikeReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "min", text: "Please set threshold min. Tomorrow follows"},
		{name: "minWould", text: "Please set threshold min. Would follow now"},
		{name: "avg", text: "Please check average avg. Thanks follow now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewNvidiaTTS("secret", "")
			if err != nil {
				t.Fatalf("NewNvidiaTTS error = %v", err)
			}
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
			if !ok {
				t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
			}

			if err := stream.PushText(tc.text); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if concrete.flushed {
				t.Fatal("flushed = true after measurement abbreviation non-starter, want wait for real sentence boundary")
			}
			if got := concrete.text; got != tc.text {
				t.Fatalf("text = %q, want unsplit measurement abbreviation text %q", got, tc.text)
			}
		})
	}
}

func TestNvidiaTTSStreamDoesNotSplitAcronymLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("Please verify the U.S. address tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after acronym, want wait for real sentence boundary")
	}
	if got, want := concrete.text, "Please verify the U.S. address tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit acronym text %q", got, want)
	}
}

func TestNvidiaTTSStreamDoesNotSplitLowercaseAcronymLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("Please verify the u.s. address tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after lowercase acronym, want wait for real sentence boundary")
	}
	if got, want := concrete.text, "Please verify the u.s. address tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit lowercase acronym text %q", got, want)
	}
}

func TestNvidiaTTSStreamStartsLowercaseAcronymBeforeStarterLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Use a longer example e.g. Next step follows"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after lowercase acronym starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after lowercase acronym starter boundary")
	}
}

func TestNvidiaTTSStreamStartsAcronymBeforeStarterLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("The office is in the U.S. We should continue"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after acronym before starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after acronym before reference starter")
	}
}

func TestNvidiaTTSStreamWaitsForIncompleteAcronymStarterLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("The office is in the U.S. He"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after incomplete acronym starter returned (%v, %v), want wait for more text", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.PushText(" left"); err != nil {
		t.Fatalf("PushText(tail) error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after completed acronym starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after completed acronym starter")
	}
}

func TestNvidiaTTSStreamDoesNotSplitAcronymStarterWithoutSpaceLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("The office is in the U.S.We should continue"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after acronym starter without space, want wait for real sentence boundary")
	}
	if got, want := concrete.text, "The office is in the U.S.We should continue"; got != want {
		t.Fatalf("text = %q, want unsplit acronym starter text %q", got, want)
	}
}

func TestNvidiaTTSStreamStartsLongAcronymBoundaryLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please confirm U.S.A.F. Next step"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after long acronym = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after long acronym boundary")
	}
}

func TestNvidiaTTSStreamDoesNotSplitPhDLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("Please connect me to Ph.D. support tomorrow"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after Ph.D., want wait for real sentence boundary")
	}
	if got, want := concrete.text, "Please connect me to Ph.D. support tomorrow"; got != want {
		t.Fatalf("text = %q, want unsplit Ph.D. text %q", got, want)
	}
}

func TestNvidiaTTSStreamStartsPhDBeforeStarterLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please connect me to Ph.D. Next step follows"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Ph.D. starter = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Ph.D. starter boundary")
	}
}

func TestNvidiaTTSStreamDoesNotSplitBarePhLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		done <- result{audio: audio, err: err}
	}()

	if err := stream.PushText("Please discuss topic Ph.Next step"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if concrete.flushed {
		t.Fatal("flushed = true after bare Ph. adjacent text, want NVIDIA blingfire tokenizer to keep sentence pending")
	}
	if got, want := concrete.text, "Please discuss topic Ph.Next step"; got != want {
		t.Fatalf("text = %q, want unsplit bare Ph. text %q", got, want)
	}
	if got := concrete.pendingText; got != "" {
		t.Fatalf("pendingText = %q, want empty pending tail", got)
	}
	select {
	case got := <-done:
		t.Fatalf("Next() after bare Ph. returned (%v, %v), want wait for Flush", got.audio, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-done:
		if got.audio != nil || got.err == nil || !strings.Contains(got.err.Error(), "riva tts streaming is not implemented") {
			t.Fatalf("Next() after Flush = (%v, %v), want unsupported stream error", got.audio, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not start after Flush")
	}
}

func TestNvidiaTTSStreamNextUnblocksOnCancelLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if audio != nil {
			t.Errorf("Next() audio = %v, want nil", audio)
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Next() before cancel returned %v, want wait for cancellation", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Next() after cancel error = %v, want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not unblock after cancellation")
	}
}

func TestNvidiaTTSStreamEndInputCompletesEmptyReferenceStream(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before end input")
	}
	if err := tts.EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput() error = %v", err)
	}
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText() after EndInput error = %v, want nil ignored late text", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() after EndInput error = %v, want nil no-op", err)
	}
	if audio, err := stream.Next(); err != io.EOF || audio != nil {
		t.Fatalf("Next() after empty EndInput = (%v, %v), want nil EOF", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after empty EndInput EOF")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after empty EndInput EOF = %v, want nil", err)
	}
}

func TestNvidiaTTSStreamIgnoresTextAfterFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("first"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := stream.PushText("second"); err != nil {
		t.Fatalf("PushText(second) error = %v, want nil ignored second segment", err)
	}
	if got, want := concrete.text, "first"; got != want {
		t.Fatalf("stream text = %q, want first flushed segment only %q", got, want)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}
	if err := stream.PushText("third"); err != nil {
		t.Fatalf("PushText(third) error = %v, want nil ignored third segment", err)
	}
	if got, want := concrete.text, "first"; got != want {
		t.Fatalf("stream text after third segment = %q, want first flushed segment only %q", got, want)
	}
}

func TestNvidiaTTSStreamIgnoresTextAfterExplicitFlushLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("first"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := stream.PushText("second"); err != nil {
		t.Fatalf("PushText(second) error = %v, want nil ignored late text", err)
	}
	if got, want := concrete.text, "first"; got != want {
		t.Fatalf("stream text after post-flush PushText = %q, want unchanged first segment %q", got, want)
	}
	if got, want := concrete.readyText, []string{"first"}; !slices.Equal(got, want) {
		t.Fatalf("readyText after post-flush PushText = %q, want %q", got, want)
	}
}

func TestNvidiaTTSStreamPreservesWhitespaceInputLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("   "); err != nil {
		t.Fatalf("PushText(whitespace) error = %v", err)
	}
	if !concrete.hasText {
		t.Fatal("hasText = false, want whitespace counted as reference text input")
	}
	if got, want := concrete.text, "   "; got != want {
		t.Fatalf("text = %q, want preserved whitespace %q", got, want)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText(late) error = %v", err)
	}
	if got, want := concrete.text, "   "; got != want {
		t.Fatalf("text after second segment = %q, want flushed whitespace segment only %q", got, want)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}
	if err := stream.PushText(" tail"); err != nil {
		t.Fatalf("PushText(tail) error = %v", err)
	}
	if got, want := concrete.text, "   "; got != want {
		t.Fatalf("text after third segment = %q, want flushed whitespace segment only %q", got, want)
	}
}

func TestNvidiaTTSStreamWhitespaceFlushIgnoresLaterTextLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushText("   "); err != nil {
		t.Fatalf("PushText(whitespace) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText(late) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if audio != nil {
			t.Errorf("Next() audio = %v, want nil before close", audio)
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Next() after ignored later text returned %v, want wait for close", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("Next() after Close error = %v, want EOF", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not unblock after Close")
	}
}

func TestNvidiaTTSStreamWhitespaceOnlyEndInputDrainsLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.ExceptionStream")
	}

	if err := stream.PushText("   "); err != nil {
		t.Fatalf("PushText(whitespace) error = %v", err)
	}
	if err := tts.EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput() error = %v", err)
	}
	if audio, err := stream.Next(); err != io.EOF || audio != nil {
		t.Fatalf("Next() after whitespace EndInput = (%v, %v), want nil EOF", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after whitespace EndInput EOF")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after whitespace EndInput EOF = %v, want nil", err)
	}
}

func TestNvidiaTTSReturnsCallerCancellationBeforeUnsupportedTransport(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.Synthesize(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Synthesize() error = %v, want context.Canceled", err)
	}
	if _, err := provider.Stream(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
}

func TestNvidiaSTTReferenceDefaultsAndCapabilities(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	if provider.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", provider.apiKey)
	}
	if got, want := provider.model, "parakeet-1.1b-en-US-asr-streaming-silero-vad-sortformer"; got != want {
		t.Fatalf("model = %q, want reference default model %q", got, want)
	}
	if got, want := provider.server, "grpc.nvcf.nvidia.com:443"; got != want {
		t.Fatalf("server = %q, want reference default server %q", got, want)
	}
	if got, want := provider.functionID, "1598d209-5e27-4d3c-8079-4751568b1081"; got != want {
		t.Fatalf("functionID = %q, want reference function id %q", got, want)
	}
	if got, want := provider.language, "en-US"; got != want {
		t.Fatalf("language = %q, want %q", got, want)
	}
	if !provider.punctuate {
		t.Fatal("punctuate = false, want reference default true")
	}
	if !provider.useSSL {
		t.Fatal("useSSL = false, want reference default true")
	}
	if got, want := provider.Label(), "nvidia.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "parakeet-1.1b-en-US-asr-streaming-silero-vad-sortformer"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "nvidia"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if got, want := provider.InputSampleRate(), uint32(16000); got != want {
		t.Fatalf("InputSampleRate() = %d, want reference sample rate %d", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize || caps.Diarization || caps.AlignedTranscript != "word" {
		t.Fatalf("Capabilities() = %+v, want reference streaming interim STT with word alignment and without offline recognition", caps)
	}
}

func TestNvidiaSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "env-secret")

	provider, err := NewNvidiaSTT("", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}

	if got, want := provider.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestNvidiaSTTAllowsExplicitEmptyAPIKeyLikeReference(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "env-secret")

	provider, err := NewNvidiaSTT("", "", WithNvidiaSTTAPIKey(""))
	if err != nil {
		t.Fatalf("NewNvidiaSTT explicit empty api key error = %v, want nil like reference", err)
	}
	if got, want := provider.apiKey, ""; got != want {
		t.Fatalf("apiKey = %q, want explicit empty key", got)
	}
}

func TestNvidiaSTTRequiresAPIKeyWhenUsingSSL(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	_, err := NewNvidiaSTT("", "")

	wantErr := "NVIDIA_API_KEY is not set while using SSL. Either pass api_key parameter, set NVIDIA_API_KEY environment variable or disable SSL and use a locally hosted Riva NIM service."
	if err == nil || err.Error() != wantErr {
		t.Fatalf("NewNvidiaSTT error = %v, want missing key error", err)
	}
}

func TestNvidiaSTTAllowsLocalRivaWithoutAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	provider, err := NewNvidiaSTT("", "", WithNvidiaSTTUseSSL(false))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v, want local Riva config without key", err)
	}

	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
	if provider.apiKey != "" {
		t.Fatalf("apiKey = %q, want empty local key", provider.apiKey)
	}
}

func TestNvidiaSTTOptionsMatchReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "parakeet-rnnt-1.1b",
		WithNvidiaSTTServer("localhost:50051"),
		WithNvidiaSTTFunctionID("local-function"),
		WithNvidiaSTTLanguage("id-ID"),
		WithNvidiaSTTSampleRate(24000),
		WithNvidiaSTTUseSSL(false),
		WithNvidiaSTTDiarization(true),
		WithNvidiaSTTMaxSpeakerCount(4),
		WithNvidiaSTTPunctuate(false),
	)
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}

	if got, want := provider.model, "parakeet-rnnt-1.1b"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if got, want := provider.server, "localhost:50051"; got != want {
		t.Fatalf("server = %q, want %q", got, want)
	}
	if got, want := provider.functionID, "local-function"; got != want {
		t.Fatalf("functionID = %q, want %q", got, want)
	}
	if got, want := provider.language, "id-ID"; got != want {
		t.Fatalf("language = %q, want %q", got, want)
	}
	if got, want := provider.InputSampleRate(), uint32(24000); got != want {
		t.Fatalf("InputSampleRate() = %d, want %d", got, want)
	}
	if !provider.diarization {
		t.Fatal("diarization = false, want true")
	}
	if got, want := provider.maxSpeakerCount, 4; got != want {
		t.Fatalf("maxSpeakerCount = %d, want %d", got, want)
	}
	if provider.punctuate {
		t.Fatal("punctuate = true, want false")
	}
	if caps := provider.Capabilities(); !caps.Diarization || caps.AlignedTranscript != "word" {
		t.Fatalf("Capabilities() = %+v, want reference diarization and word alignment", caps)
	}
	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}

	provider, err = NewNvidiaSTT("secret", "", WithNvidiaSTTMaxSpeakerCount(-1))
	if err != nil {
		t.Fatalf("NewNvidiaSTT(negative max speaker count) error = %v", err)
	}
	if got, want := provider.maxSpeakerCount, -1; got != want {
		t.Fatalf("maxSpeakerCount negative override = %d, want reference value %d", got, want)
	}
}

func TestNvidiaSTTAllowsEmptyModelLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTModel(""))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	if got := provider.model; got != "" {
		t.Fatalf("model = %q, want explicit empty model", got)
	}
	if got := provider.Model(); got != "" {
		t.Fatalf("Model() = %q, want explicit empty model", got)
	}
}

func TestNvidiaSTTAllowsEmptyLanguageLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTLanguage(""))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	if got, want := provider.language, ""; got != want {
		t.Fatalf("language = %q, want explicit empty language", got)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaSTTStream", stream)
	}
	if got, want := concrete.language, ""; got != want {
		t.Fatalf("stream language = %q, want explicit empty provider language", got)
	}
}

func TestNvidiaSTTAllowsEmptyRoutingOptionsLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "",
		WithNvidiaSTTServer(""),
		WithNvidiaSTTFunctionID(""),
	)
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	if got, want := provider.server, ""; got != want {
		t.Fatalf("server = %q, want explicit empty server", got)
	}
	if got, want := provider.functionID, ""; got != want {
		t.Fatalf("functionID = %q, want explicit empty function id", got)
	}
}

func TestNvidiaSTTAllowsZeroSampleRateLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTSampleRate(0))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	if got, want := provider.sampleRate, 0; got != want {
		t.Fatalf("sampleRate = %d, want explicit zero sample rate", got)
	}
	if got, want := provider.InputSampleRate(), uint32(0); got != want {
		t.Fatalf("InputSampleRate() = %d, want explicit zero sample rate", got)
	}

	provider, err = NewNvidiaSTT("secret", "", WithNvidiaSTTSampleRate(-1))
	if err != nil {
		t.Fatalf("NewNvidiaSTT(negative sample rate) error = %v", err)
	}
	if got, want := provider.sampleRate, -1; got != want {
		t.Fatalf("sampleRate negative override = %d, want reference value %d", got, want)
	}
	if got, want := provider.InputSampleRate(), uint32(0); got != want {
		t.Fatalf("InputSampleRate() with negative sample rate = %d, want %d to avoid unsigned wrap", got, want)
	}
}

func TestNvidiaSTTResponseEventsMatchReferenceOrdering(t *testing.T) {
	stream := &nvidiaSTTStream{
		language:        "en-US",
		startTimeOffset: 1.25,
		stt:             &NvidiaSTT{diarization: true},
	}

	events := stream.eventsFromResult(nvidiaSTTResult{
		RequestID: "nvidia-response-1",
		IsFinal:   false,
		Alternative: nvidiaSTTAlternative{
			Transcript: "hello",
			Confidence: 0.7,
			Words: []nvidiaSTTWord{{
				Word:       "hello",
				StartTime:  100,
				EndTime:    340,
				SpeakerTag: 2,
			}},
		},
	})
	if len(events) != 2 {
		t.Fatalf("interim event count = %d, want start_of_speech + interim_transcript", len(events))
	}
	if events[0].Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("event[0].Type = %q, want start_of_speech", events[0].Type)
	}
	if events[1].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event[1].Type = %q, want interim_transcript", events[1].Type)
	}
	if got, want := events[1].RequestID, "nvidia-response-1"; got != want {
		t.Fatalf("interim RequestID = %q, want %q", got, want)
	}
	interim := events[1].Alternatives[0]
	if interim.Text != "hello" || interim.Language != "en-US" || interim.Confidence != 0.7 {
		t.Fatalf("interim speech data = %+v, want transcript/language/confidence from Riva alternative", interim)
	}
	if interim.SpeakerID != "" {
		t.Fatalf("interim SpeakerID = %q, want empty until final diarization", interim.SpeakerID)
	}
	if interim.StartTime != 1.35 || interim.EndTime != 1.59 {
		t.Fatalf("interim timing = (%v, %v), want seconds plus offset", interim.StartTime, interim.EndTime)
	}
	if len(interim.Words) != 1 || interim.Words[0].Text != "hello" || interim.Words[0].StartTime != 101.25 || interim.Words[0].EndTime != 341.25 {
		t.Fatalf("interim words = %+v, want reference millisecond word timings plus offset", interim.Words)
	}

	events = stream.eventsFromResult(nvidiaSTTResult{
		RequestID: "nvidia-response-2",
		IsFinal:   true,
		Alternative: nvidiaSTTAlternative{
			Transcript: "hello there",
			Confidence: 0.9,
			Words: []nvidiaSTTWord{
				{Word: "hello", StartTime: 100, EndTime: 340, SpeakerTag: 2},
				{Word: "there", StartTime: 350, EndTime: 700, SpeakerTag: 2},
				{Word: "aside", StartTime: 710, EndTime: 900, SpeakerTag: 1},
			},
		},
	})
	if len(events) != 2 {
		t.Fatalf("final event count = %d, want final_transcript + end_of_speech", len(events))
	}
	if events[0].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event[0].Type = %q, want final_transcript", events[0].Type)
	}
	if got, want := events[0].RequestID, "nvidia-response-2"; got != want {
		t.Fatalf("final RequestID = %q, want %q", got, want)
	}
	if events[1].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event[1].Type = %q, want end_of_speech", events[1].Type)
	}
	final := events[0].Alternatives[0]
	if final.SpeakerID != "S2" {
		t.Fatalf("final SpeakerID = %q, want majority speaker S2", final.SpeakerID)
	}
	if final.StartTime != 1.35 || final.EndTime != 2.15 {
		t.Fatalf("final timing = (%v, %v), want first/last word seconds plus offset", final.StartTime, final.EndTime)
	}
}

func TestNvidiaSTTFinalDiarizationTieKeepsFirstSpeakerLikeReference(t *testing.T) {
	stream := &nvidiaSTTStream{
		language: "en-US",
		stt:      &NvidiaSTT{diarization: true},
	}

	data := stream.speechDataFromAlternative(nvidiaSTTAlternative{
		Transcript: "one two three four",
		Words: []nvidiaSTTWord{
			{Word: "one", SpeakerTag: 1},
			{Word: "two", SpeakerTag: 2},
			{Word: "three", SpeakerTag: 2},
			{Word: "four", SpeakerTag: 1},
		},
	}, true)

	if got, want := data.SpeakerID, "S1"; got != want {
		t.Fatalf("SpeakerID = %q, want first speaker among tied majority counts %q", got, want)
	}
}

func TestNvidiaSTTResponseEventsPreserveMultipleResultOrder(t *testing.T) {
	stream := &nvidiaSTTStream{language: "en-US"}

	events := stream.eventsFromResponse(nvidiaSTTResponse{
		RequestID: "nvidia-response",
		Results: []nvidiaSTTResult{
			{Alternative: nvidiaSTTAlternative{Transcript: "   "}},
			{
				IsFinal: false,
				Alternative: nvidiaSTTAlternative{
					Transcript: "first",
					Confidence: 0.4,
				},
			},
			{
				IsFinal: true,
				Alternative: nvidiaSTTAlternative{
					Transcript: "second",
					Confidence: 0.8,
				},
			},
		},
	})

	if len(events) != 4 {
		t.Fatalf("event count = %d, want start, interim, final, end", len(events))
	}
	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
	if got := events[1].RequestID; !strings.HasPrefix(got, "nvidia-") {
		t.Fatalf("interim RequestID = %q, want synthetic nvidia- prefix", got)
	}
	if got, want := events[2].RequestID, events[1].RequestID; got != want {
		t.Fatalf("final RequestID = %q, want same response id %q", got, want)
	}
	if got, want := events[1].Alternatives[0].Text, "first"; got != want {
		t.Fatalf("interim text = %q, want %q", got, want)
	}
	if got, want := events[2].Alternatives[0].Text, "second"; got != want {
		t.Fatalf("final text = %q, want %q", got, want)
	}
}

func TestNvidiaSTTResponseEventsKeepSpeakingAfterFinalLikeReference(t *testing.T) {
	stream := &nvidiaSTTStream{language: "en-US"}

	first := stream.eventsFromResponse(nvidiaSTTResponse{
		Results: []nvidiaSTTResult{{
			IsFinal: true,
			Alternative: nvidiaSTTAlternative{
				Transcript: "first utterance",
			},
		}},
	})
	if len(first) != 3 {
		t.Fatalf("first event count = %d, want start, final, end", len(first))
	}
	if first[0].Type != stt.SpeechEventStartOfSpeech || first[1].Type != stt.SpeechEventFinalTranscript || first[2].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("first event types = %q, %q, %q; want start, final, end", first[0].Type, first[1].Type, first[2].Type)
	}
	if !stream.speaking {
		t.Fatal("speaking = false after final, want reference state to remain true")
	}

	second := stream.eventsFromResponse(nvidiaSTTResponse{
		Results: []nvidiaSTTResult{{
			IsFinal: false,
			Alternative: nvidiaSTTAlternative{
				Transcript: "second starts",
			},
		}},
	})
	if len(second) != 1 {
		t.Fatalf("second event count = %d, want interim only while reference speaking state remains true", len(second))
	}
	if second[0].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("second event[0].Type = %q, want interim_transcript without another start_of_speech", second[0].Type)
	}
	if !stream.speaking {
		t.Fatal("speaking = false after later interim, want reference state to remain true")
	}
}

func TestNvidiaSTTResponseEventsUseReferenceRequestIDLikeReference(t *testing.T) {
	stream := &nvidiaSTTStream{language: "en-US"}

	blank := stream.eventsFromResponse(nvidiaSTTResponse{
		Results: []nvidiaSTTResult{{
			IsFinal: true,
			Alternative: nvidiaSTTAlternative{
				Transcript: "   ",
			},
		}},
	})
	first := stream.eventsFromResponse(nvidiaSTTResponse{
		Results: []nvidiaSTTResult{{
			IsFinal: false,
			Alternative: nvidiaSTTAlternative{
				Transcript: "first",
			},
		}},
	})
	second := stream.eventsFromResponse(nvidiaSTTResponse{
		RequestID: "provider-response-id",
		Results: []nvidiaSTTResult{{
			RequestID: "explicit-result",
			IsFinal:   true,
			Alternative: nvidiaSTTAlternative{
				Transcript: "second",
			},
		}},
	})
	third := stream.eventsFromResponse(nvidiaSTTResponse{
		Results: []nvidiaSTTResult{{
			IsFinal: true,
			Alternative: nvidiaSTTAlternative{
				Transcript: "third",
			},
		}},
	})

	if len(blank) != 0 {
		t.Fatalf("blank response event count = %d, want 0", len(blank))
	}
	firstID := first[1].RequestID
	if !strings.HasPrefix(firstID, "nvidia-") {
		t.Fatalf("first RequestID = %q, want synthetic nvidia- prefix like reference", firstID)
	}
	secondID := second[0].RequestID
	if !strings.HasPrefix(secondID, "nvidia-") {
		t.Fatalf("second RequestID = %q, want synthetic nvidia- prefix like reference", secondID)
	}
	if secondID == "explicit-result" {
		t.Fatalf("second RequestID = %q, want ignored provider result id like reference", secondID)
	}
	if secondID == "provider-response-id" {
		t.Fatalf("second RequestID = %q, want ignored provider response id like reference", secondID)
	}
	if secondID == firstID {
		t.Fatalf("second RequestID = %q, want new synthetic id per response", secondID)
	}
	if len(third) != 2 {
		t.Fatalf("third event count = %d, want final, end without another start after prior final", len(third))
	}
	if third[0].Type != stt.SpeechEventFinalTranscript || third[1].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event types = %q, %q; want final, end", third[0].Type, third[1].Type)
	}
	thirdID := third[0].RequestID
	if !strings.HasPrefix(thirdID, "nvidia-") {
		t.Fatalf("third RequestID = %q, want synthetic nvidia- prefix like reference", thirdID)
	}
	if thirdID == firstID || thirdID == secondID {
		t.Fatalf("third RequestID = %q, want new synthetic id per response", thirdID)
	}
}

func TestNvidiaSTTStreamExposesReferenceTimingOffset(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatal("stream does not implement stt.StreamTiming")
	}

	stt.SetStreamStartTimeOffset(timing, 1.25)
	stt.SetStreamStartTime(timing, 10.5)
	if got, want := timing.StartTimeOffset(), 1.25; got != want {
		t.Fatalf("StartTimeOffset() = %v, want %v", got, want)
	}
	if got, want := timing.StartTime(), 10.5; got != want {
		t.Fatalf("StartTime() = %v, want %v", got, want)
	}
}

func TestNvidiaSTTStreamSeedsReferenceStartTime(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	before := float64(time.Now().Add(-time.Second).UnixNano()) / float64(time.Second)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatal("stream does not implement stt.StreamTiming")
	}
	after := float64(time.Now().Add(time.Second).UnixNano()) / float64(time.Second)

	if got := timing.StartTime(); got < before || got > after {
		t.Fatalf("StartTime() = %v, want stream creation wall-clock between %v and %v", got, before, after)
	}
}

func TestNvidiaSTTStreamDropsEmptyFramesLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{}); err != nil {
		t.Fatalf("PushFrame(empty) error = %v, want nil", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0, 1}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(non-empty) error = %v, want nil queued input like reference", err)
	}
	if event, err := stream.Next(); event != nil || err == nil || !strings.Contains(err.Error(), "riva stt streaming is not implemented") {
		t.Fatalf("Next() = (%v, %v), want nil unsupported transport error", event, err)
	}
}

func TestNvidiaSTTStreamNextWaitsForInputLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	type result struct {
		event *stt.SpeechEvent
		err   error
	}
	done := make(chan result, 1)
	go func() {
		event, err := stream.Next()
		done <- result{event: event, err: err}
	}()

	select {
	case got := <-done:
		t.Fatalf("Next() before input returned (%v, %v), want wait for input like reference", got.event, got.err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case got := <-done:
		if got.event != nil || got.err != io.EOF {
			t.Fatalf("Next() after Close = (%v, %v), want nil EOF", got.event, got.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not unblock after Close")
	}
}

func TestNvidiaSTTStreamNextUnblocksOnCancelLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.Stream(ctx, "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		if event != nil {
			t.Errorf("Next() event = %v, want nil", event)
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Next() before cancel returned %v, want wait for cancellation", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Next() after cancel error = %v, want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not unblock after cancellation")
	}
}

func TestNvidiaSTTStreamAcceptsAudioBeforeTransportErrorLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	err = stream.PushFrame(&model.AudioFrame{
		Data:              []byte{1, 0},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	if err != nil {
		t.Fatalf("PushFrame(non-empty) error = %v, want nil queued input like reference", err)
	}
	event, err := stream.Next()
	if event != nil || err == nil || !strings.Contains(err.Error(), "riva stt streaming is not implemented") {
		t.Fatalf("Next() = (%v, %v), want nil unsupported transport error", event, err)
	}
}

func TestNvidiaSTTFlushAfterAudioReportsTransportErrorLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(non-empty) error = %v, want nil queued input like reference", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	event, err := stream.Next()
	if event != nil || err == nil || !strings.Contains(err.Error(), "riva stt streaming is not implemented") {
		t.Fatalf("Next() after audio then Flush = (%v, %v), want nil unsupported transport error", event, err)
	}
}

func TestNvidiaSTTStreamRejectsMismatchedSampleRatesLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{SampleRate: 16000, NumChannels: 1}); err != nil {
		t.Fatalf("PushFrame(first empty frame) error = %v, want nil", err)
	}
	err = stream.PushFrame(&model.AudioFrame{SampleRate: 8000, NumChannels: 1})
	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("PushFrame(mismatched sample rate) error = %v, want reference consistency error", err)
	}

	stream, err = provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream(second) error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{SampleRate: 16000, NumChannels: 1}); err != nil {
		t.Fatalf("PushFrame(nonzero first frame) error = %v, want nil", err)
	}
	err = stream.PushFrame(&model.AudioFrame{SampleRate: 0, NumChannels: 1})
	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("PushFrame(zero after nonzero) error = %v, want reference consistency error", err)
	}
}

func TestNvidiaSTTStreamNormalizesInputSampleRateLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTSampleRate(16000))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaSTTStream", stream)
	}

	frame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{0, 3000, 6000, 9000, 12000, 15000}),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 6,
	}
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame(48kHz) error = %v, want nil", err)
	}
	if got, want := concrete.inputSampleRate, uint32(16000); got != want {
		t.Fatalf("inputSampleRate = %d, want provider-normalized sample rate %d", got, want)
	}
}

func TestNvidiaSTTStreamNormalizesInputChannelsLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTSampleRate(16000))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaSTTStream", stream)
	}

	normalized, err := concrete.normalizeInputFrame(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{1000, 3000, 5000, 9000}),
		SampleRate:        16000,
		NumChannels:       2,
		SamplesPerChannel: 2,
	})
	if err != nil {
		t.Fatalf("normalizeInputFrame(stereo) error = %v", err)
	}
	if normalized.NumChannels != 1 || normalized.SampleRate != 16000 || normalized.SamplesPerChannel != 2 {
		t.Fatalf("normalized format = %d Hz/%d ch/%d samples, want 16000 Hz/1 ch/2 samples", normalized.SampleRate, normalized.NumChannels, normalized.SamplesPerChannel)
	}
	if got, want := littleEndianBytesToInt16Slice(normalized.Data), []int16{2000, 7000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized mono PCM = %v, want channel averages %v", got, want)
	}
}

func TestNvidiaSTTStreamPreservesResamplePhaseLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTSampleRate(16000))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	whole, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream(whole) error = %v", err)
	}
	wholeStream := whole.(*nvidiaSTTStream)
	wholeFrame := &model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{0, 3000, 6000, 9000, 12000, 15000}),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 6,
	}
	wholeNormalized, err := wholeStream.normalizeInputFrame(wholeFrame)
	if err != nil {
		t.Fatalf("normalizeInputFrame(whole) error = %v", err)
	}

	split, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream(split) error = %v", err)
	}
	splitStream := split.(*nvidiaSTTStream)
	first, err := splitStream.normalizeInputFrame(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{0, 3000}),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})
	if err != nil {
		t.Fatalf("normalizeInputFrame(first split) error = %v", err)
	}
	second, err := splitStream.normalizeInputFrame(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{6000, 9000, 12000, 15000}),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 4,
	})
	if err != nil {
		t.Fatalf("normalizeInputFrame(second split) error = %v", err)
	}

	var got []int16
	if first != nil {
		got = append(got, littleEndianBytesToInt16Slice(first.Data)...)
	}
	if second != nil {
		got = append(got, littleEndianBytesToInt16Slice(second.Data)...)
	}
	want := littleEndianBytesToInt16Slice(wholeNormalized.Data)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split normalized PCM = %v, want whole-frame PCM %v from stateful reference resampler", got, want)
	}
}

func TestNvidiaSTTFlushDrainsBufferedResampleInputLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTSampleRate(16000))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{0, 3000}),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}); err != nil {
		t.Fatalf("PushFrame(short 48kHz) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if event, err := stream.Next(); event != nil || err == nil || !strings.Contains(err.Error(), "nvidia riva stt streaming is not implemented") {
		t.Fatalf("Next() after buffered resample Flush = (%v, %v), want unsupported transport error", event, err)
	}
}

func TestNvidiaSTTEndInputDrainsBufferedResampleInputLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "", WithNvidiaSTTSampleRate(16000))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              int16SliceToLittleEndianBytes([]int16{0, 3000}),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}); err != nil {
		t.Fatalf("PushFrame(short 48kHz) error = %v", err)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if event, err := stream.Next(); event != nil || err == nil || !strings.Contains(err.Error(), "nvidia riva stt streaming is not implemented") {
		t.Fatalf("Next() after buffered resample EndInput = (%v, %v), want unsupported transport error", event, err)
	}
}

func TestNvidiaSTTStreamEndInputCompletesEmptyReferenceStream(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}

	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if err := ending.EndInput(); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("second EndInput() error = %v, want input ended", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{}); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame() after EndInput error = %v, want input ended", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("Flush() after EndInput error = %v, want input ended", err)
	}
	if event, err := stream.Next(); err != io.EOF || event != nil {
		t.Fatalf("Next() after empty EndInput = (%v, %v), want nil EOF", event, err)
	}
}

func TestNvidiaSTTStreamClosedInputReportsInputEndedLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for name, fn := range map[string]func() error{
		"PushFrame": func() error { return stream.PushFrame(&model.AudioFrame{}) },
		"Flush":     stream.Flush,
		"EndInput":  ending.EndInput,
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "input ended") {
			t.Fatalf("%s after Close error = %v, want input ended", name, err)
		}
	}

	stream, err = provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream(second) error = %v", err)
	}
	ending, ok = stream.(stt.InputEnding)
	if !ok {
		t.Fatal("second stream does not implement stt.InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	for name, fn := range map[string]func() error{
		"PushFrame": func() error { return stream.PushFrame(&model.AudioFrame{}) },
		"Flush":     stream.Flush,
		"EndInput":  ending.EndInput,
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "input ended") {
			t.Fatalf("%s after EndInput error = %v, want input ended", name, err)
		}
	}
}

func TestNvidiaSTTEndInputFlushesBeforeCloseLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaSTTStream", stream)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}

	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if !concrete.flushed {
		t.Fatal("EndInput() left flushed=false, want implicit flush boundary before input close")
	}
}

func TestNvidiaSTTFlushKeepsInputOpenLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{}); err != nil {
		t.Fatalf("PushFrame(empty) error = %v, want nil", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{}); err != nil {
		t.Fatalf("PushFrame(empty) after Flush error = %v, want nil", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("second Flush() error = %v, want nil", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() after Flush error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{}); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame() after EndInput error = %v, want input ended", err)
	}
	if event, err := stream.Next(); err != io.EOF || event != nil {
		t.Fatalf("Next() after empty EndInput = (%v, %v), want nil EOF", event, err)
	}
}

func TestNvidiaSTTFlushStopsWorkerLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(non-empty) after Flush error = %v, want nil ignored late audio", err)
	}
	if event, err := stream.Next(); err != io.EOF || event != nil {
		t.Fatalf("Next() after Flush = (%v, %v), want nil EOF", event, err)
	}
}

func TestNvidiaSTTFlushStillRejectsMismatchedSampleRateLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(first) error = %v, want nil queued input like reference", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	err = stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1})
	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("PushFrame(mismatched after Flush) error = %v, want reference consistency error", err)
	}
}

func TestNvidiaSTTStreamReturnsCallerCancellationBeforeUnsupportedTransport(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if stream, err := provider.Stream(ctx, ""); !errors.Is(err, context.Canceled) || stream != nil {
		t.Fatalf("Stream(canceled) = (%v, %v), want nil context.Canceled", stream, err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	stream, err := provider.Stream(ctx, "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	cancel()

	err = stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PushFrame() error = %v, want context.Canceled", err)
	}
	if err := stream.Flush(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Flush() error = %v, want context.Canceled", err)
	}
	if event, err := stream.Next(); !errors.Is(err, context.Canceled) || event != nil {
		t.Fatalf("Next() = (%v, %v), want nil context.Canceled", event, err)
	}
}

func TestNvidiaSTTReturnsCallerCancellationBeforeUnsupportedRecognize(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	event, err := provider.Recognize(ctx, []*model.AudioFrame{{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}}, "")
	if !errors.Is(err, context.Canceled) || event != nil {
		t.Fatalf("Recognize() = (%v, %v), want nil context.Canceled", event, err)
	}
}

func TestNvidiaSTTReportsUnsupportedRivaCallsAndClosedInput(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	recognizeErr := "Not implemented"
	if _, err := provider.Recognize(context.Background(), nil, ""); err == nil || err.Error() != recognizeErr {
		t.Fatalf("Recognize() error = %v, want explicit unsupported recognition error", err)
	}

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaSTTStream", stream)
	}
	if got, want := concrete.language, "id-ID"; got != want {
		t.Fatalf("stream language = %q, want %q", got, want)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame() error = %v, want nil queued input like reference", err)
	}
	if event, err := stream.Next(); event != nil || err == nil || !strings.Contains(err.Error(), "riva stt streaming is not implemented") {
		t.Fatalf("Next() = (%v, %v), want nil unsupported transport error", event, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{}); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame() after Close error = %v, want input ended", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("Flush() after Close error = %v, want input ended", err)
	}
	if event, err := stream.Next(); err != io.EOF || event != nil {
		t.Fatalf("Next() after Close = (%v, %v), want nil EOF", event, err)
	}
}

func encodeNvidiaRealtimeOpusPacket(t *testing.T, pcm []int16) []byte {
	t.Helper()
	encoder, err := opus.NewEncoder(defaultNvidiaRealtimeSampleRate, defaultNvidiaRealtimeNumChannels, opus.AppVoIP)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}
	buf := make([]byte, 256)
	n, err := encoder.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if n == 0 {
		t.Fatal("Encode() wrote zero bytes")
	}
	return append([]byte(nil), buf[:n]...)
}

func makeNvidiaRealtimePCMFrame() []int16 {
	pcm := make([]int16, 480)
	for i := range pcm {
		pcm[i] = int16((i%32 - 16) * 128)
	}
	return pcm
}

func makeNvidiaRealtimePCMInputFrame() []int16 {
	pcm := make([]int16, 1920)
	for i := range pcm {
		pcm[i] = int16((i%64 - 32) * 64)
	}
	return pcm
}
