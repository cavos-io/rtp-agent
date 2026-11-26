package livekit

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

// Repro harness for PRD call_logs 3295518: a real AgentSession + PipelineAgent publishing
// through a real RoomIO. Only the TTS provider and the Opus encoder are faked, so every frame
// that reaches the track is attributable to the reply that produced it.

// markedTTS stamps each reply's PCM with the first byte of its text. Frames are emitted
// instantly (faster than real time, like a streaming provider) in frameDur-sized chunks.
type markedTTS struct {
	tts.MetricsEmitter
	tts.ErrorEmitter
	frameDur time.Duration
	total    time.Duration
}

func (m *markedTTS) Label() string { return "marked" }
func (m *markedTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}
func (m *markedTTS) SampleRate() int  { return 48000 }
func (m *markedTTS) NumChannels() int { return 1 }
func (m *markedTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, nil
}
func (m *markedTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return &markedTTSStream{tts: m, ended: make(chan struct{})}, nil
}

type markedTTSStream struct {
	tts     *markedTTS
	mu      sync.Mutex
	text    strings.Builder
	ended   chan struct{}
	endOnce sync.Once
	sent    time.Duration
}

func (s *markedTTSStream) PushText(text string) error {
	s.mu.Lock()
	s.text.WriteString(text)
	s.mu.Unlock()
	return nil
}
func (s *markedTTSStream) Flush() error { return nil }
func (s *markedTTSStream) EndInput() error {
	s.endOnce.Do(func() { close(s.ended) })
	return nil
}
func (s *markedTTSStream) Close() error { return s.EndInput() }
func (s *markedTTSStream) Next() (*tts.SynthesizedAudio, error) {
	<-s.ended
	s.mu.Lock()
	defer s.mu.Unlock()
	text := s.text.String()
	if text == "" || s.sent >= s.tts.total {
		return nil, io.EOF
	}
	s.sent += s.tts.frameDur
	return &tts.SynthesizedAudio{Frame: markedFrame(text[0], s.tts.frameDur)}, nil
}

func markedFrame(marker byte, dur time.Duration) *model.AudioFrame {
	samples := int(48000 * dur / time.Second)
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		data[i*2] = marker
	}
	return &model.AudioFrame{Data: data, SampleRate: 48000, NumChannels: 1, SamplesPerChannel: uint32(samples)}
}

type publishedFrame struct {
	marker byte
	at     time.Time
}

// markerEncoder records which reply each 20 ms frame handed to the transport belongs to.
// It counts encoded frames: what the worker publishes, not what a receiver plays. A frame
// encoded just before a cancellation may not reach the track (at most one per interrupt).
type markerEncoder struct {
	mu     sync.Mutex
	frames []publishedFrame
}

func (e *markerEncoder) Encode(pcm []byte) ([]byte, error) {
	e.mu.Lock()
	e.frames = append(e.frames, publishedFrame{marker: pcm[0], at: time.Now()})
	e.mu.Unlock()
	return []byte{0x01}, nil
}
func (e *markerEncoder) Close() error { return nil }

func (e *markerEncoder) snapshot() []publishedFrame {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]publishedFrame(nil), e.frames...)
}

// encoded is the audio duration of marker encoded in (after, +inf).
func (e *markerEncoder) encoded(marker byte, after time.Time) time.Duration {
	var d time.Duration
	for _, f := range e.snapshot() {
		if f.marker == marker && f.at.After(after) {
			d += 20 * time.Millisecond
		}
	}
	return d
}

type interruptHarness struct {
	session *agent.AgentSession
	rio     *RoomIO
	enc     *markerEncoder
}

func newInterruptHarness(t *testing.T, frameDur, total time.Duration, opts agent.AgentSessionOptions) *interruptHarness {
	t.Helper()
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, opts)
	session.TTS = &markedTTS{frameDur: frameDur, total: total}
	enc := &markerEncoder{}
	rio := &RoomIO{AgentSession: session, audioTrack: newRoomIOTestAudioTrack(t), encoder: enc}
	session.SetAudioOutputController(rio)
	session.SetAudioPlaybackController(roomIOPlaybackController{rio: rio})
	session.EnsureAssistant().SetPublishAudio(rio.PublishAudio)
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("session.Start error = %v", err)
	}
	t.Cleanup(func() { session.Shutdown() })
	return &interruptHarness{session: session, rio: rio, enc: enc}
}

func (h *interruptHarness) say(t *testing.T, text string) *agent.SpeechHandle {
	t.Helper()
	handle, err := h.session.Say(context.Background(), text)
	if err != nil {
		t.Fatalf("Say(%q) error = %v", text, err)
	}
	return handle
}

func (h *interruptHarness) waitEncoded(t *testing.T, marker byte, atLeast time.Duration) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for h.enc.encoded(marker, time.Time{}) < atLeast {
		if time.Now().After(deadline) {
			t.Fatalf("reply %q published %v, want at least %v encoded", marker, h.enc.encoded(marker, time.Time{}), atLeast)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitHandle(t *testing.T, handle *agent.SpeechHandle) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handle.Wait(ctx); err != nil {
		t.Fatalf("speech %s did not finish: %v", handle.ID, err)
	}
}

// The frames a provider hands over are not 20 ms: ElevenLabs delivers multi-second chunks.
const reproProviderChunk = 4 * time.Second

func TestInterruptStopsPublishedAudioWithinLeadBudget(t *testing.T) {
	h := newInterruptHarness(t, reproProviderChunk, reproProviderChunk, agent.AgentSessionOptions{})
	handle := h.say(t, "A long reply")
	h.waitEncoded(t, 'A', 300*time.Millisecond)

	interruptedAt := time.Now()
	if err := h.session.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	waitHandle(t, handle)
	time.Sleep(400 * time.Millisecond)

	if late := h.enc.encoded('A', interruptedAt.Add(roomIOOutputMaxLead)); late > 0 {
		t.Fatalf("published %v of the interrupted reply more than %v after the interrupt", late, roomIOOutputMaxLead)
	}
}

func TestPauseStopsPublishedAudioWithinLeadBudget(t *testing.T) {
	h := newInterruptHarness(t, reproProviderChunk, reproProviderChunk, agent.AgentSessionOptions{})
	h.say(t, "A long reply")
	h.waitEncoded(t, 'A', 300*time.Millisecond)

	pausedAt := time.Now()
	h.rio.PauseAudioOutput()
	time.Sleep(700 * time.Millisecond)
	// Release the parked reply so session shutdown is not left waiting on it.
	defer func() {
		_ = h.session.Interrupt(true)
		h.rio.ResumeAudioOutput()
	}()

	if late := h.enc.encoded('A', pausedAt.Add(roomIOOutputMaxLead)); late > 0 {
		t.Fatalf("published %v of the paused reply more than %v after the pause", late, roomIOOutputMaxLead)
	}
}

func TestReplyAfterInterruptIsPublishedInFull(t *testing.T) {
	const replyDur = 600 * time.Millisecond
	h := newInterruptHarness(t, 200*time.Millisecond, replyDur, agent.AgentSessionOptions{})
	first := h.say(t, "A first reply")
	h.waitEncoded(t, 'A', 100*time.Millisecond)
	if err := h.session.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	second := h.say(t, "B second reply")
	waitHandle(t, first)
	waitHandle(t, second)

	if got := h.enc.encoded('B', time.Time{}); got != replyDur {
		t.Fatalf("second reply published %v of audio, want all %v", got, replyDur)
	}
}

// storedAfterInterrupt speaks a 4 s reply delivered in chunk-sized frames, interrupts it once
// playFor of audio is out, and returns the committed assistant text ("" when none).
func storedAfterInterrupt(t *testing.T, chunk, playFor time.Duration) (stored, full string, encoded time.Duration) {
	t.Helper()
	h := newInterruptHarness(t, chunk, 4*time.Second, agent.AgentSessionOptions{})
	full = "A " + strings.Repeat("kata ", 40)
	handle := h.say(t, full)
	h.waitEncoded(t, 'A', playFor)
	if err := h.session.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	waitHandle(t, handle)
	for _, item := range handle.ChatItems() {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleAssistant {
			stored = msg.TextContent()
		}
	}
	return stored, full, h.enc.encoded('A', time.Time{})
}

func TestInterruptedReplyStoresOnlyPublishedText(t *testing.T) {
	for name, chunk := range map[string]time.Duration{"20ms frames": 20 * time.Millisecond, "1s chunks": time.Second, "single 4s chunk": 4 * time.Second} {
		t.Run(name, func(t *testing.T) {
			stored, full, encoded := storedAfterInterrupt(t, chunk, time.Second)
			// ~1.2 s of a 4 s reply reached the track: the stored text must be a matching prefix.
			if stored == "" || !strings.HasPrefix(full, stored) || len(stored) > len(full)/2 {
				t.Fatalf("stored %q (%d of %d chars) after encoding %v of 4s", stored, len(stored), len(full), encoded)
			}
		})
	}
}

// scriptedLLM answers turn n with replies[n] (the last one repeats), like the PRD reply path
// (LLM -> TTS). Each reply's first byte is its audio marker.
type scriptedLLM struct {
	llm.MetricsEmitter
	llm.ErrorEmitter
	mu      sync.Mutex
	replies []string
	calls   int
}

func (l *scriptedLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	l.mu.Lock()
	reply := l.replies[min(l.calls, len(l.replies)-1)]
	l.calls++
	l.mu.Unlock()
	return &scriptedLLMStream{chunks: strings.SplitAfter(reply, " ")}, nil
}

type scriptedLLMStream struct {
	chunks []string
	next   int
}

func (s *scriptedLLMStream) Next() (*llm.ChatChunk, error) {
	if s.next >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := &llm.ChatChunk{Delta: &llm.ChoiceDelta{Content: s.chunks[s.next]}}
	s.next++
	return chunk, nil
}
func (s *scriptedLLMStream) Close() error { return nil }

func assistantText(handle *agent.SpeechHandle) string {
	for _, item := range handle.ChatItems() {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleAssistant {
			return msg.TextContent()
		}
	}
	return ""
}

func (e *markerEncoder) encodedAll(after time.Time) time.Duration {
	var d time.Duration
	for _, f := range e.snapshot() {
		if f.at.After(after) {
			d += 20 * time.Millisecond
		}
	}
	return d
}

func newLLMReplyHarness(t *testing.T, chunk, total time.Duration, replies ...string) *interruptHarness {
	t.Helper()
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	session.LLM = &scriptedLLM{replies: replies}
	session.TTS = &markedTTS{frameDur: chunk, total: total}
	enc := &markerEncoder{}
	rio := &RoomIO{AgentSession: session, audioTrack: newRoomIOTestAudioTrack(t), encoder: enc}
	session.SetAudioOutputController(rio)
	session.SetAudioPlaybackController(roomIOPlaybackController{rio: rio})
	session.EnsureAssistant().SetPublishAudio(rio.PublishAudio)
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("session.Start error = %v", err)
	}
	t.Cleanup(func() { session.Shutdown() })
	return &interruptHarness{session: session, rio: rio, enc: enc}
}

func (h *interruptHarness) waitEncodedAll(t *testing.T, atLeast time.Duration) {
	t.Helper()
	deadline := time.Now().Add(atLeast + 5*time.Second)
	for h.enc.encodedAll(time.Time{}) < atLeast {
		if time.Now().After(deadline) {
			t.Fatalf("encoded %v, want at least %v", h.enc.encodedAll(time.Time{}), atLeast)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// reproReplyDur is roughly how long reproReply takes to speak (~50 syllables).
const reproReplyDur = 12 * time.Second

const reproReply = "A satu kata kata kata kata kata kata kata. B dua kata kata kata kata kata kata kata. C tiga kata kata kata kata kata kata kata."

func TestLLMReplyInterruptStopsAudioAndStoresHeardText(t *testing.T) {
	cases := map[string]struct{ chunk, playFor time.Duration }{
		"20ms frames, cut early": {20 * time.Millisecond, 3 * time.Second},
		"2s chunks, cut late":    {2 * time.Second, 7 * time.Second},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			chunk := tc.chunk
			h := newLLMReplyHarness(t, chunk, reproReplyDur, reproReply)
			handle, err := h.session.GenerateReply(context.Background(), "halo")
			if err != nil {
				t.Fatalf("GenerateReply error = %v", err)
			}
			h.waitEncodedAll(t, tc.playFor)
			interruptedAt := time.Now()
			if err := h.session.Interrupt(false); err != nil {
				t.Fatalf("Interrupt error = %v", err)
			}
			waitHandle(t, handle)
			time.Sleep(400 * time.Millisecond)

			if late := h.enc.encodedAll(interruptedAt.Add(roomIOOutputMaxLead)); late > 0 {
				t.Fatalf("published %v more than %v after the interrupt", late, roomIOOutputMaxLead)
			}
			stored, encoded := assistantText(handle), h.enc.encodedAll(time.Time{})
			t.Logf("encoded %v, stored %q", encoded, stored)
			if stored == "" || !strings.HasPrefix(reproReply, stored) {
				t.Fatalf("stored %q, want a non-empty prefix of the reply", stored)
			}
			// The caption pace is an estimate (syllable rate), so allow a quarter either way.
			heard := float64(encoded) / float64(reproReplyDur)
			share := float64(len(stored)) / float64(len(reproReply))
			if share > heard+0.25 || share < heard-0.25 {
				t.Fatalf("stored %.0f%% of the text after encoding %.0f%% of the audio", share*100, heard*100)
			}
		})
	}
}

func TestLLMReplyPausedWhileThinkingStoresOnlyHeardText(t *testing.T) {
	for name, chunk := range map[string]time.Duration{"20ms frames": 20 * time.Millisecond, "4s chunk": 4 * time.Second} {
		t.Run(name, func(t *testing.T) {
			h := newLLMReplyHarness(t, chunk, reproReplyDur, reproReply)
			h.rio.PauseAudioOutput()
			handle, err := h.session.GenerateReply(context.Background(), "halo")
			if err != nil {
				t.Fatalf("GenerateReply error = %v", err)
			}
			time.Sleep(2 * time.Second) // reply is ready but parked behind the pause
			if got := h.enc.encodedAll(time.Time{}); got != 0 {
				t.Fatalf("encoded %v while paused", got)
			}
			h.rio.ResumeAudioOutput()
			h.waitEncodedAll(t, 300*time.Millisecond)
			if err := h.session.Interrupt(false); err != nil {
				t.Fatalf("Interrupt error = %v", err)
			}
			waitHandle(t, handle)

			stored, encoded := assistantText(handle), h.enc.encodedAll(time.Time{})
			t.Logf("encoded %v, stored %q", encoded, stored)
			if !strings.HasPrefix(reproReply, stored) {
				t.Fatalf("stored %q, want a prefix of the reply", stored)
			}
			// ~0.5 s was heard: about two syllables' worth, never the 2 s spent parked.
			if words := len(strings.Fields(stored)); words > 3 {
				t.Fatalf("stored %d words (%q) after encoding %v", words, stored, encoded)
			}
		})
	}
}

func TestLLMReplyAfterInterruptedReplyIsPublished(t *testing.T) {
	h := newLLMReplyHarness(t, 2*time.Second, 2*time.Second, "A satu kata kata.", "B dua kata kata.")
	first, err := h.session.GenerateReply(context.Background(), "halo")
	if err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	h.waitEncoded(t, 'A', 500*time.Millisecond)
	if err := h.session.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	second, err := h.session.GenerateReply(context.Background(), "lagi")
	if err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	waitHandle(t, first)
	waitHandle(t, second)
	if got := h.enc.encoded('B', time.Time{}); got != 2*time.Second {
		t.Fatalf("second reply encoded %v of 2s", got)
	}
}
