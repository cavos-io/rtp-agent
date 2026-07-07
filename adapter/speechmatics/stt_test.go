package speechmatics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestSpeechmaticsTranscriptEventPreservesWordTimings(t *testing.T) {
	resp := smResponse{
		Message: "AddTranscript",
		Results: []struct {
			Alternatives []struct {
				Content    string  `json:"content"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
			Type      string  `json:"type"`
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		}{
			{
				Type:      "word",
				StartTime: 0.1,
				EndTime:   0.3,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
				}{{Content: "hello", Confidence: 0.92}},
			},
			{
				Type:      "punctuation",
				StartTime: 0.3,
				EndTime:   0.3,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
				}{{Content: ",", Confidence: 1.0}},
			},
			{
				Type:      "word",
				StartTime: 0.4,
				EndTime:   0.8,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
				}{{Content: "world", Confidence: 0.88}},
			},
		},
	}

	event := speechmaticsTranscriptEvent(resp)
	if event == nil {
		t.Fatal("speechmaticsTranscriptEvent returned nil")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event.Type = %s, want final_transcript", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "hello, world" {
		t.Fatalf("text = %q, want punctuation-formatted transcript", got)
	}
	words := event.Alternatives[0].Words
	if len(words) != 2 {
		t.Fatalf("words = %#v, want two timed words", words)
	}
	if words[0].Text != "hello" || words[0].StartTime != 0.1 || words[0].EndTime != 0.3 || words[0].Confidence != 0.92 {
		t.Fatalf("first word = %#v, want preserved word timing", words[0])
	}
	if words[1].Text != "world" || words[1].StartTime != 0.4 || words[1].EndTime != 0.8 || words[1].Confidence != 0.88 {
		t.Fatalf("second word = %#v, want preserved word timing", words[1])
	}
}

func TestSpeechmaticsSegmentEventsMatchReference(t *testing.T) {
	tests := []struct {
		message string
		want    stt.SpeechEventType
	}{
		{message: "AddPartialSegment", want: stt.SpeechEventInterimTranscript},
		{message: "AddSegment", want: stt.SpeechEventFinalTranscript},
	}

	for _, tt := range tests {
		var resp smResponse
		body := strings.ReplaceAll(`{
			"message":"MESSAGE",
			"segments":[{
				"text":"hello",
				"language":"en",
				"speaker_id":"S1",
				"metadata":{"start_time":0.1,"end_time":0.4}
			}]
		}`, "MESSAGE", tt.message)
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("unmarshal segment response: %v", err)
		}

		events := speechmaticsEvents(resp, nil)
		if len(events) != 1 {
			t.Fatalf("%s events = %d, want one transcript", tt.message, len(events))
		}
		event := events[0]
		if event.Type != tt.want || len(event.Alternatives) != 1 {
			t.Fatalf("%s event = %#v, want one %s transcript", tt.message, event, tt.want)
		}
		got := event.Alternatives[0]
		if got.Text != "hello" || got.Language != "en" || got.SpeakerID != "S1" || got.StartTime != 0.1 || got.EndTime != 0.4 {
			t.Fatalf("%s alternative = %+v, want reference segment text/language/speaker/timing", tt.message, got)
		}
	}
}

func TestSpeechmaticsSegmentEventsApplyReferenceStartTimeOffset(t *testing.T) {
	stream := &speechmaticsSTTStream{}
	timing, ok := interface{}(stream).(stt.StreamTiming)
	if !ok {
		t.Fatal("speechmatics stream does not implement stt.StreamTiming")
	}
	timing.SetStartTimeOffset(4.5)

	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"aligned",
			"language":"en",
			"speaker_id":"S1",
			"metadata":{"start_time":0.2,"end_time":0.6}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, stream.state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one final transcript", len(events))
	}
	alt := events[0].Alternatives[0]
	if alt.StartTime < 4.699 || alt.StartTime > 4.701 || alt.EndTime < 5.099 || alt.EndTime > 5.101 {
		t.Fatalf("segment timing = %v-%v, want start_time_offset applied", alt.StartTime, alt.EndTime)
	}
}

func TestSpeechmaticsSegmentEventsApplyReferenceDefaults(t *testing.T) {
	state := &speechmaticsStreamState{language: "de"}
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"hallo",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want one transcript", events)
	}
	alt := events[0].Alternatives[0]
	if alt.Text != "hallo" || alt.Language != "de" || alt.SpeakerID != "UU" {
		t.Fatalf("alternative = %+v, want reference language and speaker defaults", alt)
	}
}

func TestSpeechmaticsSegmentEventsApplyReferenceSpeakerFormats(t *testing.T) {
	state := &speechmaticsStreamState{
		speakerActiveFormat:  "@{speaker_id}: {text}",
		speakerPassiveFormat: "@{speaker_id} [background]: {text}",
	}
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"active words",
			"language":"en",
			"speaker_id":"S1",
			"is_active":true,
			"metadata":{"start_time":0.1,"end_time":0.4}
		},{
			"text":"passive words",
			"language":"en",
			"speaker_id":"S2",
			"is_active":false,
			"metadata":{"start_time":0.5,"end_time":0.8}
		},{
			"text":"default active",
			"language":"en",
			"speaker_id":"S3",
			"metadata":{"start_time":0.9,"end_time":1.2}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 3 {
		t.Fatalf("events = %d, want three formatted transcripts", len(events))
	}
	want := []string{
		"@S1: active words",
		"@S2 [background]: passive words",
		"@S3: default active",
	}
	for i, event := range events {
		if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
			t.Fatalf("event[%d] = %#v, want final transcript", i, event)
		}
		if got := event.Alternatives[0].Text; got != want[i] {
			t.Fatalf("event[%d] text = %q, want %q", i, got, want[i])
		}
	}
}

func TestSpeechmaticsSegmentEventsFilterReferenceSpeakers(t *testing.T) {
	tests := []struct {
		name  string
		state *speechmaticsStreamState
		want  string
	}{
		{
			name: "ignored speaker",
			state: &speechmaticsStreamState{
				ignoreSpeakers: []string{"noise"},
			},
			want: "agent words",
		},
		{
			name: "focus ignore mode",
			state: &speechmaticsStreamState{
				focusSpeakers: []string{"agent"},
				focusMode:     "ignore",
			},
			want: "agent words",
		},
		{
			name: "wrapped system speaker",
			state: &speechmaticsStreamState{
				ignoreSpeakers: []string{"noise"},
			},
			want: "agent words",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp smResponse
			if err := json.Unmarshal([]byte(`{
				"message":"AddSegment",
				"segments":[{
					"text":"agent words",
					"language":"en",
					"speaker_id":"agent",
					"metadata":{"start_time":0.1,"end_time":0.4}
				},{
					"text":"noise words",
					"language":"en",
					"speaker_id":"noise",
					"metadata":{"start_time":0.5,"end_time":0.8}
				},{
					"text":"system words",
					"language":"en",
					"speaker_id":"__ASSISTANT__",
					"metadata":{"start_time":0.9,"end_time":1.2}
				}]
			}`), &resp); err != nil {
				t.Fatalf("unmarshal segment response: %v", err)
			}

			events := speechmaticsEvents(resp, tt.state)
			if len(events) != 1 || len(events[0].Alternatives) != 1 {
				t.Fatalf("events = %#v, want one filtered transcript", events)
			}
			if got := events[0].Alternatives[0].Text; got != tt.want {
				t.Fatalf("text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpeechmaticsSegmentEventsSuppressReferencePartialsWhenDisabled(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: false}
	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialSegment",
		"segments":[{
			"text":"partial words",
			"language":"en",
			"speaker_id":"agent",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal partial response: %v", err)
	}
	if events := speechmaticsEvents(partial, state); len(events) != 0 {
		t.Fatalf("partial events = %#v, want none when include_partials is false", events)
	}

	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"final words",
			"language":"en",
			"speaker_id":"agent",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}
	events := speechmaticsEvents(final, state)
	if len(events) != 1 || events[0].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("final events = %#v, want final transcript despite include_partials=false", events)
	}
}

func TestSpeechmaticsTurnBoundaryEventsMatchReference(t *testing.T) {
	state := &speechmaticsStreamState{speechDuration: 1.25}

	startEvents := speechmaticsEvents(smResponse{Message: "StartOfTurn"}, state)
	if len(startEvents) != 1 {
		t.Fatalf("start events = %d, want 1", len(startEvents))
	}
	if startEvents[0].Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("start event type = %s, want start_of_speech", startEvents[0].Type)
	}

	endEvents := speechmaticsEvents(smResponse{Message: "EndOfTurn"}, state)
	if len(endEvents) != 2 {
		t.Fatalf("end events = %d, want end_of_speech and recognition_usage", len(endEvents))
	}
	if endEvents[0].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end event type = %s, want end_of_speech", endEvents[0].Type)
	}
	usage := endEvents[1]
	if usage.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("usage event type = %s, want recognition_usage", usage.Type)
	}
	if usage.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil, want audio duration")
	}
	if usage.RecognitionUsage.AudioDuration != 1.25 {
		t.Fatalf("audio duration = %v, want 1.25", usage.RecognitionUsage.AudioDuration)
	}
	if state.speechDuration != 0 {
		t.Fatalf("speech duration after usage = %v, want reset to 0", state.speechDuration)
	}
}

func TestSpeechmaticsSTTUnexpectedNormalCloseReturnsReferenceError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read start message: %v", err)
			return
		}
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
			t.Errorf("write close: %v", err)
		}
	}))
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next event = %#v, want nil on provider close", event)
	}
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want reference provider close error", err)
	}
}

func TestSpeechmaticsSTTLogMessagesDoNotAbortReferenceStream(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read start message: %v", err)
			return
		}
		messages := []map[string]interface{}{
			{"message": "Error", "type": "telemetry", "reason": "provider diagnostic"},
			{
				"message": "AddTranscript",
				"results": []map[string]interface{}{
					{
						"type":       "word",
						"start_time": 0.0,
						"end_time":   0.2,
						"alternatives": []map[string]interface{}{
							{"content": "hello", "confidence": 0.9},
						},
					},
				},
			},
			{"message": "EndOfTranscript"},
		}
		for _, message := range messages {
			if err := conn.WriteJSON(message); err != nil {
				t.Errorf("write message: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want transcript after provider log message", err)
	}
	if event == nil || event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("Next event = %#v, want final transcript", event)
	}
	if got := event.Alternatives[0].Text; got != "hello" {
		t.Fatalf("transcript = %q, want hello", got)
	}
}

func TestSpeechmaticsSTTEndOfTranscriptRemovesActiveStream(t *testing.T) {
	upgrader := websocket.Upgrader{}
	clientClosed := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read start message: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]interface{}{"message": "EndOfTranscript"}); err != nil {
			t.Errorf("write EndOfTranscript: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, _, err = conn.ReadMessage()
		clientClosed <- err
	}))
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}

	event, err := stream.Next()
	if event != nil || err != io.EOF {
		t.Fatalf("Next after EndOfTranscript = (%#v, %v), want EOF", event, err)
	}
	if active := provider.activeStreams(); len(active) != 0 {
		t.Fatalf("active streams after EndOfTranscript = %d, want 0", len(active))
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01}}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after EndOfTranscript = %v, want io.ErrClosedPipe", err)
	}
	select {
	case err := <-clientClosed:
		if err == nil {
			t.Fatal("server read after EndOfTranscript succeeded, want client transport close")
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatalf("server read after EndOfTranscript timed out, want client transport close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe client transport close after EndOfTranscript")
	}
}

func TestSpeechmaticsSTTCloseStopsBlockedTranscriptEnqueue(t *testing.T) {
	upgrader := websocket.Upgrader{}
	releaseWrites := make(chan struct{})
	serverSent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		<-releaseWrites
		messages := []string{"first", "stale"}
		for _, text := range messages {
			if err := conn.WriteJSON(map[string]interface{}{
				"message": "AddSegment",
				"segments": []map[string]interface{}{{
					"text":       text,
					"language":   "en",
					"speaker_id": "agent",
					"metadata":   map[string]interface{}{"start_time": 0.1, "end_time": 0.2},
				}},
			}); err != nil {
				return
			}
		}
		close(serverSent)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	smStream := stream.(*speechmaticsSTTStream)
	smStream.events = make(chan *stt.SpeechEvent, 1)
	close(releaseWrites)

	select {
	case <-serverSent:
	case <-time.After(time.Second):
		t.Fatal("server did not send queued Speechmatics transcript burst")
	}

	deadline := time.After(time.Second)
	for len(smStream.events) == 0 {
		select {
		case <-deadline:
			t.Fatal("first Speechmatics transcript did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	first := <-smStream.events
	if got := first.Alternatives[0].Text; got != "first" {
		t.Fatalf("first queued transcript = %q, want first", got)
	}
	select {
	case stale, ok := <-smStream.events:
		if ok {
			t.Fatalf("stale transcript after Close = %+v, want read loop stopped", stale)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("events channel did not close after local Close stopped blocked enqueue")
	}
}

func TestSpeechmaticsSTTNextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	for range 64 {
		stream := &speechmaticsSTTStream{
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
		}
		stream.events <- &stt.SpeechEvent{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{
				{Text: "hello"},
			},
		}
		stream.errCh <- errors.New("provider close")

		event, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error = %v, want queued transcript before stream error", err)
		}
		if event == nil || event.Type != stt.SpeechEventFinalTranscript {
			t.Fatalf("Next event = %#v, want queued final transcript", event)
		}
		if got := event.Alternatives[0].Text; got != "hello" {
			t.Fatalf("transcript = %q, want hello", got)
		}
	}
}

func TestSpeechmaticsPushFrameTracksReferenceSpeechDuration(t *testing.T) {
	stream := &speechmaticsSTTStream{
		writeBinary: func([]byte) error {
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if stream.state.speechDuration < 0.099 || stream.state.speechDuration > 0.101 {
		t.Fatalf("speech duration = %v, want 0.1", stream.state.speechDuration)
	}
}

func TestSpeechmaticsPushFrameChunksAndFlushesReferenceAudio(t *testing.T) {
	var writes [][]byte
	stream := &speechmaticsSTTStream{
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}
	audioData := make([]byte, 4000)
	for i := range audioData {
		audioData[i] = byte(i)
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("binary writes after PushFrame = %d, want one 100ms chunk", len(writes))
	}
	if got := len(writes[0]); got != 3200 {
		t.Fatalf("first chunk length = %d, want 3200", got)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("binary writes after Flush = %d, want remainder chunk", len(writes))
	}
	if got := len(writes[1]); got != 800 {
		t.Fatalf("flush chunk length = %d, want 800", got)
	}
}

func TestSpeechmaticsSTTStreamRejectsReferenceSampleRateChange(t *testing.T) {
	stream := &speechmaticsSTTStream{
		writeBinary: func([]byte) error {
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("first PushFrame error = %v", err)
	}
	err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 9600),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 4800,
	})
	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("second PushFrame error = %v, want reference sample-rate consistency error", err)
	}
}

func TestSpeechmaticsSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	stream := &speechmaticsSTTStream{
		writeBinary: func([]byte) error {
			return writeErr
		},
		closeConn: func() error {
			return nil
		},
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	if err := stream.PushFrame(frame); !errors.Is(err, writeErr) {
		t.Fatalf("PushFrame write failure error = %v, want %v", err, writeErr)
	}
	if !stream.isClosed() {
		t.Fatal("stream remains open after audio write failure")
	}
	if err := stream.PushFrame(frame); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
}

func TestSpeechmaticsSTTCapabilitiesMatchReference(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	capabilities := provider.Capabilities()

	if got := stt.Provider(provider); got != "Speechmatics" {
		t.Fatalf("provider metadata = %q, want Speechmatics", got)
	}
	if got := stt.Model(provider); got != "enhanced" {
		t.Fatalf("model metadata = %q, want enhanced", got)
	}
	if !capabilities.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if !capabilities.InterimResults {
		t.Fatal("InterimResults = false, want true")
	}
	if !capabilities.Diarization {
		t.Fatal("Diarization = false, want true")
	}
	if capabilities.AlignedTranscript != "chunk" {
		t.Fatalf("AlignedTranscript = %q, want chunk", capabilities.AlignedTranscript)
	}
	if capabilities.OfflineRecognize {
		t.Fatal("OfflineRecognize = true, want false")
	}

	provider = NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTOperatingPoint("standard"))
	if got := stt.Model(provider); got != "standard" {
		t.Fatalf("configured model metadata = %q, want standard", got)
	}

	provider = NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTEnableDiarization(false))
	if capabilities := provider.Capabilities(); capabilities.Diarization {
		t.Fatal("Diarization with disabled option = true, want false")
	}
}

func TestSpeechmaticsSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want reference default 16000", got)
	}
}

func TestSpeechmaticsSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTSampleRate(48000))

	if got := provider.InputSampleRate(); got != 48000 {
		t.Fatalf("InputSampleRate = %d, want configured sample rate 48000", got)
	}
}

func TestNewSpeechmaticsSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SPEECHMATICS_API_KEY", "env-key")

	provider := NewSpeechmaticsSTT("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSpeechmaticsSTT("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSpeechmaticsSTTStreamRequiresAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("SPEECHMATICS_API_KEY", "")
	provider := NewSpeechmaticsSTT("")

	_, err := provider.Stream(context.Background(), "")

	if err == nil || !strings.Contains(err.Error(), "SPEECHMATICS_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", err)
	}
}

func TestSpeechmaticsSTTStreamRejectsInvalidReferenceEndpointingOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []SpeechmaticsSTTOption
		want string
	}{
		{
			name: "silence trigger too high",
			opts: []SpeechmaticsSTTOption{WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(2.0)},
			want: "end_of_utterance_silence_trigger must be between 0 and 2",
		},
		{
			name: "max delay below minimum",
			opts: []SpeechmaticsSTTOption{WithSpeechmaticsSTTMaxDelay(0.6)},
			want: "max_delay must be between 0.7 and 4.0",
		},
		{
			name: "eou max delay not greater than silence trigger",
			opts: []SpeechmaticsSTTOption{
				WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(0.8),
				WithSpeechmaticsSTTEndOfUtteranceMaxDelay(0.8),
			},
			want: "end_of_utterance_max_delay must be greater than end_of_utterance_silence_trigger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := append([]SpeechmaticsSTTOption{WithSpeechmaticsSTTBaseURL("ws://127.0.0.1:1")}, tt.opts...)
			provider := NewSpeechmaticsSTT("test-key", opts...)

			_, err := provider.Stream(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stream error = %v, want %q before provider dial", err, tt.want)
			}
		})
	}
}

func TestSpeechmaticsSTTProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	closed := false
	stream := &speechmaticsSTTStream{
		closeConn: func() error {
			closed = true
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !closed {
		t.Fatal("stream closed = false after provider Close")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after provider Close = %v, want io.ErrClosedPipe", err)
	}
}

func TestSpeechmaticsSTTFinalizeSendsReferenceForceEndOfUtterance(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	var writes []map[string]interface{}
	stream := &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			payload, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("finalize message = %#v, want JSON object", message)
			}
			writes = append(writes, payload)
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("finalize writes = %d, want one active stream write", len(writes))
	}
	if got, want := writes[0]["message"], "ForceEndOfUtterance"; got != want {
		t.Fatalf("finalize message = %#v, want %#v", got, want)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize after stream Close error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("finalize writes after stream Close = %d, want unchanged", len(writes))
	}
}

func TestSpeechmaticsSTTUpdateSpeakersUpdatesActiveStreams(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	var writes []map[string]interface{}
	stream := &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			payload, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("speaker update message = %#v, want JSON object", message)
			}
			writes = append(writes, payload)
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	provider.registerStream(stream)

	err := provider.UpdateSpeakers([]string{"agent"}, []string{"noise"}, "ignore")
	if err != nil {
		t.Fatalf("UpdateSpeakers error = %v", err)
	}

	if got := strings.Join(provider.focusSpeakers, ","); got != "agent" {
		t.Fatalf("provider focus speakers = %q, want agent", got)
	}
	if got := strings.Join(provider.ignoreSpeakers, ","); got != "noise" {
		t.Fatalf("provider ignore speakers = %q, want noise", got)
	}
	if provider.focusMode != "ignore" {
		t.Fatalf("provider focus mode = %q, want ignore", provider.focusMode)
	}
	if len(writes) != 1 {
		t.Fatalf("speaker update writes = %d, want one active stream write", len(writes))
	}
	if got, want := writes[0]["message"], "SetRecognitionConfig"; got != want {
		t.Fatalf("speaker update message = %#v, want %#v", got, want)
	}
	config, ok := writes[0]["transcription_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("transcription_config = %#v, want object", writes[0]["transcription_config"])
	}
	speakerConfig, ok := config["speaker_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("speaker_config = %#v, want object", config["speaker_config"])
	}
	if got := strings.Join(speakerConfig["focus_speakers"].([]string), ","); got != "agent" {
		t.Fatalf("focus_speakers = %q, want agent", got)
	}
	if got := strings.Join(speakerConfig["ignore_speakers"].([]string), ","); got != "noise" {
		t.Fatalf("ignore_speakers = %q, want noise", got)
	}
	if got, want := speakerConfig["focus_mode"], "ignore"; got != want {
		t.Fatalf("focus_mode = %#v, want %#v", got, want)
	}
	if stream.state == nil {
		t.Fatal("stream state = nil, want updated local speaker filter")
	}
	if got := strings.Join(stream.state.focusSpeakers, ","); got != "agent" {
		t.Fatalf("stream focus speakers = %q, want agent", got)
	}
	if got := strings.Join(stream.state.ignoreSpeakers, ","); got != "noise" {
		t.Fatalf("stream ignore speakers = %q, want noise", got)
	}
	if stream.state.focusMode != "ignore" {
		t.Fatalf("stream focus mode = %q, want ignore", stream.state.focusMode)
	}
}

func TestSpeechmaticsSTTUpdateSpeakersRequiresDiarization(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTEnableDiarization(false))
	stream := &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			t.Fatalf("speaker update write = %#v, want none when diarization disabled", message)
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	provider.registerStream(stream)

	err := provider.UpdateSpeakers([]string{"agent"}, nil, "retain")
	if err == nil || !strings.Contains(err.Error(), "diarization is not enabled") {
		t.Fatalf("UpdateSpeakers error = %v, want diarization disabled error", err)
	}
}

func TestSpeechmaticsSTTGetSpeakerIDsRequestsReferenceSpeakersResult(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	var writes []map[string]interface{}
	var stream *speechmaticsSTTStream
	stream = &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			payload, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("speaker request message = %#v, want JSON object", message)
			}
			writes = append(writes, payload)
			stream.recordSpeakerResult([]SpeechmaticsSpeakerIdentifier{
				{Label: "agent", SpeakerID: "spk-1"},
			})
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	provider.registerStream(stream)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	speakers, err := provider.GetSpeakerIDs(ctx)
	if err != nil {
		t.Fatalf("GetSpeakerIDs error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("speaker request writes = %d, want one active stream write", len(writes))
	}
	if got, want := writes[0]["message"], "GetSpeakers"; got != want {
		t.Fatalf("speaker request message = %#v, want %#v", got, want)
	}
	if len(speakers) != 1 || speakers[0].Label != "agent" || speakers[0].SpeakerID != "spk-1" {
		t.Fatalf("speakers = %#v, want agent speaker id", speakers)
	}
}

func TestSpeechmaticsSTTGetSpeakerIDsSkipsDisabledDiarization(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTEnableDiarization(false))
	stream := &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			t.Fatalf("speaker request write = %#v, want none when diarization disabled", message)
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	provider.registerStream(stream)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	speakers, err := provider.GetSpeakerIDs(ctx)
	if err != nil {
		t.Fatalf("GetSpeakerIDs error = %v, want nil for disabled diarization", err)
	}
	if len(speakers) != 0 {
		t.Fatalf("speakers = %#v, want empty result for disabled diarization", speakers)
	}
}

func TestSpeechmaticsSTTGetSpeakerIDsTimesOutLikeReference(t *testing.T) {
	oldTimeout := speechmaticsSpeakerResultTimeout
	speechmaticsSpeakerResultTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsSpeakerResultTimeout = oldTimeout })

	writes := 0
	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			writes++
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	provider.registerStream(stream)

	start := time.Now()
	speakers, err := provider.GetSpeakerIDs(context.Background())
	if err != nil {
		t.Fatalf("GetSpeakerIDs error = %v, want nil timeout result", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("GetSpeakerIDs elapsed = %s, want bounded reference timeout", elapsed)
	}
	if writes != 1 {
		t.Fatalf("GetSpeakers writes = %d, want one request before timeout", writes)
	}
	if len(speakers) != 0 {
		t.Fatalf("speakers = %#v, want empty timeout result", speakers)
	}
}

func TestSpeechmaticsSTTClosedStreamFinalizeReturnsEOF(t *testing.T) {
	stream := &speechmaticsSTTStream{
		writeJSON: func(interface{}) error {
			return errors.New("unexpected finalize write")
		},
		closeConn: func() error {
			return nil
		},
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	if err := stream.Finalize(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Finalize after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestSpeechmaticsSTTClosedStreamNextReturnsEOF(t *testing.T) {
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		closeConn: func() error {
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	stream.events <- &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: "stale transcript"},
		},
	}
	result := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		if event != nil {
			result <- errors.New("Next returned queued event after Close")
			return
		}
		result <- err
	}()

	select {
	case err := <-result:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next error after Close = %v, want %v", err, io.EOF)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next after Close blocked, want EOF")
	}
}

func TestSpeechmaticsSTTStreamAfterCloseIsRejected(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	oldDialer := websocket.DefaultDialer
	dials := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected speechmatics stt dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	stream, err := provider.Stream(context.Background(), "")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if dials != 0 {
		t.Fatalf("Stream after Close dialed %d times, want none", dials)
	}
}

func TestSpeechmaticsSTTStreamURLMatchesReference(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	streamURL, err := url.Parse(buildSpeechmaticsSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse default stream URL: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "eu2.rt.speechmatics.com" || streamURL.Path != "/v2" {
		t.Fatalf("stream URL = %q, want reference default realtime endpoint", streamURL.String())
	}

	provider = NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("wss://speechmatics.example/v2/"))
	streamURL, err = url.Parse(buildSpeechmaticsSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse custom stream URL: %v", err)
	}
	if streamURL.String() != "wss://speechmatics.example/v2" {
		t.Fatalf("stream URL = %q, want trimmed custom base URL", streamURL.String())
	}
}

func TestSpeechmaticsSTTUsesEnvironmentRealtimeURL(t *testing.T) {
	t.Setenv("SPEECHMATICS_RT_URL", "wss://speechmatics.env/v2/")

	provider := NewSpeechmaticsSTT("test-key")

	if got, want := buildSpeechmaticsSTTStreamURL(provider), "wss://speechmatics.env/v2"; got != want {
		t.Fatalf("stream URL = %q, want environment realtime URL %q", got, want)
	}

	provider = NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("wss://speechmatics.explicit/v2/"))
	if got, want := buildSpeechmaticsSTTStreamURL(provider), "wss://speechmatics.explicit/v2"; got != want {
		t.Fatalf("stream URL = %q, want explicit realtime URL %q", got, want)
	}
}

func TestSpeechmaticsSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("Recognize error = %q, want not implemented", err.Error())
	}
}

func TestSpeechmaticsSTTStartMessageUsesReferenceOptions(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTLanguage("de"),
		WithSpeechmaticsSTTSampleRate(48000),
		WithSpeechmaticsSTTAudioEncoding("pcm_f32le"),
		WithSpeechmaticsSTTDomain("finance"),
		WithSpeechmaticsSTTOutputLocale("de-DE"),
		WithSpeechmaticsSTTIncludePartials(false),
		WithSpeechmaticsSTTEnableDiarization(false),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	if message["message"] != "StartRecognition" {
		t.Fatalf("message = %#v, want StartRecognition", message["message"])
	}
	audioFormat := message["audio_format"].(map[string]interface{})
	if audioFormat["sample_rate"] != 48000 {
		t.Fatalf("sample_rate = %#v, want 48000", audioFormat["sample_rate"])
	}
	if audioFormat["encoding"] != "pcm_f32le" {
		t.Fatalf("encoding = %#v, want pcm_f32le", audioFormat["encoding"])
	}
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "language", "de")
	assertSpeechmaticsConfig(t, config, "domain", "finance")
	assertSpeechmaticsConfig(t, config, "output_locale", "de-DE")
	assertSpeechmaticsConfig(t, config, "enable_partials", true)
	assertSpeechmaticsConfig(t, config, "diarization", "none")

	message = buildSpeechmaticsSTTStartMessage(provider, "fr")
	config = message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "language", "fr")

	if _, err := json.Marshal(message); err != nil {
		t.Fatalf("marshal start message: %v", err)
	}
}

func TestSpeechmaticsSTTStartMessageEnablesReferenceDiarizationByDefault(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "diarization", "speaker")
}

func TestSpeechmaticsSTTStartMessageUsesReferencePresetDefaults(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "operating_point", "enhanced")
	assertSpeechmaticsConfig(t, config, "max_delay", float64(2.0))
	assertSpeechmaticsConfig(t, config, "max_delay_mode", "flexible")
}

func TestSpeechmaticsSTTStartMessageUsesVocabularyAndSpeakerOptions(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdditionalVocab([]SpeechmaticsAdditionalVocabEntry{
			{Content: "LiveKit", SoundsLike: []string{"live kit"}},
			{Content: "Cavos"},
		}),
		WithSpeechmaticsSTTSpeakerFocus([]string{"agent"}, []string{"customer"}, "ignore"),
		WithSpeechmaticsSTTKnownSpeakers([]SpeechmaticsSpeakerIdentifier{
			{Label: "agent", SpeakerID: "spk-1"},
		}),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})

	vocab := config["additional_vocab"].([]SpeechmaticsAdditionalVocabEntry)
	if len(vocab) != 2 || vocab[0].Content != "LiveKit" || vocab[0].SoundsLike[0] != "live kit" {
		t.Fatalf("additional_vocab = %#v, want LiveKit sounds-like entry", vocab)
	}
	speakerConfig := config["speaker_config"].(map[string]interface{})
	if got := speakerConfig["focus_speakers"].([]string); len(got) != 1 || got[0] != "agent" {
		t.Fatalf("focus_speakers = %#v, want agent", got)
	}
	if got := speakerConfig["ignore_speakers"].([]string); len(got) != 1 || got[0] != "customer" {
		t.Fatalf("ignore_speakers = %#v, want customer", got)
	}
	if speakerConfig["focus_mode"] != "ignore" {
		t.Fatalf("focus_mode = %#v, want ignore", speakerConfig["focus_mode"])
	}
	diarizationConfig, ok := config["speaker_diarization_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("speaker_diarization_config = %#v, want object", config["speaker_diarization_config"])
	}
	knownSpeakers := diarizationConfig["speakers"].([]SpeechmaticsSpeakerIdentifier)
	if len(knownSpeakers) != 1 || knownSpeakers[0].Label != "agent" || knownSpeakers[0].SpeakerID != "spk-1" {
		t.Fatalf("speaker_diarization_config.speakers = %#v, want agent speaker id", knownSpeakers)
	}
	if _, ok := config["known_speakers"]; ok {
		t.Fatalf("known_speakers sent at top level in %#v", config)
	}
}

func TestSpeechmaticsSTTStartMessageUsesAdvancedReferenceOptions(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTOperatingPoint("enhanced"),
		WithSpeechmaticsSTTMaxDelay(1.2),
		WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(0.6),
		WithSpeechmaticsSTTEndOfUtteranceMaxDelay(1.8),
		WithSpeechmaticsSTTPunctuationOverrides(map[string]interface{}{"permitted_marks": []string{".", "?"}}),
		WithSpeechmaticsSTTSpeakerSensitivity(0.7),
		WithSpeechmaticsSTTMaxSpeakers(4),
		WithSpeechmaticsSTTPreferCurrentSpeaker(true),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "operating_point", "enhanced")
	assertSpeechmaticsConfig(t, config, "max_delay", float64(1.2))
	diarizationConfig, ok := config["speaker_diarization_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("speaker_diarization_config = %#v, want object", config["speaker_diarization_config"])
	}
	assertSpeechmaticsConfig(t, diarizationConfig, "speaker_sensitivity", float64(0.7))
	assertSpeechmaticsConfig(t, diarizationConfig, "max_speakers", 4)
	assertSpeechmaticsConfig(t, diarizationConfig, "prefer_current_speaker", true)
	if _, ok := config["speaker_sensitivity"]; ok {
		t.Fatalf("speaker_sensitivity sent at top level in %#v", config)
	}
	if _, ok := config["max_speakers"]; ok {
		t.Fatalf("max_speakers sent at top level in %#v", config)
	}
	if _, ok := config["prefer_current_speaker"]; ok {
		t.Fatalf("prefer_current_speaker sent at top level in %#v", config)
	}
	overrides := config["punctuation_overrides"].(map[string]interface{})
	marks := overrides["permitted_marks"].([]string)
	if len(marks) != 2 || marks[0] != "." || marks[1] != "?" {
		t.Fatalf("punctuation_overrides = %#v, want permitted marks", overrides)
	}
}

func TestSpeechmaticsSTTStartMessageUsesReferenceConversationEndpointingConfig(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTFixedTurnDetection(),
		WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(0.6),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	conversationConfig, ok := config["conversation_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("conversation_config = %#v, want map", config["conversation_config"])
	}
	assertSpeechmaticsConfig(t, conversationConfig, "end_of_utterance_silence_trigger", float64(0.6))
	if _, ok := config["end_of_utterance_silence_trigger"]; ok {
		t.Fatalf("end_of_utterance_silence_trigger sent at top level in %#v", config)
	}
}

func TestSpeechmaticsSTTExternalTurnDetectionOmitsReferenceConversationConfig(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(0.6),
		WithSpeechmaticsSTTEndOfUtteranceMaxDelay(1.8),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	if _, ok := config["conversation_config"]; ok {
		t.Fatalf("conversation_config = %#v, want omitted for reference external turn detection", config["conversation_config"])
	}
}

func TestSpeechmaticsSTTStartMessageUsesReferenceFixedTurnDetectionMode(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTFixedTurnDetection(),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	conversationConfig, ok := config["conversation_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("conversation_config = %#v, want map for fixed turn detection", config["conversation_config"])
	}
	assertSpeechmaticsConfig(t, conversationConfig, "end_of_utterance_silence_trigger", float64(0.5))
}

func assertSpeechmaticsConfig(t *testing.T, config map[string]interface{}, key string, want interface{}) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %#v in %#v", key, got, want, config)
	}
}
