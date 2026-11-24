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

// markerEncoder records which reply each 20 ms frame written to the track belongs to.
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

// played is the audio duration of marker published in (after, +inf).
func (e *markerEncoder) played(marker byte, after time.Time) time.Duration {
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

func (h *interruptHarness) waitPlayed(t *testing.T, marker byte, atLeast time.Duration) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for h.enc.played(marker, time.Time{}) < atLeast {
		if time.Now().After(deadline) {
			t.Fatalf("reply %q published %v, want at least %v", marker, h.enc.played(marker, time.Time{}), atLeast)
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
	h.waitPlayed(t, 'A', 300*time.Millisecond)

	interruptedAt := time.Now()
	if err := h.session.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	waitHandle(t, handle)
	time.Sleep(400 * time.Millisecond)

	if late := h.enc.played('A', interruptedAt.Add(roomIOOutputMaxLead)); late > 0 {
		t.Fatalf("published %v of the interrupted reply more than %v after the interrupt", late, roomIOOutputMaxLead)
	}
}

func TestPauseStopsPublishedAudioWithinLeadBudget(t *testing.T) {
	h := newInterruptHarness(t, reproProviderChunk, reproProviderChunk, agent.AgentSessionOptions{})
	h.say(t, "A long reply")
	h.waitPlayed(t, 'A', 300*time.Millisecond)

	pausedAt := time.Now()
	h.rio.PauseAudioOutput()
	time.Sleep(700 * time.Millisecond)
	// Release the parked reply so session shutdown is not left waiting on it.
	defer func() {
		_ = h.session.Interrupt(true)
		h.rio.ResumeAudioOutput()
	}()

	if late := h.enc.played('A', pausedAt.Add(roomIOOutputMaxLead)); late > 0 {
		t.Fatalf("published %v of the paused reply more than %v after the pause", late, roomIOOutputMaxLead)
	}
}

func TestReplyAfterInterruptIsPublishedInFull(t *testing.T) {
	const replyDur = 600 * time.Millisecond
	h := newInterruptHarness(t, 200*time.Millisecond, replyDur, agent.AgentSessionOptions{})
	first := h.say(t, "A first reply")
	h.waitPlayed(t, 'A', 100*time.Millisecond)
	if err := h.session.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	second := h.say(t, "B second reply")
	waitHandle(t, first)
	waitHandle(t, second)

	if got := h.enc.played('B', time.Time{}); got != replyDur {
		t.Fatalf("second reply published %v of audio, want all %v", got, replyDur)
	}
}

// storedAfterInterrupt speaks a 4 s reply delivered in chunk-sized frames, interrupts it once
// playFor of audio is out, and returns the committed assistant text ("" when none).
func storedAfterInterrupt(t *testing.T, chunk, playFor time.Duration) (stored, full string, played time.Duration) {
	t.Helper()
	h := newInterruptHarness(t, chunk, 4*time.Second, agent.AgentSessionOptions{})
	full = "A " + strings.Repeat("kata ", 40)
	handle := h.say(t, full)
	h.waitPlayed(t, 'A', playFor)
	if err := h.session.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	waitHandle(t, handle)
	for _, item := range handle.ChatItems() {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleAssistant {
			stored = msg.TextContent()
		}
	}
	return stored, full, h.enc.played('A', time.Time{})
}

func TestInterruptedReplyStoresOnlyPublishedText(t *testing.T) {
	for name, chunk := range map[string]time.Duration{"20ms frames": 20 * time.Millisecond, "1s chunks": time.Second, "single 4s chunk": 4 * time.Second} {
		t.Run(name, func(t *testing.T) {
			stored, full, played := storedAfterInterrupt(t, chunk, time.Second)
			// ~1.2 s of a 4 s reply reached the track: the stored text must be a matching prefix.
			if stored == "" || len(stored) > len(full)/2 {
				t.Fatalf("stored %d of %d chars after publishing %v of 4s", len(stored), len(full), played)
			}
		})
	}
}
