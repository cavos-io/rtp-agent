package beta

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

// Reference: refs/agents/livekit-plugins/livekit-plugins-google/livekit/plugins/google/beta/gemini_stt.py
// and refs/agents/livekit-agents/livekit/agents/stt/stt.py at 31c6026df14eebb71975c16ca6a422ce68644b71.
func TestGeminiReferenceLifecycle(t *testing.T) {
	received := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Error("missing API key")
		}
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var setup map[string]any
		if c.ReadJSON(&setup) != nil {
			return
		}
		received <- setup
		_ = c.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		for {
			var msg map[string]any
			if c.ReadJSON(&msg) != nil {
				return
			}
			received <- msg
			input := msg["realtimeInput"].(map[string]any)
			if input["audioStreamEnd"] == true {
				_ = c.WriteJSON(map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": " final tail ", "languageCode": "id-ID"}, "turnComplete": true}})
				return
			}
			_ = c.WriteJSON(map[string]any{"serverContent": map[string]any{
				"interimInputTranscription": map[string]any{"text": "partial", "languageCode": "id-ID"},
				"inputTranscription":        map[string]any{"text": "first final"},
			}})
		}
	}))
	defer server.Close()
	p, err := NewSTT("test-key", WithSTTLanguage("id-ID"), WithSTTCustomVocabulary([]string{"Cavos"}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := p.Stream(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	setup := <-received
	raw, _ := json.Marshal(setup)
	if !strings.Contains(string(raw), `"customVocabulary":["Cavos"]`) || !strings.Contains(string(raw), `"languageCodes":["id-ID"]`) || !strings.Contains(string(raw), `"responseModalities":["TEXT"]`) {
		t.Fatalf("setup: %s", raw)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.PushFrame(&model.AudioFrame{Data: []byte{0, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatal(err)
	}
	msg := <-received
	if msg["realtimeInput"].(map[string]any)["audio"] == nil {
		t.Fatalf("Flush sent a wire message: %v", msg)
	}
	for i, want := range []string{"partial", "first final"} {
		ev, err := s.Next()
		if err != nil {
			t.Fatal(err)
		}
		if ev.Alternatives[0].Text != want || ev.Alternatives[0].Language != "id-ID" {
			t.Fatalf("event %d: %+v", i, ev)
		}
		if i == 0 && ev.Type != stt.SpeechEventInterimTranscript {
			t.Fatal(ev.Type)
		}
	}
	if err := s.(stt.InputEnding).EndInput(); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Next()
	if err != nil || ev.Alternatives[0].Text != "final tail" {
		t.Fatalf("tail: %+v %v", ev, err)
	}
	if _, err = s.Next(); err != io.EOF {
		t.Fatalf("end: %v", err)
	}
	if err = s.Flush(); err == nil {
		t.Fatal("flush after end accepted")
	}
}

func TestGeminiDefaultsAndUnsupportedRecognize(t *testing.T) {
	p, err := NewSTT("key")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Model() != "gemini-3.5-transcribe-live" || p.InputSampleRate() != 16000 || p.Provider() != "google" {
		t.Fatal("defaults")
	}
	if c := p.Capabilities(); !c.Streaming || !c.InterimResults || c.OfflineRecognize || c.Diarization || c.AlignedTranscript != "" {
		t.Fatalf("%+v", c)
	}
	if _, err = p.Recognize(context.Background(), nil, ""); err == nil {
		t.Fatal("offline accepted")
	}
}

func TestGeminiSetupCancellation(t *testing.T) {
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		close(connected)
		for {
			if _, _, e = c.ReadMessage(); e != nil {
				return
			}
		}
	}))
	defer server.Close()
	p, e := NewSTT("key", WithSTTConnectOptions(llm.APIConnectOptions{Timeout: time.Second}))
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	p.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, e := p.Stream(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	<-connected
	cancel()
	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("setup cancellation blocked")
	}
}

func fakeLive(t *testing.T, handler func(*websocket.Conn)) *STT {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		var setup map[string]any
		if c.ReadJSON(&setup) != nil {
			return
		}
		if c.WriteJSON(map[string]any{"setupComplete": map[string]any{}}) != nil {
			return
		}
		handler(c)
	}))
	t.Cleanup(server.Close)
	p, e := NewSTT("key", WithSTTConnectOptions(llm.APIConnectOptions{MaxRetry: 1, Timeout: time.Second, RetryInterval: time.Millisecond}))
	if e != nil {
		t.Fatal(e)
	}
	p.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	t.Cleanup(func() { _ = p.Close() })
	return p
}
func streamForTest(t *testing.T, p *STT) stt.RecognizeStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	t.Cleanup(cancel)
	s, e := p.Stream(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestGeminiInterimOnlyAndMultipleTurns(t *testing.T) {
	p := fakeLive(t, func(c *websocket.Conn) {
		for _, sc := range []map[string]any{
			{"interimInputTranscription": map[string]any{"text": "hel"}},
			{"interimInputTranscription": map[string]any{"text": "hello", "languageCode": "id-ID"}},
			{"turnComplete": true},
			{"inputTranscription": map[string]any{"text": "next"}, "turnComplete": true},
		} {
			_ = c.WriteJSON(map[string]any{"serverContent": sc})
		}
		_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, ""))
	})
	s := streamForTest(t, p)
	for i, want := range []string{"hel", "hello", "hello", "next"} {
		ev, e := s.Next()
		if e != nil {
			t.Fatal(e)
		}
		if ev.Alternatives[0].Text != want {
			t.Fatalf("%d: %+v", i, ev)
		}
		if i >= 2 && ev.Type != stt.SpeechEventFinalTranscript {
			t.Fatalf("%d: %s", i, ev.Type)
		}
		if i == 3 && ev.Alternatives[0].Language != "en-US" {
			t.Fatal("language did not reset")
		}
	}
	if _, e := s.Next(); e != io.EOF {
		t.Fatal(e)
	}
}

func TestGeminiFinalizationTimeout(t *testing.T) {
	p := fakeLive(t, func(c *websocket.Conn) {
		for {
			if _, _, e := c.ReadMessage(); e != nil {
				return
			}
		}
	})
	p.finalizeTimeout = 20 * time.Millisecond
	s := streamForTest(t, p)
	if e := s.(stt.InputEnding).EndInput(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Next(); e != io.EOF {
		t.Fatalf("silent end: %v", e)
	}
}

func TestGeminiFinalizationWithBlockedConsumer(t *testing.T) {
	p := fakeLive(t, func(c *websocket.Conn) {
		for {
			var msg map[string]any
			if c.ReadJSON(&msg) != nil {
				return
			}
			if msg["realtimeInput"].(map[string]any)["audioStreamEnd"] == true {
				for i := 0; i < 100; i++ {
					if c.WriteJSON(map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "final"}}}) != nil {
						return
					}
				}
			}
		}
	})
	p.finalizeTimeout = 20 * time.Millisecond
	s := streamForTest(t, p).(*recognizeStream)
	if e := s.EndInput(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("finalization blocked on consumer")
	}
}

func TestGeminiRetryAndSanitizedErrors(t *testing.T) {
	var attempts atomic.Int32
	p := fakeLive(t, func(c *websocket.Conn) {
		if attempts.Add(1) == 1 {
			_ = c.WriteJSON(map[string]any{"goAway": map[string]any{"timeLeft": "0s"}})
			_ = c.WriteJSON(map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "before close"}}})
			_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1008, "session duration private transcript"))
			return
		}
		_ = c.WriteJSON(map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "after reconnect"}}})
		_ = c.WriteJSON(map[string]any{"error": map[string]any{"code": 401, "message": "secret audio private transcript"}})
	})
	s := streamForTest(t, p)
	for _, want := range []string{"before close", "after reconnect"} {
		ev, e := s.Next()
		if e != nil || ev.Alternatives[0].Text != want {
			t.Fatalf("%+v %v", ev, e)
		}
	}
	_, e := s.Next()
	var status *llm.APIStatusError
	if !errors.As(e, &status) || status.StatusCode != 401 || status.Retryable || strings.Contains(e.Error(), "private") || status.Body != nil {
		t.Fatalf("unsafe/wrong error: %v", e)
	}
	if attempts.Load() != 2 {
		t.Fatal(attempts.Load())
	}
}

func TestGeminiRetryExhaustion(t *testing.T) {
	var attempts atomic.Int32
	p := fakeLive(t, func(c *websocket.Conn) {
		attempts.Add(1)
		_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "private"))
	})
	s := streamForTest(t, p)
	if _, e := s.Next(); e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal(e)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestGeminiResamplerFlushAndInputValidation(t *testing.T) {
	inputs := make(chan map[string]any, 8)
	p := fakeLive(t, func(c *websocket.Conn) {
		for {
			var msg map[string]any
			if c.ReadJSON(&msg) != nil {
				return
			}
			inputs <- msg
		}
	})
	s := streamForTest(t, p)
	f := &model.AudioFrame{Data: []byte{1, 0}, SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1}
	if e := s.PushFrame(f); e != nil {
		t.Fatal(e)
	}
	if e := s.Flush(); e != nil {
		t.Fatal(e)
	}
	if e := s.(stt.InputEnding).EndInput(); e != nil {
		t.Fatal(e)
	}
	var pcm []byte
	for {
		select {
		case msg := <-inputs:
			in := msg["realtimeInput"].(map[string]any)
			if in["audioStreamEnd"] == true {
				if len(pcm) != 4 {
					t.Fatalf("resampler tail lost: %d", len(pcm))
				}
				if e := s.PushFrame(f); e == nil {
					t.Fatal("input after end")
				}
				return
			}
			a := in["audio"].(map[string]any)
			if a["mimeType"] != "audio/pcm;rate=16000" {
				t.Fatal(a)
			}
			b, e := base64.StdEncoding.DecodeString(a["data"].(string))
			if e != nil {
				t.Fatal(e)
			}
			pcm = append(pcm, b...)
		case <-time.After(time.Second):
			t.Fatal("audio not sent")
		}
	}
}

func TestGeminiOptionsAndAuthentication(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "google")
	t.Setenv("GEMINI_API_KEY", "gemini")
	p, e := NewSTT("", WithSTTLanguageCodes([]string{}))
	if e != nil {
		t.Fatal(e)
	}
	if p.apiKey != "google" {
		t.Fatal("key precedence")
	}
	s := &recognizeStream{owner: p, language: "id-ID"}
	if len(s.setup().Setup.InputAudioTranscription.LanguageCodes) != 0 {
		t.Fatal("empty codes must enable detection")
	}
	p, e = NewSTT("explicit", WithSTTLanguageCodes([]string{"en-US", "id-ID"}))
	if e != nil {
		t.Fatal(e)
	}
	if p.apiKey != "explicit" {
		t.Fatal("explicit key ignored")
	}
	if _, e = NewSTT("key", WithSTTCustomVocabulary(make([]string, 1001))); e == nil {
		t.Fatal("vocabulary limit")
	}
	if _, e = NewSTT("key", WithSTTSampleRate(0)); e == nil {
		t.Fatal("zero rate")
	}
}

func TestGeminiSetupTimeoutExhaustsRetryBudget(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		attempts.Add(1)
		for {
			if _, _, e = c.ReadMessage(); e != nil {
				return
			}
		}
	}))
	defer server.Close()
	p, e := NewSTT("key", WithSTTConnectOptions(llm.APIConnectOptions{MaxRetry: 1, Timeout: 20 * time.Millisecond}))
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	p.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	s, e := p.Stream(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, e = s.Next()
	if e == nil || e == io.EOF || attempts.Load() != 2 {
		t.Fatalf("setup retry budget not honored: error=%v attempts=%d", e, attempts.Load())
	}
}

func TestGeminiCancellationUnblocksInputBackpressure(t *testing.T) {
	p, err := NewSTT("key")
	if err != nil {
		t.Fatal(err)
	}
	// A stream with no consumer deterministically fills the bounded input queue.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &recognizeStream{owner: p, ctx: ctx, cancel: cancel, input: make(chan inputMessage, 1), events: make(chan *stt.SpeechEvent), done: make(chan struct{})}
	f := &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	if err = s.PushFrame(f); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() { blocked <- s.PushFrame(f) }()
	cancel()
	select {
	case err = <-blocked:
		if err == nil {
			t.Fatal("push after cancellation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked push did not cancel")
	}
}

func TestGeminiFrameValidation(t *testing.T) {
	p := fakeLive(t, func(c *websocket.Conn) {
		for {
			if _, _, e := c.ReadMessage(); e != nil {
				return
			}
		}
	})
	s := streamForTest(t, p)
	for _, f := range []*model.AudioFrame{
		{Data: []byte{0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1},
		{Data: []byte{0, 0, 0, 0}, SampleRate: 16000, NumChannels: 2, SamplesPerChannel: 1},
		{Data: []byte{0, 0}, SampleRate: 0, NumChannels: 1, SamplesPerChannel: 1},
	} {
		if e := s.PushFrame(f); e == nil {
			t.Fatalf("accepted malformed frame: %+v", f)
		}
	}
	f := &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	if e := s.PushFrame(f); e != nil {
		t.Fatal(e)
	}
	f.SampleRate = 8000
	if e := s.PushFrame(f); e == nil {
		t.Fatal("rate change accepted")
	}
	if e := p.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Stream(context.Background(), ""); e == nil {
		t.Fatal("closed provider accepted stream")
	}
}

func TestGeminiModelRateAndOptionCopies(t *testing.T) {
	codes := []string{"id-ID"}
	vocab := []string{"Cavos"}
	p, e := NewSTT("key", WithSTTModel("models/gemini-3.5-transcribe-live"), WithSTTSampleRate(8000), WithSTTLanguageCodes(codes), WithSTTCustomVocabulary(vocab))
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	codes[0] = "en-US"
	vocab[0] = "changed"
	s := &recognizeStream{owner: p, language: "en-US"}
	setup := s.setup().Setup
	if setup.Model != "models/gemini-3.5-transcribe-live" || p.InputSampleRate() != 8000 || setup.InputAudioTranscription.LanguageCodes[0] != "id-ID" || setup.InputAudioTranscription.CustomVocabulary[0] != "Cavos" {
		t.Fatalf("options: %+v", setup)
	}
}

func TestGeminiTurnSplittingDrainsRetiredFinal(t *testing.T) {
	p := fakeLive(t, func(c *websocket.Conn) {
		for {
			var msg map[string]any
			if c.ReadJSON(&msg) != nil {
				return
			}
			if msg["realtimeInput"].(map[string]any)["audioStreamEnd"] == true {
				_ = c.WriteJSON(map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "retired final"}, "turnComplete": true}})
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := stt.NewTurnSplittingSTT(p, time.Millisecond).Stream(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.PushFrame(&model.AudioFrame{Data: []byte{0x40, 0x1f}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Next()
	if err != nil || ev.Alternatives[0].Text != "retired final" {
		t.Fatalf("%+v %v", ev, err)
	}
}
