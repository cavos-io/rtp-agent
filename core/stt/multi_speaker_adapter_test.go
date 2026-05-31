package stt

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/model"
)

func TestPrimarySpeakerDetectorFormatsPrimaryAndBackgroundText(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		false,
		"primary {speaker_id}: {text}",
		"background {speaker_id}: {text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	detector.primarySpeaker = "speaker-a"

	primary := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventInterimTranscript,
		Alternatives: []SpeechData{{
			Text:      "hello",
			SpeakerID: "speaker-a",
		}},
	})
	if got := primary.Alternatives[0].Text; got != "primary speaker-a: hello" {
		t.Fatalf("primary text = %q, want formatted primary text", got)
	}
	if primary.Alternatives[0].IsPrimarySpeaker == nil || !*primary.Alternatives[0].IsPrimarySpeaker {
		t.Fatal("primary IsPrimarySpeaker was not set to true")
	}

	background := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventInterimTranscript,
		Alternatives: []SpeechData{{
			Text:      "aside",
			SpeakerID: "speaker-b",
		}},
	})
	if got := background.Alternatives[0].Text; got != "background speaker-b: aside" {
		t.Fatalf("background text = %q, want formatted background text", got)
	}
	if background.Alternatives[0].IsPrimarySpeaker == nil || *background.Alternatives[0].IsPrimarySpeaker {
		t.Fatal("background IsPrimarySpeaker was not set to false")
	}
}

func TestPrimarySpeakerDetectorSuppressesFormattedBackgroundText(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		true,
		"{text}",
		"background {speaker_id}: {text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	detector.primarySpeaker = "speaker-a"

	event := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{
			Text:      "aside",
			SpeakerID: "speaker-b",
		}},
	})
	if event != nil {
		t.Fatalf("background event = %#v, want suppressed nil event", event)
	}
}

func TestPrimarySpeakerDetectorPushAudioInitializesByteStream(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		false,
		"{text}",
		"{text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	frame := &model.AudioFrame{
		Data:              make([]byte, 1600*2),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	done := make(chan struct{})

	go func() {
		detector.pushAudio(frame)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("pushAudio did not return, want initialized byte stream to emit frame")
	}
	if detector.bstream == nil {
		t.Fatal("bstream = nil, want initialized byte stream")
	}
	if detector.bstream.SamplesPerChannel == 0 {
		t.Fatal("bstream SamplesPerChannel = 0, want frame-sized stream")
	}
	if detector.pushedDuration == 0 {
		t.Fatal("pushedDuration = 0, want emitted audio duration")
	}
}

func TestMultiSpeakerAdapterWrapperReturnsEOFWhenInnerCompletes(t *testing.T) {
	wrapper := &multiSpeakerAdapterWrapper{
		inner:   &fakeMultiSpeakerStream{nextErr: io.EOF},
		ctx:     context.Background(),
		eventCh: make(chan *SpeechEvent, 1),
		errCh:   make(chan error, 1),
		audioCh: make(chan *model.AudioFrame, 1),
	}
	go wrapper.run()

	_, err := wrapper.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestMultiSpeakerAdapterWrapperPropagatesInnerError(t *testing.T) {
	innerErr := errors.New("inner stream failed")
	wrapper := &multiSpeakerAdapterWrapper{
		inner:   &fakeMultiSpeakerStream{nextErr: innerErr},
		ctx:     context.Background(),
		eventCh: make(chan *SpeechEvent, 1),
		errCh:   make(chan error, 1),
		audioCh: make(chan *model.AudioFrame, 1),
	}
	go wrapper.run()

	_, err := wrapper.Next()
	if !errors.Is(err, innerErr) {
		t.Fatalf("Next error = %v, want inner error", err)
	}
}

type fakeMultiSpeakerStream struct {
	nextErr error
}

func (f *fakeMultiSpeakerStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (f *fakeMultiSpeakerStream) Flush() error {
	return nil
}

func (f *fakeMultiSpeakerStream) Close() error {
	return nil
}

func (f *fakeMultiSpeakerStream) Next() (*SpeechEvent, error) {
	return nil, f.nextErr
}
