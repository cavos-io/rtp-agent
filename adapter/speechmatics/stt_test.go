package speechmatics

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/gorilla/websocket"
)

func speechmaticsTestFloat64(value float64) *float64 {
	return &value
}

func TestSpeechmaticsTranscriptEventPreservesWordTimings(t *testing.T) {
	resp := smResponse{
		Message: "AddTranscript",
		Results: []smResult{
			{
				Type:         "word",
				StartTime:    0.1,
				EndTime:      0.3,
				Alternatives: []smAlternative{{Content: "hello", Confidence: speechmaticsTestFloat64(0.92)}},
			},
			{
				Type:         "punctuation",
				Attaches:     "previous",
				StartTime:    0.3,
				EndTime:      0.3,
				Alternatives: []smAlternative{{Content: ",", Confidence: speechmaticsTestFloat64(1.0)}},
			},
			{
				Type:         "word",
				StartTime:    0.4,
				EndTime:      0.8,
				Alternatives: []smAlternative{{Content: "world", Confidence: speechmaticsTestFloat64(0.88)}},
			},
		},
	}

	event := speechmaticsTranscriptEvent(resp, nil)
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

func TestSpeechmaticsEventsRawTranscriptDefaultsMissingTypeToReferenceWord(t *testing.T) {
	resp := smResponse{
		Message: "AddTranscript",
		Results: []smResult{
			{
				StartTime:    0.15,
				EndTime:      0.45,
				Alternatives: []smAlternative{{Content: "defaulted", Confidence: speechmaticsTestFloat64(0.91), SpeakerID: "S1", Language: "en"}},
			},
		},
	}

	event := speechmaticsTranscriptEvent(resp, nil)
	if event == nil {
		t.Fatal("speechmaticsTranscriptEvent returned nil")
	}
	words := event.Alternatives[0].Words
	if len(words) != 1 {
		t.Fatalf("words = %#v, want missing result type treated as reference word", words)
	}
	word := words[0]
	if word.Text != "defaulted" || word.StartTime != 0.15 || word.EndTime != 0.45 || word.Confidence != 0.91 || word.SpeakerID != "S1" {
		t.Fatalf("word = %#v, want reference default word timing", word)
	}
}

func TestSpeechmaticsEventsRawTranscriptDefaultsMissingConfidenceToReferenceOne(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"hello","speaker":"agent","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	event := speechmaticsTranscriptEvent(resp, &speechmaticsStreamState{language: "en"})
	if event == nil {
		t.Fatal("speechmaticsTranscriptEvent returned nil")
	}
	alt := event.Alternatives[0]
	if alt.Confidence != 1.0 {
		t.Fatalf("confidence = %v, want reference default 1.0", alt.Confidence)
	}
	if len(alt.Words) != 1 || alt.Words[0].Confidence != 1.0 {
		t.Fatalf("words = %#v, want reference default word confidence 1.0", alt.Words)
	}
}

func TestSpeechmaticsEventsRawTranscriptAcceptsReferenceStringTiming(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":"0.15",
			"end_time":"0.45",
			"alternatives":[{"content":"timed","confidence":0.91,"speaker":"S1","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	event := speechmaticsTranscriptEvent(resp, &speechmaticsStreamState{language: "en"})
	if event == nil {
		t.Fatal("speechmaticsTranscriptEvent returned nil")
	}
	alt := event.Alternatives[0]
	if alt.StartTime != 0.15 || alt.EndTime != 0.45 {
		t.Fatalf("timing = %v-%v, want reference string timing coerced to floats", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 1 || alt.Words[0].StartTime != 0.15 || alt.Words[0].EndTime != 0.45 {
		t.Fatalf("words = %#v, want reference string word timing", alt.Words)
	}
}

func TestSpeechmaticsEventsRawTranscriptAcceptsReferenceStringConfidence(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.15,
			"end_time":0.45,
			"alternatives":[{"content":"confident","confidence":"0.91","speaker":"S1","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	event := speechmaticsTranscriptEvent(resp, &speechmaticsStreamState{language: "en"})
	if event == nil {
		t.Fatal("speechmaticsTranscriptEvent returned nil")
	}
	alt := event.Alternatives[0]
	if alt.Confidence != 0.91 {
		t.Fatalf("confidence = %v, want reference string confidence coerced to float", alt.Confidence)
	}
	if len(alt.Words) != 1 || alt.Words[0].Confidence != 0.91 {
		t.Fatalf("words = %#v, want reference string word confidence", alt.Words)
	}
}

func TestSpeechmaticsEventsMapReferenceRawTranscriptFallback(t *testing.T) {
	resp := smResponse{
		Message: "AddTranscript",
		Results: []smResult{
			{
				Type:         "word",
				StartTime:    0.1,
				EndTime:      0.3,
				Alternatives: []smAlternative{{Content: "hello", Confidence: speechmaticsTestFloat64(0.92)}},
			},
			{
				Type:         "punctuation",
				Attaches:     "previous",
				StartTime:    0.3,
				EndTime:      0.3,
				Alternatives: []smAlternative{{Content: ",", Confidence: speechmaticsTestFloat64(1.0)}},
			},
			{
				Type:         "word",
				StartTime:    0.4,
				EndTime:      0.8,
				Alternatives: []smAlternative{{Content: "world", Confidence: speechmaticsTestFloat64(0.88)}},
			},
		},
	}

	events := speechmaticsEvents(resp, nil)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one raw transcript fallback", events)
	}
	event := events[0]
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

func TestSpeechmaticsEventsMapReferenceRawPartialTranscriptWithOffset(t *testing.T) {
	resp := smResponse{
		Message: "AddPartialTranscript",
		Metadata: struct {
			Transcript string  `json:"transcript"`
			StartTime  float64 `json:"start_time"`
			EndTime    float64 `json:"end_time"`
		}{
			Transcript: "partial words",
			StartTime:  0.2,
			EndTime:    0.5,
		},
	}
	state := &speechmaticsStreamState{startTimeOffset: 1.25, includePartials: true}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one raw partial transcript fallback", events)
	}
	event := events[0]
	if event.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event.Type = %s, want interim_transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %#v, want one transcript alternative", event.Alternatives)
	}
	alt := event.Alternatives[0]
	if alt.Text != "partial words" {
		t.Fatalf("text = %q, want metadata transcript", alt.Text)
	}
	if alt.StartTime != 1.45 || alt.EndTime != 1.75 {
		t.Fatalf("timing = %v-%v, want start_time_offset applied", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 0 {
		t.Fatalf("words = %#v, want none for metadata-only raw transcript", alt.Words)
	}
}

func TestSpeechmaticsEventsRawTranscriptAppliesReferenceSpeakerFiltering(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"agent","confidence":0.9,"speaker":"agent"}]
		},{
			"type":"word",
			"start_time":0.3,
			"end_time":0.5,
			"alternatives":[{"content":"noise","confidence":0.7,"speaker":"noise"}]
		},{
			"type":"word",
			"start_time":0.5,
			"end_time":0.7,
			"alternatives":[{"content":"assistant","confidence":0.6,"speaker":"__ASSISTANT__"}]
		},{
			"type":"punctuation",
			"attaches_to":"previous",
			"start_time":0.7,
			"end_time":0.7,
			"alternatives":[{"content":".","confidence":1.0,"speaker":"agent"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}
	state := &speechmaticsStreamState{
		focusSpeakers:  []string{"agent"},
		ignoreSpeakers: []string{"noise"},
		focusMode:      "ignore",
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one filtered raw transcript fallback", events)
	}
	alt := events[0].Alternatives[0]
	if alt.Text != "agent." {
		t.Fatalf("text = %q, want only reference-active speaker text", alt.Text)
	}
	if alt.SpeakerID != "agent" {
		t.Fatalf("speaker id = %q, want first emitted speaker", alt.SpeakerID)
	}
	if len(alt.Words) != 1 || alt.Words[0].Text != "agent" || alt.Words[0].SpeakerID != "agent" {
		t.Fatalf("words = %#v, want only agent word with speaker id", alt.Words)
	}
}

func TestSpeechmaticsEventsRawTranscriptAppliesReferenceLanguage(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"hola","confidence":0.9,"speaker":"agent","language":"es"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, &speechmaticsStreamState{language: "en"})
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one raw transcript event", events)
	}
	if got := events[0].Alternatives[0].Language; got != "es" {
		t.Fatalf("language = %q, want raw alternative language", got)
	}
	if got := events[0].Alternatives[0].Words[0].StartTimeOffset; got != 0 {
		t.Fatalf("word start_time_offset = %v, want default zero offset", got)
	}

	metadataOnly := smResponse{
		Message: "AddPartialTranscript",
		Metadata: struct {
			Transcript string  `json:"transcript"`
			StartTime  float64 `json:"start_time"`
			EndTime    float64 `json:"end_time"`
		}{
			Transcript: "fallback language",
			StartTime:  0.1,
			EndTime:    0.2,
		},
	}
	events = speechmaticsEvents(metadataOnly, &speechmaticsStreamState{language: "fr", includePartials: true})
	if len(events) != 1 {
		t.Fatalf("metadata-only events = %#v, want one raw transcript event", events)
	}
	if got := events[0].Alternatives[0].Language; got != "fr" {
		t.Fatalf("metadata-only language = %q, want stream language fallback", got)
	}
}

func TestSpeechmaticsEventsRawTranscriptDefaultsMissingLanguageToReferenceEnglish(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"hello","confidence":0.9,"speaker":"agent"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, &speechmaticsStreamState{language: "de"})
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one raw transcript event", events)
	}
	if got := events[0].Alternatives[0].Language; got != "en" {
		t.Fatalf("language = %q, want reference raw fragment default en", got)
	}
}

func TestSpeechmaticsEventsRawTranscriptPreservesReferenceEmptyLanguage(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"blank","confidence":0.9,"speaker":"agent","language":""}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, &speechmaticsStreamState{language: "de"})
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want one raw transcript event", events)
	}
	if got := events[0].Alternatives[0].Language; got != "" {
		t.Fatalf("language = %q, want explicit empty raw language preserved", got)
	}
}

func TestSpeechmaticsEventsRawTranscriptKeepsMissingSpeakerDuringReferenceFocusIgnore(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"unlabeled","confidence":0.9,"language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, &speechmaticsStreamState{focusSpeakers: []string{"agent"}, focusMode: "ignore"})
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want missing-speaker raw transcript retained like reference SDK", events)
	}
	alt := events[0].Alternatives[0]
	if alt.Text != "unlabeled" || alt.SpeakerID != "UU" {
		t.Fatalf("alternative = %+v, want retained default-speaker transcript", alt)
	}
}

func TestSpeechmaticsEventsRawTranscriptPreservesReferenceEmptySpeaker(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"empty","confidence":0.9,"speaker":""}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, &speechmaticsStreamState{
		focusSpeakers:        []string{"agent"},
		focusMode:            "ignore",
		speakerPassiveFormat: "@{speaker_id} [background]: {text}",
	})
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want explicit-empty-speaker raw transcript retained like reference SDK", events)
	}
	alt := events[0].Alternatives[0]
	if alt.SpeakerID != "" {
		t.Fatalf("speaker id = %q, want explicit empty speaker preserved", alt.SpeakerID)
	}
	if alt.Text != "@ [background]: empty" {
		t.Fatalf("text = %q, want reference passive format with empty speaker", alt.Text)
	}
}

func TestSpeechmaticsEventsRawTranscriptDoesNotFallbackAfterFilteredResults(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"metadata":{"transcript":"assistant noise","start_time":0.1,"end_time":0.3},
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"assistant","confidence":0.9,"speaker":"__ASSISTANT__"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	if events := speechmaticsEvents(resp, nil); len(events) != 0 {
		t.Fatalf("events = %#v, want none after reference raw result filtering", events)
	}
}

func TestSpeechmaticsEventsRawTranscriptGroupsReferenceSpeakers(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"agent","confidence":0.9,"speaker":"agent","language":"en"}]
		},{
			"type":"word",
			"start_time":0.4,
			"end_time":0.7,
			"alternatives":[{"content":"customer","confidence":0.8,"speaker":"customer","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, &speechmaticsStreamState{language: "en"})
	if len(events) != 2 {
		t.Fatalf("events = %#v, want one final transcript per adjacent speaker group", events)
	}
	if got := events[0].Alternatives[0].Text; got != "agent" {
		t.Fatalf("first text = %q, want agent", got)
	}
	if got := events[0].Alternatives[0].SpeakerID; got != "agent" {
		t.Fatalf("first speaker = %q, want agent", got)
	}
	if got := events[1].Alternatives[0].Text; got != "customer" {
		t.Fatalf("second text = %q, want customer", got)
	}
	if got := events[1].Alternatives[0].SpeakerID; got != "customer" {
		t.Fatalf("second speaker = %q, want customer", got)
	}
}

func TestSpeechmaticsEventsRawTranscriptUsesReferenceAttachSpacing(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"punctuation",
			"attaches_to":"next",
			"start_time":0.1,
			"end_time":0.1,
			"alternatives":[{"content":"¿","confidence":1.0,"speaker":"agent","language":"es"}]
		},{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"hola","confidence":0.9,"speaker":"agent","language":"es"}]
		},{
			"type":"punctuation",
			"attaches_to":"previous",
			"start_time":0.3,
			"end_time":0.3,
			"alternatives":[{"content":"?","confidence":1.0,"speaker":"agent","language":"es"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, nil)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one transcript event", events)
	}
	if got := events[0].Alternatives[0].Text; got != "¿hola?" {
		t.Fatalf("text = %q, want reference attach spacing", got)
	}
}

func TestSpeechmaticsEventsRawTranscriptUsesReferenceWordDelimiter(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.2,
			"alternatives":[{"content":"私","confidence":0.9,"speaker":"agent","language":"ja"}]
		},{
			"type":"word",
			"start_time":0.2,
			"end_time":0.4,
			"alternatives":[{"content":"です","confidence":0.9,"speaker":"agent","language":"ja"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, &speechmaticsStreamState{language: "ja", wordDelimiter: "", wordDelimiterSet: true})
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one raw transcript", events)
	}
	if got := events[0].Alternatives[0].Text; got != "私です" {
		t.Fatalf("text = %q, want reference language-pack word delimiter", got)
	}
}

func TestSpeechmaticsRecognitionStartedStoresReferenceWordDelimiter(t *testing.T) {
	stream := &speechmaticsSTTStream{
		state: &speechmaticsStreamState{},
	}
	delimiter := ""

	if keepReading := stream.handleResponse(smResponse{
		Message: "RecognitionStarted",
		LanguagePackInfo: struct {
			WordDelimiter *string `json:"word_delimiter"`
		}{WordDelimiter: &delimiter},
	}); !keepReading {
		t.Fatal("RecognitionStarted stopped read loop")
	}
	if stream.state.wordDelimiter != "" {
		t.Fatalf("word delimiter = %q, want empty reference delimiter", stream.state.wordDelimiter)
	}
	if !stream.state.wordDelimiterSet {
		t.Fatal("word delimiter set = false, want present empty reference delimiter")
	}
}

func TestSpeechmaticsEventsRawTranscriptTrimsReferenceEdgePunctuation(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"punctuation",
			"attaches_to":"previous",
			"start_time":0.0,
			"end_time":0.0,
			"alternatives":[{"content":",","confidence":1.0,"speaker":"agent","language":"en"}]
		},{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"hello","confidence":0.9,"speaker":"agent","language":"en"}]
		},{
			"type":"punctuation",
			"attaches_to":"next",
			"start_time":0.3,
			"end_time":0.3,
			"alternatives":[{"content":"(","confidence":1.0,"speaker":"agent","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, nil)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one transcript event", events)
	}
	alt := events[0].Alternatives[0]
	if alt.Text != "hello" {
		t.Fatalf("text = %q, want reference edge punctuation trimmed", alt.Text)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.3 {
		t.Fatalf("timing = %v-%v, want trimmed fragment timing", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 1 || alt.Words[0].Text != "hello" {
		t.Fatalf("words = %#v, want only middle word", alt.Words)
	}
}

func TestSpeechmaticsEventsRawTranscriptSkipsReferenceEmptyContent(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"","confidence":0.9,"speaker":"agent","language":"en"}]
		},{
			"type":"word",
			"start_time":0.4,
			"end_time":0.6,
			"alternatives":[{"content":"kept","confidence":0.8,"speaker":"agent","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw transcript: %v", err)
	}

	events := speechmaticsEvents(resp, nil)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one raw transcript event", events)
	}
	alt := events[0].Alternatives[0]
	if alt.Text != "kept" {
		t.Fatalf("text = %q, want empty raw content skipped", alt.Text)
	}
	if len(alt.Words) != 1 || alt.Words[0].Text != "kept" {
		t.Fatalf("words = %#v, want only non-empty content word", alt.Words)
	}
	if alt.StartTime != 0.4 || alt.EndTime != 0.6 {
		t.Fatalf("timing = %v-%v, want non-empty fragment timing", alt.StartTime, alt.EndTime)
	}
}

func TestSpeechmaticsEventsRawPartialRespectsReferenceIncludePartials(t *testing.T) {
	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"unstable","confidence":0.7,"speaker":"agent","language":"en"}]
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal raw partial transcript: %v", err)
	}
	state := &speechmaticsStreamState{includePartials: false, bufferRawFinals: true}
	if events := speechmaticsEvents(partial, state); len(events) != 0 {
		t.Fatalf("partial events = %#v, want none when include_partials is false", events)
	}

	final := partial
	final.Message = "AddTranscript"
	if events := speechmaticsEvents(final, state); len(events) != 0 {
		t.Fatalf("final raw transcript before following partial = %#v, want buffered final", events)
	}
	events := speechmaticsEvents(partial, state)
	if len(events) != 1 || events[0].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("final raw transcript events = %#v, want final transcript after following partial despite include_partials false", events)
	}
}

func TestSpeechmaticsEventsRawPartialSuppressesReferenceUnchangedView(t *testing.T) {
	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"same","confidence":0.7,"speaker":"agent","language":"en"}]
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal raw partial transcript: %v", err)
	}
	state := &speechmaticsStreamState{includePartials: true}

	if events := speechmaticsEvents(partial, state); len(events) != 1 {
		t.Fatalf("first partial events = %#v, want reference new partial view", events)
	}
	if events := speechmaticsEvents(partial, state); len(events) != 0 {
		t.Fatalf("second unchanged partial events = %#v, want suppressed reference unchanged view", events)
	}
}

func TestSpeechmaticsEventsRawPartialEmitsSameTextAfterReferenceTurnBoundary(t *testing.T) {
	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"same","confidence":0.7,"speaker":"agent","language":"en"}]
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal raw partial transcript: %v", err)
	}
	state := &speechmaticsStreamState{includePartials: true}

	if events := speechmaticsEvents(partial, state); len(events) != 1 {
		t.Fatalf("first partial events = %#v, want reference new partial view", events)
	}
	_ = speechmaticsEvents(smResponse{Message: "EndOfTurn"}, state)
	if events := speechmaticsEvents(partial, state); len(events) != 1 {
		t.Fatalf("same text after turn boundary = %#v, want new reference partial view", events)
	}
}

func TestSpeechmaticsEventsRawFinalWaitsForReferenceFollowingPartial(t *testing.T) {
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"done","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal raw final transcript: %v", err)
	}
	state := &speechmaticsStreamState{includePartials: true, bufferRawFinals: true}
	if events := speechmaticsEvents(final, state); len(events) != 0 {
		t.Fatalf("final events before following partial = %#v, want buffered final", events)
	}

	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialTranscript",
		"results":[{
			"type":"word",
			"start_time":0.4,
			"end_time":0.6,
			"alternatives":[{"content":"next","confidence":0.8,"speaker":"S1","language":"en"}]
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal raw partial transcript: %v", err)
	}
	events := speechmaticsEvents(partial, state)
	if len(events) != 2 {
		t.Fatalf("events after following partial = %#v, want buffered final then partial", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript || events[0].Alternatives[0].Text != "done" {
		t.Fatalf("first event = %#v, want buffered final transcript", events[0])
	}
	if events[1].Type != stt.SpeechEventInterimTranscript || events[1].Alternatives[0].Text != "next" {
		t.Fatalf("second event = %#v, want following interim transcript", events[1])
	}
}

func TestSpeechmaticsEventsRawPartialAfterFinalTrimsReferenceDuplicate(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: true, bufferRawFinals: true}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.0,
			"end_time":0.4,
			"is_eos":true,
			"alternatives":[{"content":"hello","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal raw final transcript: %v", err)
	}
	if events := speechmaticsEvents(final, state); len(events) != 0 {
		t.Fatalf("final events before following partial = %#v, want buffered final", events)
	}

	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialTranscript",
		"results":[{
			"type":"word",
			"start_time":0.0,
			"end_time":0.4,
			"alternatives":[{"content":"hello","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal raw partial transcript: %v", err)
	}

	events := speechmaticsEvents(partial, state)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want only buffered final transcript without stale duplicate partial", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %s, want final transcript", events[0].Type)
	}
	if got := events[0].Alternatives[0].Text; got != "hello" {
		t.Fatalf("final text = %q, want hello", got)
	}
}

func TestSpeechmaticsEventsMetadataPartialAfterFinalTrimsReferenceDuplicate(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: true, bufferRawFinals: true}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.0,
			"end_time":0.4,
			"is_eos":true,
			"alternatives":[{"content":"hello","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal raw final transcript: %v", err)
	}
	if events := speechmaticsEvents(final, state); len(events) != 0 {
		t.Fatalf("final events before following partial = %#v, want buffered final", events)
	}

	partial := smResponse{
		Message: "AddPartialTranscript",
		Metadata: struct {
			Transcript string  `json:"transcript"`
			StartTime  float64 `json:"start_time"`
			EndTime    float64 `json:"end_time"`
		}{
			Transcript: "hello",
			StartTime:  0.0,
			EndTime:    0.4,
		},
	}

	events := speechmaticsEvents(partial, state)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want only buffered final transcript without stale metadata partial", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %s, want final transcript", events[0].Type)
	}
}

func TestSpeechmaticsEventsZeroTimingMetadataPartialAfterFinalTrimsReferenceDuplicate(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: true, bufferRawFinals: true}
	final := smResponse{
		Message: "AddTranscript",
		Metadata: struct {
			Transcript string  `json:"transcript"`
			StartTime  float64 `json:"start_time"`
			EndTime    float64 `json:"end_time"`
		}{
			Transcript: "hello",
		},
	}
	if events := speechmaticsEvents(final, state); len(events) != 0 {
		t.Fatalf("final events before following partial = %#v, want buffered final", events)
	}

	partial := smResponse{
		Message: "AddPartialTranscript",
		Metadata: struct {
			Transcript string  `json:"transcript"`
			StartTime  float64 `json:"start_time"`
			EndTime    float64 `json:"end_time"`
		}{
			Transcript: "hello",
		},
	}

	events := speechmaticsEvents(partial, state)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want only buffered final transcript without stale zero-timing metadata partial", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript || events[0].Alternatives[0].Text != "hello" {
		t.Fatalf("event = %#v, want buffered final hello", events[0])
	}
}

func TestSpeechmaticsEventsRawFinalFlushesBeforeReferenceEndOfTurn(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: true, bufferRawFinals: true, speechDuration: 0.75}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.4,
			"alternatives":[{"content":"done","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}

	if events := speechmaticsEvents(final, state); len(events) != 0 {
		t.Fatalf("final events before turn end = %#v, want buffered until reference turn boundary", events)
	}
	events := speechmaticsEvents(smResponse{Message: "EndOfTurn"}, state)
	if len(events) != 3 {
		t.Fatalf("end events = %#v, want final transcript, end_of_speech, recognition_usage", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript || events[0].Alternatives[0].Text != "done" {
		t.Fatalf("first event = %#v, want buffered final transcript before turn end", events[0])
	}
	if events[1].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("second event = %s, want end_of_speech", events[1].Type)
	}
	if events[2].Type != stt.SpeechEventRecognitionUsage || events[2].RecognitionUsage == nil || events[2].RecognitionUsage.AudioDuration != 0.75 {
		t.Fatalf("third event = %#v, want recognition usage", events[2])
	}
	if len(state.pendingRawFinals) != 0 {
		t.Fatalf("pending finals = %#v, want flushed at turn end", state.pendingRawFinals)
	}
}

func TestSpeechmaticsEventsRawFinalFlushesBeforeReferenceStartOfTurn(t *testing.T) {
	state := &speechmaticsStreamState{language: "en", includePartials: true, bufferRawFinals: true}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.2,
			"alternatives":[{"content":"done","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}

	if events := speechmaticsEvents(final, state); len(events) != 0 {
		t.Fatalf("final events before next turn = %#v, want buffered final", events)
	}
	events := speechmaticsEvents(smResponse{Message: "StartOfTurn"}, state)
	if len(events) != 2 {
		t.Fatalf("start events = %#v, want buffered final then start_of_speech", events)
	}
	if events[0].Type != stt.SpeechEventFinalTranscript || events[0].Alternatives[0].Text != "done" {
		t.Fatalf("first event = %#v, want buffered final transcript before start boundary", events[0])
	}
	if events[1].Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("second event = %s, want start_of_speech", events[1].Type)
	}
	if len(state.pendingRawFinals) != 0 {
		t.Fatalf("pending finals = %#v, want flushed at next turn start", state.pendingRawFinals)
	}
}

func TestSpeechmaticsEventsRawTranscriptSplitsReferenceEOSSentences(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"is_eos":false,
			"alternatives":[{"content":"hello","confidence":0.9,"speaker":"S1","language":"en"}]
		},{
			"type":"punctuation",
			"attaches_to":"previous",
			"start_time":0.3,
			"end_time":0.3,
			"is_eos":true,
			"alternatives":[{"content":".","confidence":1.0,"speaker":"S1","language":"en"}]
		},{
			"type":"word",
			"start_time":0.4,
			"end_time":0.6,
			"is_eos":false,
			"alternatives":[{"content":"next","confidence":0.8,"speaker":"S1","language":"en"}]
		},{
			"type":"punctuation",
			"attaches_to":"previous",
			"start_time":0.6,
			"end_time":0.6,
			"is_eos":true,
			"alternatives":[{"content":".","confidence":1.0,"speaker":"S1","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw eos transcript: %v", err)
	}

	events := speechmaticsEvents(resp, nil)
	if len(events) != 2 {
		t.Fatalf("events = %#v, want one final transcript per reference EOS sentence", events)
	}
	if got := events[0].Alternatives[0].Text; got != "hello." {
		t.Fatalf("first text = %q, want first EOS sentence", got)
	}
	if got := events[1].Alternatives[0].Text; got != "next." {
		t.Fatalf("second text = %q, want second EOS sentence", got)
	}
	if events[0].Alternatives[0].StartTime != 0.1 || events[0].Alternatives[0].EndTime != 0.3 {
		t.Fatalf("first timing = %v-%v, want first sentence timing", events[0].Alternatives[0].StartTime, events[0].Alternatives[0].EndTime)
	}
	if events[1].Alternatives[0].StartTime != 0.4 || events[1].Alternatives[0].EndTime != 0.6 {
		t.Fatalf("second timing = %v-%v, want second sentence timing", events[1].Alternatives[0].StartTime, events[1].Alternatives[0].EndTime)
	}
}

func TestSpeechmaticsEventsRawTranscriptAppliesReferencePassiveSpeakerFormat(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.3,
			"alternatives":[{"content":"background","confidence":0.9,"speaker":"customer","language":"en"}]
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal raw speaker transcript: %v", err)
	}
	state := &speechmaticsStreamState{
		focusSpeakers:        []string{"agent"},
		focusMode:            "retain",
		speakerActiveFormat:  "@{speaker_id}: {text}",
		speakerPassiveFormat: "@{speaker_id} [background]: {text}",
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want retained passive speaker transcript", events)
	}
	if got := events[0].Alternatives[0].Text; got != "@customer [background]: background" {
		t.Fatalf("text = %q, want reference passive speaker format", got)
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

func TestSpeechmaticsSegmentFinalDropsOverlappedReferenceRawFinal(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: true, bufferRawFinals: true}
	var rawFinal smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.1,
			"end_time":0.4,
			"alternatives":[{"content":"hello","language":"en","speaker":"S1"}]
		}]
	}`), &rawFinal); err != nil {
		t.Fatalf("unmarshal raw final response: %v", err)
	}
	if events := speechmaticsEvents(rawFinal, state); len(events) != 0 {
		t.Fatalf("raw final events = %#v, want buffered behind reference segment final", events)
	}

	var segmentFinal smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"hello",
			"language":"en",
			"speaker_id":"S1",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &segmentFinal); err != nil {
		t.Fatalf("unmarshal segment final response: %v", err)
	}
	events := speechmaticsEvents(segmentFinal, state)
	if len(events) != 1 {
		t.Fatalf("segment events = %#v, want one reference segment final", events)
	}
	if got := events[0].Alternatives[0].Text; got != "hello" {
		t.Fatalf("segment final text = %q, want hello", got)
	}

	endEvents := speechmaticsEvents(smResponse{Message: "EndOfTurn"}, state)
	if len(endEvents) != 1 || endEvents[0].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end events = %#v, want only end_of_speech without duplicate raw final", endEvents)
	}
}

func TestSpeechmaticsSegmentFinalDropsSameZeroTimingReferenceRawFinal(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: true, bufferRawFinals: true}
	var rawFinal smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"metadata":{"transcript":"hello"},
		"results":[{
			"type":"word",
			"alternatives":[{"content":"hello","language":"en","speaker":"S1"}]
		}]
	}`), &rawFinal); err != nil {
		t.Fatalf("unmarshal zero-timing raw final response: %v", err)
	}
	if events := speechmaticsEvents(rawFinal, state); len(events) != 0 {
		t.Fatalf("raw final events = %#v, want buffered behind reference segment final", events)
	}

	var segmentFinal smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"hello",
			"language":"en",
			"speaker_id":"S1"
		}]
	}`), &segmentFinal); err != nil {
		t.Fatalf("unmarshal zero-timing segment final response: %v", err)
	}
	events := speechmaticsEvents(segmentFinal, state)
	if len(events) != 1 {
		t.Fatalf("segment events = %#v, want one reference segment final", events)
	}
	if got := events[0].Alternatives[0].Text; got != "hello" {
		t.Fatalf("segment final text = %q, want hello", got)
	}

	endEvents := speechmaticsEvents(smResponse{Message: "EndOfTurn"}, state)
	if len(endEvents) != 1 || endEvents[0].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end events = %#v, want only end_of_speech without duplicate zero-timing raw final", endEvents)
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

func TestSpeechmaticsSegmentEventsFormatsReferenceNullText(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":null,
			"language":"en",
			"speaker_id":"S1",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, nil)
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want one transcript", events)
	}
	if got := events[0].Alternatives[0].Text; got != "None" {
		t.Fatalf("text = %q, want reference formatted null text", got)
	}
}

func TestSpeechmaticsSegmentEventsPreserveReferenceEmptyLanguage(t *testing.T) {
	state := &speechmaticsStreamState{language: "de"}
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"blank language",
			"language":"",
			"speaker_id":"S1",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want one transcript", events)
	}
	if got := events[0].Alternatives[0].Language; got != "" {
		t.Fatalf("language = %q, want explicit empty reference segment language", got)
	}
}

func TestSpeechmaticsSegmentEventsPreserveReferenceEmptySpeakerID(t *testing.T) {
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"empty speaker",
			"language":"en",
			"speaker_id":"",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, nil)
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want one transcript", events)
	}
	if got := events[0].Alternatives[0].SpeakerID; got != "" {
		t.Fatalf("speaker id = %q, want explicit empty reference speaker id", got)
	}
}

func TestSpeechmaticsSegmentEventsKeepReferenceMissingSpeakerDuringFocusIgnore(t *testing.T) {
	state := &speechmaticsStreamState{
		focusSpeakers: []string{"agent"},
		focusMode:     "ignore",
	}
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"unassigned words",
			"language":"en",
			"metadata":{"start_time":0.1,"end_time":0.4}
		},{
			"text":"agent words",
			"language":"en",
			"speaker_id":"agent",
			"metadata":{"start_time":0.5,"end_time":0.8}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 2 {
		t.Fatalf("events = %#v, want missing-speaker segment retained before focused speaker", events)
	}
	if got := events[0].Alternatives[0].SpeakerID; got != "UU" {
		t.Fatalf("missing speaker id = %q, want reference default UU", got)
	}
	if got := events[0].Alternatives[0].Text; got != "unassigned words" {
		t.Fatalf("missing speaker text = %q, want unassigned words", got)
	}
	if got := events[1].Alternatives[0].Text; got != "agent words" {
		t.Fatalf("focused speaker text = %q, want agent words", got)
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

func TestSpeechmaticsSegmentEventsFormatsReferenceNullSpeakerID(t *testing.T) {
	state := &speechmaticsStreamState{
		speakerActiveFormat: "@{speaker_id}: {text}",
	}
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"null speaker",
			"language":"en",
			"speaker_id":null,
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want one transcript", events)
	}
	if got := events[0].Alternatives[0].Text; got != "@None: null speaker" {
		t.Fatalf("text = %q, want reference formatted null speaker id", got)
	}
}

func TestSpeechmaticsSegmentEventsTreatReferenceNullActiveAsPassive(t *testing.T) {
	state := &speechmaticsStreamState{
		speakerActiveFormat:  "@{speaker_id}: {text}",
		speakerPassiveFormat: "@{speaker_id} [background]: {text}",
	}
	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"null active",
			"language":"en",
			"speaker_id":"S1",
			"is_active":null,
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segment response: %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 || len(events[0].Alternatives) != 1 {
		t.Fatalf("events = %#v, want one transcript", events)
	}
	if got := events[0].Alternatives[0].Text; got != "@S1 [background]: null active" {
		t.Fatalf("text = %q, want reference passive format for explicit null is_active", got)
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

func TestSpeechmaticsSegmentEventsEmitReferencePartialsWhenDelivered(t *testing.T) {
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
	events := speechmaticsEvents(partial, state)
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("partial events = %#v, want delivered reference interim transcript", events)
	}
	if got := events[0].Alternatives[0].Text; got != "partial words" {
		t.Fatalf("partial text = %q, want partial words", got)
	}

	var stablePartial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialSegment",
		"segments":[{
			"text":"stable words",
			"language":"en",
			"speaker_id":"agent",
			"annotation":["has_final"],
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &stablePartial); err != nil {
		t.Fatalf("unmarshal stable partial response: %v", err)
	}
	events = speechmaticsEvents(stablePartial, state)
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("stable partial events = %#v, want reference interim transcript with has_final", events)
	}
	if got := events[0].Alternatives[0].Text; got != "stable words" {
		t.Fatalf("stable partial text = %q, want stable words", got)
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
	events = speechmaticsEvents(final, state)
	if len(events) != 1 || events[0].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("final events = %#v, want final transcript despite include_partials=false", events)
	}
}

func TestSpeechmaticsSegmentEventsEmitDeliveredReferencePartialsWhenDisabled(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: false}
	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialSegment",
		"segments":[{
			"text":"provider partial",
			"language":"en",
			"speaker_id":"agent",
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal partial response: %v", err)
	}

	events := speechmaticsEvents(partial, state)
	if len(events) != 1 || events[0].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("partial events = %#v, want delivered reference interim segment", events)
	}
	if got := events[0].Alternatives[0].Text; got != "provider partial" {
		t.Fatalf("partial text = %q, want provider partial", got)
	}
}

func TestSpeechmaticsSegmentEventsRecordReferencePartialAnnotationsWhenOutputDisabled(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: false}
	var partial smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialSegment",
		"segments":[{
			"text":"partial words",
			"language":"en",
			"speaker_id":"agent",
			"annotation":["slow_speaker"],
			"metadata":{"start_time":0.1,"end_time":0.4}
		}]
	}`), &partial); err != nil {
		t.Fatalf("unmarshal partial response: %v", err)
	}

	if events := speechmaticsEvents(partial, state); len(events) != 1 {
		t.Fatalf("partial events = %#v, want delivered partial transcript even when include_partials is false", events)
	}
	if !state.latestSegmentAnnotationSet {
		t.Fatal("latest annotation set = false, want reference partial annotation retained for endpointing")
	}
	if got, want := state.latestSegmentAnnotation, []string{"slow_speaker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("latest annotation = %#v, want %#v", got, want)
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

func TestSpeechmaticsSTTUnexpectedCloseDrainsPendingRawFinalBeforeError(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"results": []map[string]interface{}{{
				"type":       "word",
				"start_time": 0.1,
				"end_time":   0.3,
				"alternatives": []map[string]interface{}{{
					"content":    "closing",
					"confidence": 0.9,
					"speaker":    "S1",
					"language":   "en",
				}},
			}},
		}); err != nil {
			t.Errorf("write raw final: %v", err)
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
	if err != nil {
		t.Fatalf("first Next error = %v, want pending raw final before provider close error", err)
	}
	if event == nil || event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("first Next event = %#v, want pending raw final transcript", event)
	}
	if got := event.Alternatives[0].Text; got != "closing" {
		t.Fatalf("transcript = %q, want closing", got)
	}
	event, err = stream.Next()
	if event != nil {
		t.Fatalf("second Next event = %#v, want nil before provider close error", event)
	}
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want reference provider close error", err)
	}
}

func TestSpeechmaticsSTTUnexpectedCloseQueuesPendingRawFinalWhenEventsFull(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverReady := make(chan struct{})
	releaseMessages := make(chan struct{})
	serverClosed := make(chan struct{})
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
		close(serverReady)
		<-releaseMessages
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{{
				"text":       "already queued",
				"language":   "en",
				"speaker_id": "S1",
			}},
		}); err != nil {
			t.Errorf("write AddSegment: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"results": []map[string]interface{}{{
				"type":       "word",
				"start_time": 0.2,
				"end_time":   0.5,
				"alternatives": []map[string]interface{}{{
					"content":    "pending final",
					"confidence": 0.9,
					"speaker":    "S1",
					"language":   "en",
				}},
			}},
		}); err != nil {
			t.Errorf("write AddTranscript: %v", err)
			return
		}
		if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.UnderlyingConn().Close()
		close(serverClosed)
	}))
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	smStream := stream.(*speechmaticsSTTStream)
	smStream.events = make(chan *stt.SpeechEvent, 1)

	select {
	case <-serverReady:
	case <-time.After(time.Second):
		t.Fatal("server did not receive StartRecognition")
	}
	close(releaseMessages)
	select {
	case <-serverClosed:
	case <-time.After(time.Second):
		t.Fatal("server did not close Speechmatics connection")
	}

	deadline := time.After(100 * time.Millisecond)
	for {
		if active := provider.activeStreams(); len(active) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("unexpected close cleanup blocked on full events channel while flushing pending raw final")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want queued transcript before close error", err)
	}
	if got := event.Alternatives[0].Text; got != "already queued" {
		t.Fatalf("first transcript = %q, want queued transcript before pending raw final", got)
	}
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want pending raw final before close error", err)
	}
	if got := event.Alternatives[0].Text; got != "pending final" {
		t.Fatalf("second transcript = %q, want pending raw final", got)
	}
	event, err = stream.Next()
	if event != nil {
		t.Fatalf("third Next event = %#v, want terminal error", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("third Next error = %T %v, want APIConnectionError after drained transcripts", err, err)
	}
}

func TestSpeechmaticsSTTProviderCloseClosesReferenceVAD(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
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

	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")),
		WithSpeechmaticsSTTVAD(&fakeSpeechmaticsVAD{stream: vadStream}),
	)
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
	if !vadStream.isClosed() {
		t.Fatal("VAD stream closed = false after provider close")
	}
}

func TestSpeechmaticsSTTReadFailureReturnsReferenceConnectionError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read start message: %v", err)
			return
		}
		if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.UnderlyingConn().Close()
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
		t.Fatalf("Next event = %#v, want nil on provider read failure", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestSpeechmaticsSTTReadLoopErrorDeliveryDoesNotBlockWhenErrorQueued(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverReady := make(chan struct{})
	closeProvider := make(chan struct{})
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
		close(serverReady)
		<-closeProvider
		if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.UnderlyingConn().Close()
	}))
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	smStream := stream.(*speechmaticsSTTStream)
	t.Cleanup(func() {
		select {
		case <-smStream.errCh:
		default:
		}
	})
	select {
	case <-serverReady:
	case <-time.After(time.Second):
		t.Fatal("server did not receive StartRecognition")
	}
	smStream.errCh <- errors.New("queued provider error")
	close(closeProvider)

	select {
	case _, ok := <-smStream.events:
		if ok {
			t.Fatal("events yielded transcript, want cleanup close")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read loop blocked delivering provider error with queued error")
	}
}

func TestSpeechmaticsSTTStartupWriteFailureReturnsReferenceConnectionErrorAndClosesVAD(t *testing.T) {
	withSpeechmaticsSTTRetryInterval(t, 0)
	vadStream := newFakeSpeechmaticsVADStream()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &speechmaticsFailAfterHandshakeWriteConn{Conn: conn}, nil
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")),
		WithSpeechmaticsSTTVAD(&fakeSpeechmaticsVAD{stream: vadStream}),
	)
	stream, err := provider.Stream(context.Background(), "")
	if err == nil {
		if stream != nil {
			_ = stream.Close()
		}
		t.Fatal("Stream error = nil, want StartRecognition write failure")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
	if !vadStream.isClosed() {
		t.Fatal("VAD stream closed = false after startup write failure")
	}
}

func TestSpeechmaticsSTTStreamRetriesReferenceStartupWriteFailure(t *testing.T) {
	withSpeechmaticsSTTRetryInterval(t, 0)
	upgrader := websocket.Upgrader{}
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		if attempt == 1 {
			if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
				_ = tcpConn.SetLinger(0)
			}
			_ = conn.UnderlyingConn().Close()
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read start message: %v", err)
		}
	}))
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %T %v, want retry success", err, err)
	}
	defer stream.Close()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("startup attempts = %d, want one reference retry after StartRecognition write failure", got)
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
				"message": "AddSegment",
				"segments": []map[string]interface{}{
					{
						"text":       "hello",
						"language":   "en",
						"speaker_id": "S1",
						"metadata": map[string]interface{}{
							"start_time": 0.0,
							"end_time":   0.2,
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

func TestSpeechmaticsSTTInvalidJSONClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{`)); err != nil {
			t.Errorf("write invalid JSON: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after invalid provider JSON", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid JSON received") {
		t.Fatalf("APIConnectionError message = %q, want invalid JSON reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTMalformedRecognizedMessageClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message":  "AddSegment",
			"segments": "not segments",
		}); err != nil {
			t.Errorf("write malformed AddSegment: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after malformed recognized message", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawMetadataClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message":  "AddTranscript",
			"metadata": nil,
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": 0.0,
					"end_time":   0.2,
					"alternatives": []map[string]interface{}{
						{
							"content":  "corrupt",
							"language": "en",
							"speaker":  "S1",
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-metadata AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript metadata", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawTagsClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": 0.0,
					"end_time":   0.2,
					"alternatives": []map[string]interface{}{
						{
							"content":  "corrupt",
							"language": "en",
							"speaker":  "S1",
							"tags":     nil,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-tags AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript tags", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawLanguageClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": 0.0,
					"end_time":   0.2,
					"alternatives": []map[string]interface{}{
						{
							"content":  "corrupt",
							"language": nil,
							"speaker":  "S1",
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-language AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript language", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawConfidenceClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": 0.0,
					"end_time":   0.2,
					"alternatives": []map[string]interface{}{
						{
							"content":    "corrupt",
							"language":   "en",
							"speaker":    "S1",
							"confidence": nil,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-confidence AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript confidence", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawIsEOSClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": 0.0,
					"end_time":   0.2,
					"is_eos":     nil,
					"alternatives": []map[string]interface{}{
						{
							"content":    "corrupt",
							"language":   "en",
							"speaker":    "S1",
							"confidence": 0.9,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-is-eos AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript is_eos", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawAttachesToClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":        "punctuation",
					"start_time":  0.0,
					"end_time":    0.2,
					"attaches_to": nil,
					"alternatives": []map[string]interface{}{
						{
							"content":    ".",
							"language":   "en",
							"speaker":    "S1",
							"confidence": 1.0,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-attaches-to AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript attaches_to", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawTypeClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       nil,
					"start_time": 0.0,
					"end_time":   0.2,
					"alternatives": []map[string]interface{}{
						{
							"content":    "corrupt",
							"language":   "en",
							"speaker":    "S1",
							"confidence": 0.9,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-type AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript type", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawDirectionClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": 0.0,
					"end_time":   0.2,
					"alternatives": []map[string]interface{}{
						{
							"content":    "corrupt",
							"language":   "en",
							"direction":  nil,
							"speaker":    "S1",
							"confidence": 0.9,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-direction AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript direction", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawStartTimeClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": nil,
					"end_time":   0.2,
					"alternatives": []map[string]interface{}{
						{
							"content":    "corrupt",
							"language":   "en",
							"speaker":    "S1",
							"confidence": 0.9,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-start-time AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript start_time", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullRawEndTimeClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddTranscript",
			"metadata": map[string]interface{}{
				"start_time": 0.0,
				"end_time":   0.2,
			},
			"results": []map[string]interface{}{
				{
					"type":       "word",
					"start_time": 0.0,
					"end_time":   nil,
					"alternatives": []map[string]interface{}{
						{
							"content":    "corrupt",
							"language":   "en",
							"speaker":    "S1",
							"confidence": 0.9,
						},
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-end-time AddTranscript: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null raw transcript end_time", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullSegmentMetadataClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "corrupt",
					"language":   "en",
					"speaker_id": "S1",
					"metadata":   nil,
				},
			},
		}); err != nil {
			t.Errorf("write null-metadata AddSegment: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null segment metadata", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTNullSegmentLanguageClosesReferenceStream(t *testing.T) {
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
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "corrupt",
					"language":   nil,
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		}); err != nil {
			t.Errorf("write null-language AddSegment: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "ignored",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		})
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
		t.Fatalf("Next event = %#v, want nil after null segment language", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "Invalid Speechmatics message") {
		t.Fatalf("APIConnectionError message = %q, want malformed message reason", connectionErr.Message)
	}
}

func TestSpeechmaticsSTTValidNonObjectJSONDoesNotAbortReferenceStream(t *testing.T) {
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
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`[1]`)); err != nil {
			t.Errorf("write valid non-object JSON: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]interface{}{
			"message": "AddSegment",
			"segments": []map[string]interface{}{
				{
					"text":       "hello",
					"language":   "en",
					"speaker_id": "S1",
					"metadata": map[string]interface{}{
						"start_time": 0.0,
						"end_time":   0.2,
					},
				},
			},
		}); err != nil {
			t.Errorf("write transcript: %v", err)
			return
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
		t.Fatalf("Next error = %v, want transcript after valid non-object JSON", err)
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

func TestSpeechmaticsSTTEndOfTranscriptClosesStreamBeforeNext(t *testing.T) {
	closedTransport := false
	stream := &speechmaticsSTTStream{
		closeConn: func() error {
			closedTransport = true
			return nil
		},
	}

	if keepReading := stream.handleResponse(smResponse{Message: "EndOfTranscript"}); keepReading {
		t.Fatal("EndOfTranscript handler continued reading, want terminal stream cleanup")
	}
	if !closedTransport {
		t.Fatal("EndOfTranscript did not close provider transport")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01}}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after EndOfTranscript before Next = %v, want io.ErrClosedPipe", err)
	}
}

func TestSpeechmaticsSTTEndOfTranscriptUnblocksInFlightPushFrameLikeReference(t *testing.T) {
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	var releaseOnce sync.Once
	stream := &speechmaticsSTTStream{
		writeBinary: func([]byte) error {
			close(writeStarted)
			<-releaseWrite
			return io.ErrClosedPipe
		},
		closeConn: func() error {
			releaseOnce.Do(func() { close(releaseWrite) })
			return nil
		},
	}

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- stream.PushFrame(&model.AudioFrame{
			Data:              make([]byte, 3200),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1600,
		})
	}()

	select {
	case <-writeStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PushFrame did not start provider audio write")
	}

	endDone := make(chan bool, 1)
	go func() {
		endDone <- stream.handleResponse(smResponse{Message: "EndOfTranscript"})
	}()

	select {
	case keepReading := <-endDone:
		if keepReading {
			t.Fatal("EndOfTranscript handler continued reading, want terminal cleanup")
		}
	case <-time.After(100 * time.Millisecond):
		releaseOnce.Do(func() { close(releaseWrite) })
		t.Fatal("EndOfTranscript did not unblock in-flight PushFrame")
	}

	select {
	case err := <-pushDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("PushFrame error = %v, want closed-pipe after EndOfTranscript interrupted write", err)
		}
	case <-time.After(100 * time.Millisecond):
		releaseOnce.Do(func() { close(releaseWrite) })
		t.Fatal("PushFrame did not return after EndOfTranscript")
	}
}

func TestSpeechmaticsSTTNextDrainsQueuedTranscriptAfterEndOfTranscript(t *testing.T) {
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 2),
		errCh:  make(chan error, 1),
		state: &speechmaticsStreamState{
			language:        "en",
			includePartials: true,
		},
		closeConn: func() error { return nil },
	}

	if keepReading := stream.handleResponse(smResponse{
		Message: "AddSegment",
		Segments: []struct {
			Text       string   `json:"text"`
			Language   string   `json:"language"`
			SpeakerID  string   `json:"speaker_id"`
			IsActive   *bool    `json:"is_active"`
			Annotation []string `json:"annotation"`
			Metadata   struct {
				StartTime float64 `json:"start_time"`
				EndTime   float64 `json:"end_time"`
			} `json:"metadata"`
		}{{
			Text:      "final before end",
			Language:  "en",
			SpeakerID: "S1",
		}},
	}); !keepReading {
		t.Fatal("AddSegment stopped read loop")
	}
	if keepReading := stream.handleResponse(smResponse{Message: "EndOfTranscript"}); keepReading {
		t.Fatal("EndOfTranscript handler continued reading, want terminal stream cleanup")
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want queued final transcript before EOF", err)
	}
	if event == nil || event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("Next event = %#v, want queued final transcript", event)
	}
	if got := event.Alternatives[0].Text; got != "final before end" {
		t.Fatalf("transcript = %q, want final before end", got)
	}
}

func TestSpeechmaticsSTTEndOfTranscriptFlushesPendingRawFinalBeforeEOF(t *testing.T) {
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 2),
		errCh:  make(chan error, 1),
		state: &speechmaticsStreamState{
			includePartials: true,
			bufferRawFinals: true,
		},
		closeConn: func() error { return nil },
	}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.2,
			"end_time":0.5,
			"alternatives":[{"content":"closing","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}

	if keepReading := stream.handleResponse(final); !keepReading {
		t.Fatal("AddTranscript stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("events before EndOfTranscript = %d, want raw final buffered", len(stream.events))
	}
	if keepReading := stream.handleResponse(smResponse{Message: "EndOfTranscript"}); keepReading {
		t.Fatal("EndOfTranscript handler continued reading, want terminal stream cleanup")
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want pending raw final before EOF", err)
	}
	if event == nil || event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("Next event = %#v, want pending raw final transcript", event)
	}
	if got := event.Alternatives[0].Text; got != "closing" {
		t.Fatalf("transcript = %q, want pending raw final text", got)
	}
}

func TestSpeechmaticsSTTEndOfTranscriptQueuesPendingRawFinalWhenEventsFull(t *testing.T) {
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		state: &speechmaticsStreamState{
			includePartials: true,
			bufferRawFinals: true,
		},
		closeConn: func() error { return nil },
	}
	stream.events <- &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: "already queued"},
		},
	}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.2,
			"end_time":0.5,
			"alternatives":[{"content":"pending final","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}
	if keepReading := stream.handleResponse(final); !keepReading {
		t.Fatal("AddTranscript stopped read loop")
	}

	done := make(chan bool, 1)
	go func() {
		done <- stream.handleResponse(smResponse{Message: "EndOfTranscript"})
	}()
	select {
	case keepReading := <-done:
		if keepReading {
			t.Fatal("EndOfTranscript handler continued reading, want terminal cleanup")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("EndOfTranscript blocked on full events channel while flushing pending raw final")
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want queued transcript before EOF", err)
	}
	if got := event.Alternatives[0].Text; got != "already queued" {
		t.Fatalf("first transcript = %q, want queued transcript before pending raw final", got)
	}
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want pending raw final before EOF", err)
	}
	if got := event.Alternatives[0].Text; got != "pending final" {
		t.Fatalf("second transcript = %q, want pending raw final", got)
	}
	if event, err := stream.Next(); event != nil || err != io.EOF {
		t.Fatalf("third Next = (%#v, %v), want EOF after terminal drain", event, err)
	}
}

func TestSpeechmaticsSTTCloseFlushesPendingRawFinalBeforeEOF(t *testing.T) {
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 2),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		state: &speechmaticsStreamState{
			includePartials: true,
			bufferRawFinals: true,
		},
		closeConn: func() error { return nil },
	}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.2,
			"end_time":0.5,
			"alternatives":[{"content":"closing","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}

	if keepReading := stream.handleResponse(final); !keepReading {
		t.Fatal("AddTranscript stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("events before Close = %d, want raw final buffered", len(stream.events))
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want pending raw final before EOF", err)
	}
	if event == nil || event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("Next event = %#v, want pending raw final transcript", event)
	}
	if got := event.Alternatives[0].Text; got != "closing" {
		t.Fatalf("transcript = %q, want pending raw final text", got)
	}
	if event, err := stream.Next(); event != nil || err != io.EOF {
		t.Fatalf("second Next = (%#v, %v), want EOF after drained close final", event, err)
	}
}

func TestSpeechmaticsSTTCloseUnblocksPendingNextWithRawFinal(t *testing.T) {
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		state: &speechmaticsStreamState{
			includePartials: true,
			bufferRawFinals: true,
		},
		closeConn: func() error {
			close(closeStarted)
			<-releaseClose
			return nil
		},
	}
	var final smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{
			"type":"word",
			"start_time":0.2,
			"end_time":0.5,
			"alternatives":[{"content":"closing","confidence":0.9,"speaker":"S1","language":"en"}]
		}]
	}`), &final); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}
	if keepReading := stream.handleResponse(final); !keepReading {
		t.Fatal("AddTranscript stopped read loop")
	}

	result := make(chan *stt.SpeechEvent, 1)
	errs := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		result <- event
		errs <- err
	}()
	select {
	case err := <-errs:
		t.Fatalf("Next returned before Close with error %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	closeErr := make(chan error, 1)
	go func() {
		closeErr <- stream.Close()
	}()
	select {
	case <-closeStarted:
	case <-time.After(100 * time.Millisecond):
		close(releaseClose)
		t.Fatal("Close did not start transport close")
	}
	select {
	case event := <-result:
		err := <-errs
		close(releaseClose)
		t.Fatalf("Next returned before close drain ready: event=%#v err=%v", event, err)
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseClose)
	if err := <-closeErr; err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case event := <-result:
		err := <-errs
		if err != nil {
			t.Fatalf("Next error after Close = %v, want pending raw final", err)
		}
		if event == nil || event.Type != stt.SpeechEventFinalTranscript {
			t.Fatalf("Next event = %#v, want pending raw final transcript", event)
		}
		if got := event.Alternatives[0].Text; got != "closing" {
			t.Fatalf("transcript = %q, want pending raw final text", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close did not unblock pending Next with raw final")
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

func TestSpeechmaticsSTTEnqueueEventPreservesReferenceOrderWhenEventsFull(t *testing.T) {
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
	}
	first := &stt.SpeechEvent{
		Type: stt.SpeechEventInterimTranscript,
		Alternatives: []stt.SpeechData{
			{Text: "first"},
		},
	}
	second := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: "second"},
		},
	}

	if !stream.enqueueEvent(first) {
		t.Fatal("enqueue first = false, want queued event")
	}
	done := make(chan bool, 1)
	go func() {
		done <- stream.enqueueEvent(second)
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("enqueue second = false, want overflow queue")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("enqueue second blocked on full events channel, want reference nonblocking event queue")
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want first queued event", err)
	}
	if got := event.Alternatives[0].Text; got != "first" {
		t.Fatalf("first transcript = %q, want first", got)
	}
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want overflow event", err)
	}
	if got := event.Alternatives[0].Text; got != "second" {
		t.Fatalf("second transcript = %q, want overflow event", got)
	}
}

func TestSpeechmaticsSTTNextSurfacesErrorAfterQueuedTranscriptLikeReference(t *testing.T) {
	for range 64 {
		stream := &speechmaticsSTTStream{
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
		}
		streamErr := errors.New("provider close")
		stream.events <- &stt.SpeechEvent{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{
				{Text: "hello"},
			},
		}
		stream.errCh <- streamErr

		event, err := stream.Next()
		if err != nil {
			t.Fatalf("first Next error = %v, want queued transcript before stream error", err)
		}
		if event == nil || event.Alternatives[0].Text != "hello" {
			t.Fatalf("first Next event = %#v, want queued final transcript", event)
		}

		result := make(chan error, 1)
		go func() {
			event, err := stream.Next()
			if event != nil {
				result <- fmt.Errorf("second Next event = %#v, want nil", event)
				return
			}
			result <- err
		}()

		select {
		case err := <-result:
			if !errors.Is(err, streamErr) {
				t.Fatalf("second Next error = %v, want queued stream error", err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("second Next blocked after queued transcript, want stream error")
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

func TestSpeechmaticsSTTDefaultExternalLoadsReferenceSileroVAD(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	if provider.turnDetectionMode != "external" {
		t.Fatalf("turn detection mode = %q, want default external", provider.turnDetectionMode)
	}
	if provider.vad == nil {
		t.Fatal("vad = nil, want reference default Silero VAD")
	}
	if label := provider.vad.Label(); label != "silero.VAD" {
		t.Fatalf("vad label = %q, want silero.VAD", label)
	}
}

func TestSpeechmaticsSTTExplicitNilVADOptsOutOfReferenceAutoVAD(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTVAD(nil))

	if provider.vad != nil {
		t.Fatalf("vad = %#v, want nil when caller explicitly opts out", provider.vad)
	}
	if provider.turnDetectionMode != "external" {
		t.Fatalf("turn detection mode = %q, want external", provider.turnDetectionMode)
	}
}

func TestSpeechmaticsSTTProviderManagedModesLoadReferenceLocalVAD(t *testing.T) {
	tests := []struct {
		name string
		opt  SpeechmaticsSTTOption
	}{
		{name: "adaptive", opt: WithSpeechmaticsSTTAdaptiveTurnDetection()},
		{name: "smart_turn", opt: WithSpeechmaticsSTTSmartTurnDetection()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewSpeechmaticsSTT("test-key", tt.opt)
			if provider.turnDetectionMode != tt.name {
				t.Fatalf("turn detection mode = %q, want %s", provider.turnDetectionMode, tt.name)
			}
			if provider.vad == nil {
				t.Fatal("vad = nil, want reference local Silero VAD for provider-managed endpointing")
			}
			if label := provider.vad.Label(); label != "silero.VAD" {
				t.Fatalf("vad label = %q, want silero.VAD", label)
			}
		})
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADEndOfSpeechDelaysReferenceEOU(t *testing.T) {
	originalDelay := speechmaticsLocalEndpointingDelay
	speechmaticsLocalEndpointingDelay = func(*SpeechmaticsSTT) time.Duration { return 20 * time.Millisecond }
	t.Cleanup(func() { speechmaticsLocalEndpointingDelay = originalDelay })

	vadStream := newFakeSpeechmaticsVADStream()
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection())
	provider.vad = &fakeSpeechmaticsVAD{stream: vadStream}

	controlMessages := make(chan map[string]interface{}, 4)
	stream := &speechmaticsSTTStream{
		owner:                      provider,
		providerManagedEndpointing: true,
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			controlMessages <- control
			return nil
		},
		closeConn: func() error { return nil },
	}
	if err := stream.startVAD(context.Background()); err != nil {
		t.Fatalf("startVAD() error = %v", err)
	}
	defer stream.Close()

	vadStream.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	select {
	case message := <-controlMessages:
		t.Fatalf("immediate control message = %#v, want delayed ForceEndOfUtterance after reference turn timer", message)
	case <-time.After(5 * time.Millisecond):
	}
	waitForSpeechmaticsControlMessage(t, controlMessages, "ForceEndOfUtterance")
}

func TestSpeechmaticsSTTAdaptiveLocalVADDelayClampsReferenceMaxDelay(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdaptiveTurnDetection(),
		WithSpeechmaticsSTTEndOfUtteranceMaxDelay(0.05),
	)

	if got, want := speechmaticsLocalEndpointingDelay(provider), 50*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want reference max-delay clamp %s", got, want)
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADDelayAppliesReferenceStoppedPenalty(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection())

	if got, want := speechmaticsLocalEndpointingDelay(provider), 140*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want reference VAD-stopped penalty delay %s", got, want)
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADDelayAppliesReferenceSegmentAnnotationPenalties(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection())

	tests := []struct {
		name        string
		annotation  []string
		want        time.Duration
		description string
	}{
		{
			name:        "final eos",
			annotation:  []string{"ends_with_final", "ends_with_eos"},
			want:        70 * time.Millisecond,
			description: "reference final sentence penalty halves the VAD-stopped delay",
		},
		{
			name:        "no eos",
			annotation:  []string{"has_final"},
			want:        280 * time.Millisecond,
			description: "reference missing-EOS penalty doubles the VAD-stopped delay",
		},
		{
			name:        "disfluency no eos",
			annotation:  []string{"has_disfluency"},
			want:        308 * time.Millisecond,
			description: "reference disfluency and missing-EOS penalties compound",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &speechmaticsSTTStream{
				owner: provider,
				state: &speechmaticsStreamState{
					latestSegmentAnnotationSet: true,
					latestSegmentAnnotation:    tt.annotation,
				},
			}
			if got := stream.localEndpointingDelay(); got != tt.want {
				t.Fatalf("local endpointing delay = %s, want %s: %s", got, tt.want, tt.description)
			}
		})
	}
}

func TestSpeechmaticsSegmentEventsFilterReferenceWrappedSystemSpeakerLabels(t *testing.T) {
	if !speechmaticsSystemSpeakerID("__assistant__") {
		t.Fatal("lowercase wrapped speaker was not classified as reference system speaker")
	}

	for _, speakerID := range []string{"__ASSISTANT__", "__assistant__", "__Assistant__", "__a__", "__A1__", "__A_B__"} {
		t.Run(speakerID, func(t *testing.T) {
			resp := smResponse{Message: "AddSegment"}
			resp.Segments = append(resp.Segments, struct {
				Text       string   `json:"text"`
				Language   string   `json:"language"`
				SpeakerID  string   `json:"speaker_id"`
				IsActive   *bool    `json:"is_active"`
				Annotation []string `json:"annotation"`
				Metadata   struct {
					StartTime float64 `json:"start_time"`
					EndTime   float64 `json:"end_time"`
				} `json:"metadata"`
			}{
				Text:      "system echo",
				Language:  "en",
				SpeakerID: speakerID,
			})

			if events := speechmaticsEvents(resp, nil); len(events) != 0 {
				t.Fatalf("events = %#v, want wrapped system speaker %q filtered", events, speakerID)
			}
		})
	}
}

func TestSpeechmaticsSegmentEventsKeepReferenceNonWrappedSpeakerLabels(t *testing.T) {
	for _, speakerID := range []string{"__", "__A", "A__", "_assistant_"} {
		t.Run(speakerID, func(t *testing.T) {
			resp := smResponse{Message: "AddSegment"}
			resp.Segments = append(resp.Segments, struct {
				Text       string   `json:"text"`
				Language   string   `json:"language"`
				SpeakerID  string   `json:"speaker_id"`
				IsActive   *bool    `json:"is_active"`
				Annotation []string `json:"annotation"`
				Metadata   struct {
					StartTime float64 `json:"start_time"`
					EndTime   float64 `json:"end_time"`
				} `json:"metadata"`
			}{
				Text:      "speaker words",
				Language:  "en",
				SpeakerID: speakerID,
			})

			events := speechmaticsEvents(resp, nil)
			if len(events) != 1 || len(events[0].Alternatives) != 1 {
				t.Fatalf("events = %#v, want non-wrapped speaker %q emitted", events, speakerID)
			}
			if got := events[0].Alternatives[0].SpeakerID; got != speakerID {
				t.Fatalf("speaker id = %q, want %q", got, speakerID)
			}
		})
	}
}

func TestSpeechmaticsSegmentEventsRecordReferenceActiveSegmentAnnotations(t *testing.T) {
	inactive := false
	active := true
	state := &speechmaticsStreamState{}
	resp := smResponse{Message: "AddSegment"}
	resp.Segments = append(resp.Segments, struct {
		Text       string   `json:"text"`
		Language   string   `json:"language"`
		SpeakerID  string   `json:"speaker_id"`
		IsActive   *bool    `json:"is_active"`
		Annotation []string `json:"annotation"`
		Metadata   struct {
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"metadata"`
	}{
		Text:       "background",
		IsActive:   &inactive,
		Annotation: []string{"has_final"},
	})
	resp.Segments = append(resp.Segments, struct {
		Text       string   `json:"text"`
		Language   string   `json:"language"`
		SpeakerID  string   `json:"speaker_id"`
		IsActive   *bool    `json:"is_active"`
		Annotation []string `json:"annotation"`
		Metadata   struct {
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"metadata"`
	}{
		Text:       "done.",
		IsActive:   &active,
		Annotation: []string{"ends_with_final", "ends_with_eos"},
	})

	events := speechmaticsEvents(resp, state)
	if len(events) != 2 {
		t.Fatalf("events = %d, want both segment transcript events", len(events))
	}
	if !state.latestSegmentAnnotationSet {
		t.Fatal("latest segment annotation not recorded")
	}
	if got, want := state.latestSegmentAnnotation, []string{"ends_with_final", "ends_with_eos"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("latest segment annotation = %#v, want active reference annotation %#v", got, want)
	}
}

func TestSpeechmaticsRawTranscriptRecordsReferenceFinalEOSAnnotationForLocalVAD(t *testing.T) {
	state := &speechmaticsStreamState{}
	resp := smResponse{Message: "AddTranscript"}
	resp.Results = append(resp.Results, smResult{
		Alternatives: []smAlternative{{Content: "done"}},
		Type:         "word",
		IsEOS:        true,
	})

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want raw final transcript", len(events))
	}
	if got, want := state.latestSegmentAnnotation, []string{"ends_with_final", "ends_with_eos"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("latest raw annotation = %#v, want reference final EOS annotation %#v", got, want)
	}

	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection()),
		state: state,
	}
	if got, want := stream.localEndpointingDelay(), 70*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want raw final EOS reference delay %s", got, want)
	}
}

func TestSpeechmaticsRawPartialRecordsReferenceDisfluencyAnnotationForLocalVAD(t *testing.T) {
	state := &speechmaticsStreamState{includePartials: true}
	resp := smResponse{Message: "AddPartialTranscript"}
	resp.Results = append(resp.Results, smResult{
		Alternatives: []smAlternative{{Content: "um", Tags: []string{"disfluency"}}},
		Type:         "word",
		StartTime:    0,
		EndTime:      0.2,
	})

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one raw partial transcript", len(events))
	}
	for _, want := range []string{"has_disfluency", "ends_with_disfluency"} {
		if !speechmaticsStringInSlice(want, state.latestSegmentAnnotation) {
			t.Fatalf("latest raw partial annotation = %#v, want reference %s", state.latestSegmentAnnotation, want)
		}
	}
	if speechmaticsStringInSlice("ends_with_final", state.latestSegmentAnnotation) {
		t.Fatalf("latest raw partial annotation = %#v, want no final marker", state.latestSegmentAnnotation)
	}

	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection()),
		state: state,
	}
	if got, want := stream.localEndpointingDelay(), 770*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want partial disfluency reference delay %s", got, want)
	}
}

func TestSpeechmaticsRawTranscriptRecordsReferenceSlowSpeakerAnnotationForLocalVAD(t *testing.T) {
	state := &speechmaticsStreamState{}
	resp := smResponse{Message: "AddTranscript"}
	for i := 0; i < 10; i++ {
		resp.Results = append(resp.Results, smResult{
			Alternatives: []smAlternative{{Content: fmt.Sprintf("w%d", i)}},
			Type:         "word",
			IsEOS:        i == 9,
			StartTime:    float64(i),
			EndTime:      float64(i + 1),
		})
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one raw final transcript", len(events))
	}
	if !speechmaticsStringInSlice("very_slow_speaker", state.latestSegmentAnnotation) {
		t.Fatalf("latest raw annotation = %#v, want reference very_slow_speaker", state.latestSegmentAnnotation)
	}

	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection()),
		state: state,
	}
	if got, want := stream.localEndpointingDelay(), 210*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want very-slow reference delay %s", got, want)
	}
}

func TestSpeechmaticsRawTranscriptRecordsReferenceDisfluencyAnnotationForLocalVAD(t *testing.T) {
	state := &speechmaticsStreamState{}
	resp := smResponse{Message: "AddTranscript"}
	resp.Results = append(resp.Results, smResult{
		Alternatives: []smAlternative{{Content: "um", Tags: []string{"disfluency"}}},
		Type:         "word",
		StartTime:    0,
		EndTime:      0.2,
	})

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one raw final transcript", len(events))
	}
	for _, want := range []string{"has_disfluency", "ends_with_disfluency"} {
		if !speechmaticsStringInSlice(want, state.latestSegmentAnnotation) {
			t.Fatalf("latest raw annotation = %#v, want reference %s", state.latestSegmentAnnotation, want)
		}
	}

	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection()),
		state: state,
	}
	if got, want := stream.localEndpointingDelay(), 770*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want disfluency reference delay %s", got, want)
	}
}

func TestSpeechmaticsRawTranscriptDecodesReferenceDisfluencyTagsForLocalVAD(t *testing.T) {
	state := &speechmaticsStreamState{}
	payload := []byte(`{
		"message": "AddTranscript",
		"results": [
			{
				"type": "word",
				"start_time": 0,
				"end_time": 0.2,
				"alternatives": [
					{
						"content": "uh",
						"tags": ["disfluency"]
					}
				]
			}
		]
	}`)
	var resp smResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one raw final transcript", len(events))
	}
	for _, want := range []string{"has_disfluency", "starts_with_disfluency", "ends_with_disfluency"} {
		if !speechmaticsStringInSlice(want, state.latestSegmentAnnotation) {
			t.Fatalf("latest raw annotation = %#v, want decoded reference %s", state.latestSegmentAnnotation, want)
		}
	}

	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection()),
		state: state,
	}
	if got, want := stream.localEndpointingDelay(), 770*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want decoded disfluency reference delay %s", got, want)
	}
}

func TestSpeechmaticsRawTranscriptAcceptsReferenceStringDisfluencyTags(t *testing.T) {
	state := &speechmaticsStreamState{}
	payload := []byte(`{
		"message": "AddTranscript",
		"results": [
			{
				"type": "word",
				"start_time": 0,
				"end_time": 0.2,
				"alternatives": [
					{
						"content": "uh",
						"tags": "disfluency"
					}
				]
			}
		]
	}`)
	var resp smResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one raw final transcript", len(events))
	}
	for _, want := range []string{"has_disfluency", "starts_with_disfluency", "ends_with_disfluency"} {
		if !speechmaticsStringInSlice(want, state.latestSegmentAnnotation) {
			t.Fatalf("latest raw annotation = %#v, want string-tag reference %s", state.latestSegmentAnnotation, want)
		}
	}
}

func TestSpeechmaticsRawTranscriptRecordsReferencePenultimateDisfluencyBeforeEOS(t *testing.T) {
	state := &speechmaticsStreamState{}
	resp := smResponse{Message: "AddTranscript"}
	resp.Results = append(resp.Results,
		smResult{
			Alternatives: []smAlternative{{Content: "well", Tags: []string{"disfluency"}}},
			Type:         "word",
			StartTime:    0,
			EndTime:      0.2,
		},
		smResult{
			Alternatives: []smAlternative{{Content: "..."}},
			Type:         "punctuation",
			Attaches:     "previous",
			IsEOS:        true,
			StartTime:    0.2,
			EndTime:      0.2,
		},
	)

	events := speechmaticsEvents(resp, state)
	if len(events) != 1 {
		t.Fatalf("events = %d, want one raw final transcript", len(events))
	}
	for _, want := range []string{"has_disfluency", "starts_with_disfluency", "ends_with_disfluency", "ends_with_eos"} {
		if !speechmaticsStringInSlice(want, state.latestSegmentAnnotation) {
			t.Fatalf("latest raw annotation = %#v, want reference %s", state.latestSegmentAnnotation, want)
		}
	}

	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection()),
		state: state,
	}
	if got, want := stream.localEndpointingDelay(), 193*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want penultimate disfluency reference delay %s", got, want)
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADStartClearsReferenceEndpointAnnotations(t *testing.T) {
	state := &speechmaticsStreamState{
		latestSegmentAnnotationSet: true,
		latestSegmentAnnotation:    []string{"ends_with_final", "ends_with_eos"},
	}
	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection()),
		state: state,
	}
	if got, want := stream.localEndpointingDelay(), 70*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay before start = %s, want previous final EOS delay %s", got, want)
	}

	stream.reopenLocalEndpointingTurn()

	if state.latestSegmentAnnotationSet {
		t.Fatalf("latest segment annotation still set after speech start: %#v", state.latestSegmentAnnotation)
	}
	if got, want := stream.localEndpointingDelay(), 140*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay after speech start = %s, want reference fresh-turn delay %s", got, want)
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADDelayClampsReferenceMinimumDelay(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdaptiveTurnDetection(),
		WithSpeechmaticsSTTEndOfUtteranceMaxDelay(0.005),
	)

	if got, want := speechmaticsLocalEndpointingDelay(provider), 10*time.Millisecond; got != want {
		t.Fatalf("local endpointing delay = %s, want reference min end-of-turn delay %s", got, want)
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADStartCancelsReferenceDelayedEOU(t *testing.T) {
	originalDelay := speechmaticsLocalEndpointingDelay
	speechmaticsLocalEndpointingDelay = func(*SpeechmaticsSTT) time.Duration { return 20 * time.Millisecond }
	t.Cleanup(func() { speechmaticsLocalEndpointingDelay = originalDelay })

	vadStream := newFakeSpeechmaticsVADStream()
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection())
	provider.vad = &fakeSpeechmaticsVAD{stream: vadStream}

	controlMessages := make(chan map[string]interface{}, 4)
	stream := &speechmaticsSTTStream{
		owner:                      provider,
		providerManagedEndpointing: true,
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			controlMessages <- control
			return nil
		},
		closeConn: func() error { return nil },
	}
	if err := stream.startVAD(context.Background()); err != nil {
		t.Fatalf("startVAD() error = %v", err)
	}
	defer stream.Close()

	vadStream.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	vadStream.events <- &vad.VADEvent{Type: vad.VADEventStartOfSpeech}
	select {
	case message := <-controlMessages:
		t.Fatalf("control message after restarted speech = %#v, want canceled delayed ForceEndOfUtterance", message)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSpeechmaticsSTTAdaptiveProviderEndOfTurnCancelsReferenceDelayedEOU(t *testing.T) {
	originalDelay := speechmaticsLocalEndpointingDelay
	speechmaticsLocalEndpointingDelay = func(*SpeechmaticsSTT) time.Duration { return 20 * time.Millisecond }
	t.Cleanup(func() { speechmaticsLocalEndpointingDelay = originalDelay })

	vadStream := newFakeSpeechmaticsVADStream()
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection())
	provider.vad = &fakeSpeechmaticsVAD{stream: vadStream}

	controlMessages := make(chan map[string]interface{}, 4)
	stream := &speechmaticsSTTStream{
		owner:                      provider,
		events:                     make(chan *stt.SpeechEvent, 4),
		providerManagedEndpointing: true,
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			controlMessages <- control
			return nil
		},
		closeConn: func() error { return nil },
		state:     &speechmaticsStreamState{},
	}
	if err := stream.startVAD(context.Background()); err != nil {
		t.Fatalf("startVAD() error = %v", err)
	}
	defer stream.Close()

	vadStream.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	if ok := stream.handleResponse(smResponse{Message: "EndOfTurn"}); !ok {
		t.Fatal("EndOfTurn stopped read loop")
	}
	select {
	case message := <-controlMessages:
		t.Fatalf("control message after provider EndOfTurn = %#v, want canceled delayed ForceEndOfUtterance", message)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSpeechmaticsSTTAdaptiveProviderStartOfTurnCancelsReferenceDelayedEOU(t *testing.T) {
	originalDelay := speechmaticsLocalEndpointingDelay
	speechmaticsLocalEndpointingDelay = func(*SpeechmaticsSTT) time.Duration { return 20 * time.Millisecond }
	t.Cleanup(func() { speechmaticsLocalEndpointingDelay = originalDelay })

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAdaptiveTurnDetection())

	controlMessages := make(chan map[string]interface{}, 4)
	stream := &speechmaticsSTTStream{
		owner:                      provider,
		events:                     make(chan *stt.SpeechEvent, 4),
		providerManagedEndpointing: true,
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			controlMessages <- control
			return nil
		},
		closeConn: func() error { return nil },
		state:     &speechmaticsStreamState{},
	}
	defer stream.Close()

	stream.scheduleLocalEndpointingForceEndOfUtterance()
	if ok := stream.handleResponse(smResponse{Message: "StartOfTurn"}); !ok {
		t.Fatal("StartOfTurn stopped read loop")
	}
	select {
	case message := <-controlMessages:
		t.Fatalf("control message after provider StartOfTurn = %#v, want canceled delayed ForceEndOfUtterance", message)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADFinalizeWaitsForRecognitionStarted(t *testing.T) {
	var writes []string
	stream := &speechmaticsSTTStream{
		waitForRecognitionStarted:  true,
		providerManagedEndpointing: true,
		pendingAudioChunks:         [][]byte{speechmaticsTestInt16PCM(1600)},
		writeBinary: func(data []byte) error {
			writes = append(writes, fmt.Sprintf("audio:%d", len(data)))
			return nil
		},
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			writes = append(writes, fmt.Sprintf("control:%s", control["message"]))
			return nil
		},
	}

	if err := stream.sendLocalEndpointingForceEndOfUtterance(); err != nil {
		t.Fatalf("sendLocalEndpointingForceEndOfUtterance before ready error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes before RecognitionStarted = %#v, want pending local endpointing finalize", writes)
	}
	if err := stream.markReadyForAudio(); err != nil {
		t.Fatalf("markReadyForAudio error = %v", err)
	}

	want := []string{"audio:3200", "control:ForceEndOfUtterance"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after RecognitionStarted = %#v, want %#v", writes, want)
	}
}

func TestSpeechmaticsSTTAdaptiveLocalVADStartCancelsPendingStartupFinalize(t *testing.T) {
	var writes []string
	stream := &speechmaticsSTTStream{
		waitForRecognitionStarted:  true,
		providerManagedEndpointing: true,
		pendingAudioChunks:         [][]byte{speechmaticsTestInt16PCM(1600)},
		writeBinary: func(data []byte) error {
			writes = append(writes, fmt.Sprintf("audio:%d", len(data)))
			return nil
		},
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			writes = append(writes, fmt.Sprintf("control:%s", control["message"]))
			return nil
		},
	}

	if err := stream.sendLocalEndpointingForceEndOfUtterance(); err != nil {
		t.Fatalf("sendLocalEndpointingForceEndOfUtterance before ready error = %v", err)
	}
	stream.handleVADStartOfSpeech()
	if err := stream.markReadyForAudio(); err != nil {
		t.Fatalf("markReadyForAudio error = %v", err)
	}

	want := []string{"audio:3200"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after restarted speech before RecognitionStarted = %#v, want %#v", writes, want)
	}
}

func TestSpeechmaticsSTTVADEndOfSpeechFinalizesReferenceExternalTurn(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdaptiveTurnDetection(),
		WithSpeechmaticsSTTVAD(&fakeSpeechmaticsVAD{stream: vadStream}),
	)
	if provider.turnDetectionMode != "external" {
		t.Fatalf("turn detection mode = %q, want external when explicit VAD is provided", provider.turnDetectionMode)
	}

	controlMessages := make(chan map[string]interface{}, 4)
	stream := &speechmaticsSTTStream{
		owner: provider,
		writeBinary: func([]byte) error {
			return nil
		},
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			controlMessages <- control
			return nil
		},
	}
	if err := stream.startVAD(context.Background()); err != nil {
		t.Fatalf("startVAD() error = %v", err)
	}
	defer stream.Close()

	frame := &model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if got := vadStream.pushedFrames()[0]; got != frame {
		t.Fatalf("VAD pushed frame = %#v, want original pre-normalized frame", got)
	}

	vadStream.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	waitForSpeechmaticsControlMessage(t, controlMessages, "ForceEndOfUtterance")
}

func TestSpeechmaticsSTTVADFinalizeFailureSurfacesToNext(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	finalizeErr := errors.New("finalize failed")
	transportClosed := make(chan struct{})
	var closeOnce sync.Once
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		writeJSON: func(interface{}) error {
			return finalizeErr
		},
		closeConn: func() error {
			closeOnce.Do(func() { close(transportClosed) })
			return nil
		},
	}
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTVAD(&fakeSpeechmaticsVAD{stream: vadStream}),
	)
	provider.registerStream(stream)
	if err := stream.startVAD(context.Background()); err != nil {
		t.Fatalf("startVAD error = %v", err)
	}

	vadStream.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	select {
	case <-transportClosed:
	case <-time.After(time.Second):
		t.Fatal("VAD finalize failure did not close Speechmatics stream transport")
	}
	if _, err := stream.Next(); !errors.Is(err, finalizeErr) {
		t.Fatalf("Next after VAD finalize failure = %v, want %v", err, finalizeErr)
	}
}

func TestSpeechmaticsSTTExplicitVADForcesReferenceExternalTurnDetectionAfterLaterModeOptions(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTVAD(&fakeSpeechmaticsVAD{stream: newFakeSpeechmaticsVADStream()}),
		WithSpeechmaticsSTTFixedTurnDetection(),
		WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(0.6),
	)
	if provider.turnDetectionMode != "external" {
		t.Fatalf("turn detection mode = %q, want external when explicit VAD is provided", provider.turnDetectionMode)
	}

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	if _, ok := config["conversation_config"]; ok {
		t.Fatalf("conversation_config = %#v, want omitted because explicit VAD owns endpointing", config["conversation_config"])
	}

	var controlMessages []map[string]interface{}
	stream := &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			controlMessages = append(controlMessages, control)
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	if len(controlMessages) != 1 || controlMessages[0]["message"] != "ForceEndOfUtterance" {
		t.Fatalf("control messages = %#v, want ForceEndOfUtterance", controlMessages)
	}
}

func TestSpeechmaticsSTTVADErrorClosesReferenceStream(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	vadStream.setNextErr(errors.New("vad failed"))
	nextStarted := make(chan struct{})
	vadStream.setNextStarted(nextStarted)
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTVAD(&fakeSpeechmaticsVAD{stream: vadStream}),
	)
	transportClosed := make(chan struct{})
	var closeOnce sync.Once
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		closeConn: func() error {
			closeOnce.Do(func() { close(transportClosed) })
			return nil
		},
	}
	provider.registerStream(stream)

	if err := stream.startVAD(context.Background()); err != nil {
		t.Fatalf("startVAD error = %v", err)
	}
	select {
	case <-nextStarted:
	case <-time.After(time.Second):
		t.Fatal("VAD stream did not start")
	}
	select {
	case <-transportClosed:
	case <-time.After(time.Second):
		t.Fatal("VAD error did not close Speechmatics stream transport")
	}
	if _, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "vad failed") {
		t.Fatalf("Next after VAD error = %v, want reference VAD failure", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01}}); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after VAD error = %v, want reference input-ended error", err)
	}
}

func TestSpeechmaticsSTTVADPushFailureSurfacesToNext(t *testing.T) {
	vadErr := errors.New("vad push failed")
	vadStream := newFakeSpeechmaticsVADStream()
	vadStream.setPushErr(vadErr)
	transportClosed := make(chan struct{})
	var closeOnce sync.Once
	stream := &speechmaticsSTTStream{
		events:    make(chan *stt.SpeechEvent, 1),
		errCh:     make(chan error, 1),
		done:      make(chan struct{}),
		vadStream: vadStream,
		closeConn: func() error {
			closeOnce.Do(func() { close(transportClosed) })
			return nil
		},
		writeBinary: func([]byte) error {
			return nil
		},
	}

	err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	})
	if !errors.Is(err, vadErr) {
		t.Fatalf("PushFrame VAD failure error = %v, want %v", err, vadErr)
	}
	select {
	case <-transportClosed:
	case <-time.After(time.Second):
		t.Fatal("VAD PushFrame failure did not close Speechmatics stream transport")
	}
	if _, err := stream.Next(); !errors.Is(err, vadErr) {
		t.Fatalf("Next after VAD PushFrame failure = %v, want %v", err, vadErr)
	}
}

func TestSpeechmaticsSTTVADEndInputFailureSurfacesToNext(t *testing.T) {
	vadErr := errors.New("vad end input failed")
	vadStream := newFakeSpeechmaticsVADStream()
	vadStream.setEndInputErr(vadErr)
	transportClosed := make(chan struct{})
	var closeOnce sync.Once
	stream := &speechmaticsSTTStream{
		events:    make(chan *stt.SpeechEvent, 1),
		errCh:     make(chan error, 1),
		done:      make(chan struct{}),
		vadStream: vadStream,
		closeConn: func() error {
			closeOnce.Do(func() { close(transportClosed) })
			return nil
		},
		writeJSON: func(interface{}) error {
			return nil
		},
	}

	err := stream.EndInput()
	if !errors.Is(err, vadErr) {
		t.Fatalf("EndInput VAD failure error = %v, want %v", err, vadErr)
	}
	select {
	case <-transportClosed:
	case <-time.After(time.Second):
		t.Fatal("VAD EndInput failure did not close Speechmatics stream transport")
	}
	if _, err := stream.Next(); !errors.Is(err, vadErr) {
		t.Fatalf("Next after VAD EndInput failure = %v, want %v", err, vadErr)
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

func TestSpeechmaticsPushFrameUsesReferenceMonoProviderChunks(t *testing.T) {
	var writes [][]byte
	stream := &speechmaticsSTTStream{
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}
	audioData := make([]byte, 8000)
	for i := range audioData {
		audioData[i] = byte(i)
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        16000,
		NumChannels:       2,
		SamplesPerChannel: 2000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("binary writes after stereo PushFrame = %d, want two reference mono 100ms chunks", len(writes))
	}
	if got := len(writes[0]); got != 3200 {
		t.Fatalf("first chunk length = %d, want 3200 reference mono bytes", got)
	}
	if got := len(writes[1]); got != 3200 {
		t.Fatalf("second chunk length = %d, want 3200 reference mono bytes", got)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(writes) != 3 {
		t.Fatalf("binary writes after Flush = %d, want stereo remainder chunk", len(writes))
	}
	if got := len(writes[2]); got != 1600 {
		t.Fatalf("flush chunk length = %d, want remaining reference mono bytes", got)
	}
}

func TestSpeechmaticsSTTStreamResamplesInputAudioToReferenceRate(t *testing.T) {
	var writes [][]byte
	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTSampleRate(16000)),
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}
	audioData := speechmaticsTestInt16PCM(480)

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("binary writes after PushFrame = %d, want resampled frame buffered below 100ms chunk", len(writes))
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("binary writes after Flush = %d, want one resampled remainder chunk", len(writes))
	}
	want := speechmaticsEveryNthInt16PCM(480, 3)
	if got := writes[0]; !bytes.Equal(got, want) {
		t.Fatalf("flushed binary data = %#v, want 48k->16k reference resampled PCM", got)
	}
}

func TestSpeechmaticsSTTStreamResamplingIsReferenceChunkInvariant(t *testing.T) {
	whole := speechmaticsResampledBytes(t, []int{441})
	split := speechmaticsResampledBytes(t, []int{220, 221})

	if !bytes.Equal(split, whole) {
		t.Fatalf("split resampled PCM differs from whole frame\nsplit=%#v\nwhole=%#v", split, whole)
	}
}

func TestSpeechmaticsSTTStreamRejectsReferenceEmptyFrameSampleRateChange(t *testing.T) {
	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTSampleRate(16000)),
		writeBinary: func([]byte) error {
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              speechmaticsTestInt16PCM(160),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); err != nil {
		t.Fatalf("first PushFrame() error = %v", err)
	}

	err := stream.PushFrame(&model.AudioFrame{
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 0,
	})
	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("empty-frame PushFrame() error = %v, want reference sample-rate consistency error", err)
	}
}

func TestSpeechmaticsPushFrameWaitsForReferenceRecognitionStarted(t *testing.T) {
	var writes [][]byte
	stream := &speechmaticsSTTStream{
		waitForRecognitionStarted: true,
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
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
	if len(writes) != 0 {
		t.Fatalf("binary writes before RecognitionStarted = %d, want buffered audio", len(writes))
	}

	if keepReading := stream.handleResponse(smResponse{Message: "RecognitionStarted"}); !keepReading {
		t.Fatal("RecognitionStarted stopped read loop")
	}
	if len(writes) != 1 {
		t.Fatalf("binary writes after RecognitionStarted = %d, want buffered chunk", len(writes))
	}
	if got := len(writes[0]); got != 3200 {
		t.Fatalf("buffered chunk length = %d, want 3200", got)
	}
}

func TestSpeechmaticsSTTStartupDrainWriteFailureSurfacesToNext(t *testing.T) {
	writeErr := errors.New("startup write failed")
	stream := &speechmaticsSTTStream{
		events:                    make(chan *stt.SpeechEvent, 1),
		errCh:                     make(chan error, 1),
		done:                      make(chan struct{}),
		waitForRecognitionStarted: true,
		writeBinary: func([]byte) error {
			return writeErr
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
	if keepReading := stream.handleResponse(smResponse{Message: "RecognitionStarted"}); keepReading {
		t.Fatal("RecognitionStarted kept read loop after startup drain write failure")
	}
	if _, err := stream.Next(); !errors.Is(err, writeErr) {
		t.Fatalf("Next() error after startup drain write failure = %v, want %v", err, writeErr)
	}
}

func TestSpeechmaticsPushFrameForwardsReferenceEmptyFrameToVAD(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	stream := &speechmaticsSTTStream{
		vadStream: vadStream,
		writeBinary: func(data []byte) error {
			t.Fatalf("provider audio write = %d bytes, want no provider write for empty frame", len(data))
			return nil
		},
	}
	frame := &model.AudioFrame{
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 0,
	}

	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame empty frame error = %v", err)
	}
	pushed := vadStream.pushedFrames()
	if len(pushed) != 1 {
		t.Fatalf("VAD pushed frames = %d, want reference empty frame forwarded", len(pushed))
	}
	if pushed[0] != frame {
		t.Fatalf("VAD frame = %#v, want original empty frame", pushed[0])
	}
}

func TestSpeechmaticsPushFrameWaitsForReferenceRecognitionStartedBeforeVAD(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	frame := &model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	var writes [][]byte
	stream := &speechmaticsSTTStream{
		waitForRecognitionStarted: true,
		vadStream:                 vadStream,
		writeBinary: func(data []byte) error {
			if vadStream.isEnded() {
				t.Fatal("provider audio wrote after VAD EndInput, want provider audio before VAD end input")
			}
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
		writeJSON: func(interface{}) error {
			return nil
		},
	}

	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if pushed := vadStream.pushedFrames(); len(pushed) != 0 {
		t.Fatalf("VAD pushed frames before RecognitionStarted = %d, want buffered original audio", len(pushed))
	}
	if len(writes) != 0 {
		t.Fatalf("provider writes before RecognitionStarted = %d, want buffered provider audio", len(writes))
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() before RecognitionStarted error = %v", err)
	}
	if vadStream.isEnded() {
		t.Fatal("VAD EndInput before RecognitionStarted = true, want buffered end input")
	}

	if keepReading := stream.handleResponse(smResponse{Message: "RecognitionStarted"}); !keepReading {
		t.Fatal("RecognitionStarted stopped read loop")
	}
	if pushed := vadStream.pushedFrames(); len(pushed) != 1 || pushed[0] != frame {
		t.Fatalf("VAD pushed frames after RecognitionStarted = %#v, want original frame", pushed)
	}
	if !vadStream.isEnded() {
		t.Fatal("VAD EndInput after RecognitionStarted = false, want drained end input after frames")
	}
	if len(writes) != 1 {
		t.Fatalf("provider writes after RecognitionStarted = %d, want buffered provider audio", len(writes))
	}
}

func TestSpeechmaticsCloseUnblocksReferenceVADStartupDrain(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	pushStarted := make(chan struct{})
	vadStream.setPushStarted(pushStarted)
	frame := &model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	stream := &speechmaticsSTTStream{
		waitForRecognitionStarted: true,
		vadStream:                 vadStream,
		writeBinary: func([]byte) error {
			return nil
		},
		writeJSON: func(interface{}) error {
			return nil
		},
	}

	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	readyDone := make(chan struct{})
	go func() {
		stream.handleResponse(smResponse{Message: "RecognitionStarted"})
		close(readyDone)
	}()
	select {
	case <-pushStarted:
	case <-time.After(time.Second):
		t.Fatal("VAD startup drain did not begin")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- stream.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		vadStream.events <- &vad.VADEvent{}
		<-readyDone
		err := <-closeDone
		t.Fatalf("Close() blocked behind pending VAD startup drain, later error = %v", err)
	}
	select {
	case <-readyDone:
	case <-time.After(time.Second):
		t.Fatal("RecognitionStarted handler stayed blocked after Close")
	}
}

func TestSpeechmaticsSTTEndInputFlushesAndEndsReferenceInput(t *testing.T) {
	var writes [][]byte
	var controlMessages []map[string]interface{}
	stream := &speechmaticsSTTStream{
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			controlMessages = append(controlMessages, control)
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 800),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 400,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes before EndInput = %d, want buffered partial chunk", len(writes))
	}

	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if len(writes) != 1 || len(writes[0]) != 800 {
		t.Fatalf("writes after EndInput = %#v, want flushed tail", writes)
	}
	if len(controlMessages) != 1 || controlMessages[0]["message"] != "EndOfStream" {
		t.Fatalf("control messages = %#v, want EndOfStream", controlMessages)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame after EndInput returned nil, want stream input ended error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush after EndInput returned nil, want stream input ended error")
	}
	if stream.isClosed() {
		t.Fatal("EndInput marked stream closed, want read side open for final provider messages")
	}
}

func TestSpeechmaticsSTTEndInputWaitsForRecognitionStartedBeforeEndStream(t *testing.T) {
	var ordered []string
	stream := &speechmaticsSTTStream{
		waitForRecognitionStarted: true,
		writeBinary: func(data []byte) error {
			ordered = append(ordered, fmt.Sprintf("audio:%d", len(data)))
			return nil
		},
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			ordered = append(ordered, fmt.Sprintf("control:%s", control["message"]))
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 800),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 400,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if len(ordered) != 0 {
		t.Fatalf("writes before RecognitionStarted = %#v, want input buffered", ordered)
	}

	if keepReading := stream.handleResponse(smResponse{Message: "RecognitionStarted"}); !keepReading {
		t.Fatal("RecognitionStarted stopped read loop")
	}
	want := []string{"audio:800", "control:EndOfStream"}
	if !reflect.DeepEqual(ordered, want) {
		t.Fatalf("writes after RecognitionStarted = %#v, want %#v", ordered, want)
	}
}

func TestSpeechmaticsSTTFinalizeWaitsForRecognitionStartedBeforeForceEOU(t *testing.T) {
	var ordered []string
	stream := &speechmaticsSTTStream{
		waitForRecognitionStarted: true,
		writeBinary: func(data []byte) error {
			ordered = append(ordered, fmt.Sprintf("audio:%d", len(data)))
			return nil
		},
		writeJSON: func(message interface{}) error {
			control, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("control message = %#v, want JSON object", message)
			}
			ordered = append(ordered, fmt.Sprintf("control:%s", control["message"]))
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
	if err := stream.Finalize(); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if len(ordered) != 0 {
		t.Fatalf("writes before RecognitionStarted = %#v, want finalize buffered behind audio", ordered)
	}

	if keepReading := stream.handleResponse(smResponse{Message: "RecognitionStarted"}); !keepReading {
		t.Fatal("RecognitionStarted stopped read loop")
	}
	want := []string{"audio:3200", "control:ForceEndOfUtterance"}
	if !reflect.DeepEqual(ordered, want) {
		t.Fatalf("writes after RecognitionStarted = %#v, want %#v", ordered, want)
	}
}

func TestSpeechmaticsSTTCloseAfterEndInputDoesNotDuplicateReferenceEndStream(t *testing.T) {
	messages := make(chan string, 3)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var message map[string]interface{}
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			if value, _ := message["message"].(string); value != "" {
				messages <- value
				if value == "StartRecognition" {
					if err := conn.WriteJSON(map[string]interface{}{"message": "RecognitionStarted"}); err != nil {
						t.Errorf("write RecognitionStarted: %v", err)
						return
					}
				}
			}
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL(wsURL))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	concrete, ok := stream.(*speechmaticsSTTStream)
	if !ok {
		t.Fatalf("stream %T is not *speechmaticsSTTStream", stream)
	}
	if got := <-messages; got != "StartRecognition" {
		t.Fatalf("first control message = %q, want StartRecognition", got)
	}
	waitSpeechmaticsStreamReady(t, concrete)

	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatalf("stream %T does not implement stt.InputEnding", stream)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got := drainSpeechmaticsControlMessages(messages)
	if !reflect.DeepEqual(got, []string{"EndOfStream"}) {
		t.Fatalf("control messages = %#v, want single EndOfStream", got)
	}
}

func TestSpeechmaticsSTTCloseBeforeEndInputDoesNotSendReferenceEndStream(t *testing.T) {
	messages := make(chan string, 3)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var message map[string]interface{}
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			if value, _ := message["message"].(string); value != "" {
				messages <- value
				if value == "StartRecognition" {
					if err := conn.WriteJSON(map[string]interface{}{"message": "RecognitionStarted"}); err != nil {
						t.Errorf("write RecognitionStarted: %v", err)
						return
					}
				}
			}
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL(wsURL))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if got := <-messages; got != "StartRecognition" {
		t.Fatalf("first control message = %q, want StartRecognition", got)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got := drainSpeechmaticsControlMessages(messages)
	if len(got) != 0 {
		t.Fatalf("control messages after Close = %#v, want no EndOfStream", got)
	}
}

func drainSpeechmaticsControlMessages(messages <-chan string) []string {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	var got []string
	for {
		select {
		case message := <-messages:
			got = append(got, message)
		case <-timer.C:
			return got
		}
	}
}

func waitForSpeechmaticsControlMessage(t *testing.T, messages <-chan map[string]interface{}, want string) {
	t.Helper()
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	var seen []map[string]interface{}
	for {
		select {
		case message := <-messages:
			seen = append(seen, message)
			if message["message"] == want {
				return
			}
		case <-timer.C:
			t.Fatalf("control messages = %#v, want %s", seen, want)
		}
	}
}

func waitSpeechmaticsStreamReady(t *testing.T, stream *speechmaticsSTTStream) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		ready := stream.audioReady
		stream.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stream did not process RecognitionStarted")
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

func TestSpeechmaticsSTTRejectsSampleRateChangeBeforeVADLikeReference(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	stream := &speechmaticsSTTStream{
		vadStream: vadStream,
		writeBinary: func([]byte) error {
			return nil
		},
	}
	first := &model.AudioFrame{
		Data:              make([]byte, 3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	if err := stream.PushFrame(first); err != nil {
		t.Fatalf("first PushFrame error = %v", err)
	}
	second := &model.AudioFrame{
		Data:              make([]byte, 9600),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 4800,
	}
	err := stream.PushFrame(second)
	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("second PushFrame error = %v, want reference sample-rate consistency error", err)
	}
	if pushed := vadStream.pushedFrames(); len(pushed) != 1 {
		t.Fatalf("VAD pushed frames = %d, want only first valid sample-rate frame", len(pushed))
	}
}

func TestSpeechmaticsSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
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
	if _, err := stream.Next(); !errors.Is(err, writeErr) {
		t.Fatalf("Next after write failure error = %v, want %v", err, writeErr)
	}
	if err := stream.PushFrame(frame); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after write failure error = %v, want reference input-ended error", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("Flush after write failure error = %v, want reference input-ended error", err)
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

func TestSpeechmaticsSTTPreservesReferenceZeroSampleRate(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTSampleRate(0))

	if got := provider.InputSampleRate(); got != 0 {
		t.Fatalf("InputSampleRate = %d, want explicit reference sample rate 0", got)
	}

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	audioFormat := message["audio_format"].(map[string]interface{})
	if audioFormat["sample_rate"] != 0 {
		t.Fatalf("sample_rate = %#v, want explicit reference sample rate 0", audioFormat["sample_rate"])
	}
}

func TestSpeechmaticsSTTPreservesReferenceNegativeSampleRate(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTSampleRate(-1))

	if provider.sampleRate != -1 {
		t.Fatalf("sample rate = %d, want explicit reference sample rate -1", provider.sampleRate)
	}
	if got := provider.InputSampleRate(); got != 0 {
		t.Fatalf("InputSampleRate = %d, want no silent 16 kHz fallback for negative sample rate", got)
	}

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	audioFormat := message["audio_format"].(map[string]interface{})
	if audioFormat["sample_rate"] != -1 {
		t.Fatalf("sample_rate = %#v, want explicit reference sample rate -1", audioFormat["sample_rate"])
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

func TestSpeechmaticsSTTStreamDialFailureReturnsReferenceConnectionError(t *testing.T) {
	withSpeechmaticsSTTRetryInterval(t, 0)
	oldDialer := websocket.DefaultDialer
	attempts := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			attempts++
			return nil, errors.New("dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws://speechmatics.example/v2"))
	stream, err := provider.Stream(context.Background(), "")
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil on dial failure", stream)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "failed to recognize speech after 3 attempts" {
		t.Fatalf("APIConnectionError message = %q, want reference exhausted-retry summary", connectionErr.Message)
	}
	if attempts != 4 {
		t.Fatalf("dial attempts = %d, want initial attempt plus 3 reference retries", attempts)
	}
}

func TestSpeechmaticsSTTStreamRetriesReferenceTransientDialFailure(t *testing.T) {
	withSpeechmaticsSTTRetryInterval(t, 0)
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
		}
	}))
	defer server.Close()

	oldDialer := websocket.DefaultDialer
	var attempts int
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("transient dial failed")
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, addr)
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %T %v, want retry success", err, err)
	}
	defer stream.Close()
	if attempts != 2 {
		t.Fatalf("dial attempts = %d, want one reference retry after transient failure", attempts)
	}
}

func TestSpeechmaticsSTTStreamRetryAccumulatesReferenceStartTimeOffset(t *testing.T) {
	const retryDelay = 25 * time.Millisecond
	withSpeechmaticsSTTRetryInterval(t, retryDelay)
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
		}
	}))
	defer server.Close()

	oldDialer := websocket.DefaultDialer
	var attempts int
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("transient dial failed")
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, addr)
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %T %v, want retry success", err, err)
	}
	defer stream.Close()
	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatal("Speechmatics stream does not expose reference timing anchors")
	}
	if got := timing.StartTimeOffset(); got < retryDelay.Seconds() {
		t.Fatalf("StartTimeOffset = %.6f, want at least retry delay %.6f after startup retry", got, retryDelay.Seconds())
	}
}

func TestSpeechmaticsSTTStreamCallerCancelReturnsContextCanceled(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws://speechmatics.example/v2"))

	stream, err := provider.Stream(ctx, "")
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil after caller cancellation", stream)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream error = %T %v, want context.Canceled", err, err)
	}
}

func withSpeechmaticsSTTRetryInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	previous := speechmaticsSTTRetryInterval
	speechmaticsSTTRetryInterval = func(int) time.Duration { return interval }
	t.Cleanup(func() { speechmaticsSTTRetryInterval = previous })
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

func TestSpeechmaticsSTTAllowsReferenceEndOfUtteranceMaxDelayWithoutTrigger(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTEndOfUtteranceMaxDelay(0.4))

	if err := validateSpeechmaticsSTTOptions(provider); err != nil {
		t.Fatalf("validateSpeechmaticsSTTOptions() error = %v, want nil when only end_of_utterance_max_delay is set", err)
	}
}

func TestSpeechmaticsSTTStreamAllowsReferenceSampleRatesToReachProvider(t *testing.T) {
	withSpeechmaticsSTTRetryInterval(t, 0)
	tests := []struct {
		name       string
		sampleRate int
	}{
		{name: "zero", sampleRate: 0},
		{name: "negative", sampleRate: -1},
		{name: "unsupported high", sampleRate: 48000},
	}

	oldDialer := websocket.DefaultDialer
	dials := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialsBefore := dials
			provider := NewSpeechmaticsSTT("test-key",
				WithSpeechmaticsSTTBaseURL("ws://speechmatics.example/v2"),
				WithSpeechmaticsSTTSampleRate(tt.sampleRate),
			)

			stream, err := provider.Stream(context.Background(), "")
			if stream != nil {
				t.Fatalf("Stream = %#v, want nil on dial failure", stream)
			}
			var connectionErr *llm.APIConnectionError
			if !errors.As(err, &connectionErr) {
				t.Fatalf("Stream error = %T %v, want provider dial APIConnectionError", err, err)
			}
			if dials <= dialsBefore {
				t.Fatalf("dials = %d, want provider dial for reference sample_rate %d", dials, tt.sampleRate)
			}
		})
	}
}

func TestSpeechmaticsSTTStreamAllowsReferenceDisabledDiarizationOptionsToReachProvider(t *testing.T) {
	withSpeechmaticsSTTRetryInterval(t, 0)
	tests := []struct {
		name string
		opts []SpeechmaticsSTTOption
	}{
		{
			name: "focus speakers",
			opts: []SpeechmaticsSTTOption{
				WithSpeechmaticsSTTEnableDiarization(false),
				WithSpeechmaticsSTTSpeakerFocus([]string{"agent"}, nil, "retain"),
			},
		},
		{
			name: "ignore speakers",
			opts: []SpeechmaticsSTTOption{
				WithSpeechmaticsSTTEnableDiarization(false),
				WithSpeechmaticsSTTSpeakerFocus(nil, []string{"noise"}, "retain"),
			},
		},
		{
			name: "max speakers",
			opts: []SpeechmaticsSTTOption{
				WithSpeechmaticsSTTEnableDiarization(false),
				WithSpeechmaticsSTTMaxSpeakers(3),
			},
		},
	}

	oldDialer := websocket.DefaultDialer
	dials := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialsBefore := dials
			opts := append([]SpeechmaticsSTTOption{WithSpeechmaticsSTTBaseURL("ws://speechmatics.example/v2")}, tt.opts...)
			provider := NewSpeechmaticsSTT("test-key", opts...)
			if err := validateSpeechmaticsSTTOptions(provider); err != nil {
				t.Fatalf("validateSpeechmaticsSTTOptions() error = %v, want reference config accepted", err)
			}
			message := buildSpeechmaticsSTTStartMessage(provider, "")
			config := message["transcription_config"].(map[string]interface{})
			if _, ok := config["diarization"]; ok {
				t.Fatalf("diarization = %#v, want omitted when reference diarization disabled", config["diarization"])
			}
			if _, ok := config["speaker_diarization_config"]; ok {
				t.Fatalf("speaker_diarization_config = %#v, want omitted when reference diarization disabled", config["speaker_diarization_config"])
			}

			stream, err := provider.Stream(context.Background(), "")
			if stream != nil {
				t.Fatalf("Stream = %#v, want nil on dial failure", stream)
			}
			var connectionErr *llm.APIConnectionError
			if !errors.As(err, &connectionErr) {
				t.Fatalf("Stream error = %T %v, want provider dial APIConnectionError", err, err)
			}
			if dials <= dialsBefore {
				t.Fatalf("dials = %d, want provider dial for reference disabled-diarization options", dials)
			}
		})
	}
}

func TestSpeechmaticsSTTStreamSeedsReferenceStartTime(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		var message map[string]interface{}
		_ = conn.ReadJSON(&message)
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws"+strings.TrimPrefix(server.URL, "http")))
	before := float64(time.Now().UnixNano()) / 1e9
	stream, err := provider.Stream(context.Background(), "")
	after := float64(time.Now().UnixNano()) / 1e9
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	timing, ok := stream.(interface{ StartTime() float64 })
	if !ok {
		t.Fatalf("stream %T does not expose start time", stream)
	}
	if got := timing.StartTime(); got < before || got > after {
		t.Fatalf("StartTime() = %v, want between %v and %v", got, before, after)
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
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after provider Close = %v, want reference input-ended error", err)
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

func TestSpeechmaticsSTTFinalizeIncludesReferenceCumulativeAudioTimestamp(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	var writes []map[string]interface{}
	stream := &speechmaticsSTTStream{
		writeBinary: func([]byte) error { return nil },
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
	t.Cleanup(func() { _ = stream.Close() })

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              speechmaticsTestInt16PCM(1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("first PushFrame error = %v", err)
	}
	for _, event := range speechmaticsEndOfTurnEvents(stream.state) {
		if event.Type == stt.SpeechEventRecognitionUsage && event.RecognitionUsage.AudioDuration != 0.1 {
			t.Fatalf("first turn usage = %#v, want 0.1s", event.RecognitionUsage)
		}
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              speechmaticsTestInt16PCM(3200),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 3200,
	}); err != nil {
		t.Fatalf("second PushFrame error = %v", err)
	}

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("finalize writes = %d, want one", len(writes))
	}
	timestamp, ok := writes[0]["timestamp"].(float64)
	if !ok {
		t.Fatalf("timestamp = %#v, want reference audio_seconds_sent", writes[0]["timestamp"])
	}
	if timestamp < 0.299 || timestamp > 0.301 {
		t.Fatalf("timestamp = %v, want cumulative reference audio_seconds_sent 0.3", timestamp)
	}
}

func TestSpeechmaticsSTTFinalizeFailureSurfacesToNext(t *testing.T) {
	finalizeErr := errors.New("manual finalize failed")
	transportClosed := make(chan struct{})
	var closeOnce sync.Once
	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		writeJSON: func(interface{}) error {
			return finalizeErr
		},
		closeConn: func() error {
			closeOnce.Do(func() { close(transportClosed) })
			return nil
		},
	}
	provider.registerStream(stream)

	err := provider.Finalize()
	if !errors.Is(err, finalizeErr) {
		t.Fatalf("Finalize failure error = %v, want %v", err, finalizeErr)
	}
	select {
	case <-transportClosed:
	case <-time.After(time.Second):
		t.Fatal("Finalize failure did not close Speechmatics stream transport")
	}
	if _, err := stream.Next(); !errors.Is(err, finalizeErr) {
		t.Fatalf("Next after Finalize failure = %v, want %v", err, finalizeErr)
	}
}

func TestSpeechmaticsSTTFinalizeTimesOutReferenceForcedEOU(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 2),
		state:  &speechmaticsStreamState{speechDuration: 0.25, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			payload, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("finalize message = %#v, want JSON object", message)
			}
			if got, want := payload["message"], "ForceEndOfUtterance"; got != want {
				t.Fatalf("finalize message = %#v, want %#v", got, want)
			}
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}

	var event *stt.SpeechEvent
	select {
	case event = <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("forced EOU timeout did not emit end_of_speech")
	}
	if event.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event type = %s, want end_of_speech", event.Type)
	}
	var usage *stt.SpeechEvent
	select {
	case usage = <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("forced EOU timeout did not emit recognition usage")
	}
	if usage.Type != stt.SpeechEventRecognitionUsage || usage.RecognitionUsage == nil || usage.RecognitionUsage.AudioDuration != 0.25 {
		t.Fatalf("usage event = %#v, want reference forced EOU timeout usage", usage)
	}
}

func TestSpeechmaticsSTTFinalizeTimeoutWithoutSpeechSuppressesReferenceEndOfTurn(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 2),
		state:  &speechmaticsStreamState{},
		writeJSON: func(message interface{}) error {
			payload, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("finalize message = %#v, want JSON object", message)
			}
			if got, want := payload["message"], "ForceEndOfUtterance"; got != want {
				t.Fatalf("finalize message = %#v, want %#v", got, want)
			}
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}

	select {
	case event := <-stream.events:
		t.Fatalf("forced EOU timeout without speech emitted %#v, want no reference end_of_speech", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSpeechmaticsSTTFinalizeTimeoutWithoutTranscriptSuppressesReferenceEndOfTurn(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 2),
		state:  &speechmaticsStreamState{speechDuration: 0.25},
		writeJSON: func(message interface{}) error {
			payload, ok := message.(map[string]interface{})
			if !ok {
				t.Fatalf("finalize message = %#v, want JSON object", message)
			}
			if got, want := payload["message"], "ForceEndOfUtterance"; got != want {
				t.Fatalf("finalize message = %#v, want %#v", got, want)
			}
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}

	select {
	case event := <-stream.events:
		t.Fatalf("forced EOU timeout without transcript emitted %#v, want no reference end_of_speech", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSpeechmaticsSTTLateEndOfTurnAfterForcedEOUTimeoutDoesNotDuplicate(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{speechDuration: 0.25, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	select {
	case <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("forced EOU timeout did not emit end_of_speech")
	}
	select {
	case <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("forced EOU timeout did not emit recognition usage")
	}
	if ok := stream.handleResponse(smResponse{Message: "EndOfTurn"}); !ok {
		t.Fatal("late EndOfTurn stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("late EndOfTurn events = %d, want no duplicate end_of_speech", len(stream.events))
	}
}

func TestSpeechmaticsSTTLateEndOfTurnAfterForcedEOUTimeoutAndNewStartDoesNotDuplicate(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{speechDuration: 0.25, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	select {
	case <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("forced EOU timeout did not emit end_of_speech")
	}
	select {
	case <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("forced EOU timeout did not emit recognition usage")
	}
	if ok := stream.handleResponse(smResponse{Message: "StartOfTurn"}); !ok {
		t.Fatal("StartOfTurn stopped read loop")
	}
	if got := <-stream.events; got.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("new turn event = %s, want start_of_speech", got.Type)
	}
	if ok := stream.handleResponse(smResponse{Message: "EndOfTurn"}); !ok {
		t.Fatal("late EndOfTurn stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("late EndOfTurn events after new start = %d, want no duplicate end_of_speech", len(stream.events))
	}
}

func TestSpeechmaticsSTTStaleForcedEOUAckAfterNewTranscriptDoesNotEndTurn(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = time.Second
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{speechDuration: 0.25, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	if ok := stream.handleResponse(smResponse{Message: "StartOfTurn"}); !ok {
		t.Fatal("StartOfTurn stopped read loop")
	}
	if got := readSpeechmaticsTestEvent(t, stream.events); got.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("new turn event = %s, want start_of_speech", got.Type)
	}

	var newTranscript smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[{
			"text":"new words",
			"language":"en",
			"speaker_id":"agent",
			"metadata":{"start_time":0.3,"end_time":0.6}
		}]
	}`), &newTranscript); err != nil {
		t.Fatalf("unmarshal new transcript: %v", err)
	}
	if ok := stream.handleResponse(newTranscript); !ok {
		t.Fatal("AddSegment stopped read loop")
	}
	if got := readSpeechmaticsTestEvent(t, stream.events); got.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("new transcript event = %s, want final transcript", got.Type)
	}

	if ok := stream.handleResponse(smResponse{Message: "EndOfUtterance"}); !ok {
		t.Fatal("stale EndOfUtterance stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("stale EndOfUtterance after new transcript events = %d, want no turn end", len(stream.events))
	}
}

func TestSpeechmaticsSTTForcedEOUSuppressesReferencePartialSegments(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = time.Second
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{includePartials: true},
		writeJSON: func(message interface{}) error {
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	resp := smResponse{}
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialSegment",
		"segments":[{"text":"still talking","metadata":{"start_time":0.1,"end_time":0.4}}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal partial segment: %v", err)
	}
	if ok := stream.handleResponse(resp); !ok {
		t.Fatal("AddPartialSegment stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("forced EOU partial events = %d, want suppressed reference partial", len(stream.events))
	}
}

func TestSpeechmaticsSTTForcedEOURawPartialFlushesFinalOnly(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = time.Second
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state: &speechmaticsStreamState{
			bufferRawFinals: true,
			includePartials: true,
		},
		writeJSON: func(message interface{}) error {
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	finalResp := smResponse{}
	if err := json.Unmarshal([]byte(`{
		"message":"AddTranscript",
		"results":[{"type":"word","start_time":0.1,"end_time":0.4,"alternatives":[{"content":"done","confidence":0.9}]}]
	}`), &finalResp); err != nil {
		t.Fatalf("unmarshal final transcript: %v", err)
	}
	if ok := stream.handleResponse(finalResp); !ok {
		t.Fatal("AddTranscript stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("buffered final events = %d, want none before reference follow-up partial", len(stream.events))
	}
	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	partialResp := smResponse{}
	if err := json.Unmarshal([]byte(`{
		"message":"AddPartialTranscript",
		"results":[{"type":"word","start_time":0.4,"end_time":0.6,"alternatives":[{"content":"late","confidence":0.8}]}]
	}`), &partialResp); err != nil {
		t.Fatalf("unmarshal partial transcript: %v", err)
	}
	if ok := stream.handleResponse(partialResp); !ok {
		t.Fatal("AddPartialTranscript stopped read loop")
	}
	if len(stream.events) != 1 {
		t.Fatalf("forced EOU raw partial events = %d, want only buffered final", len(stream.events))
	}
	event := <-stream.events
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "done" {
		t.Fatalf("event = %#v, want buffered final transcript only", event)
	}
}

func TestSpeechmaticsSTTProviderEndOfTurnCancelsReferenceForcedEOUTimeout(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 20 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{speechDuration: 0.25},
		writeJSON: func(message interface{}) error {
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	if ok := stream.handleResponse(smResponse{Message: "EndOfTurn"}); !ok {
		t.Fatal("EndOfTurn stopped read loop")
	}
	if got := <-stream.events; got.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("provider turn event = %s, want end_of_speech", got.Type)
	}
	if got := <-stream.events; got.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("provider usage event = %s, want recognition_usage", got.Type)
	}
	time.Sleep(50 * time.Millisecond)
	if len(stream.events) != 0 {
		t.Fatalf("events after provider EndOfTurn = %d, want forced EOU timeout canceled", len(stream.events))
	}
}

func TestSpeechmaticsSTTFinalizeSuppressesDuplicateReferenceForcedEOU(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = time.Second
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	writes := 0
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 2),
		state:  &speechmaticsStreamState{speechDuration: 0.3, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			writes++
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("first Finalize error = %v", err)
	}
	if err := provider.Finalize(); err != nil {
		t.Fatalf("second Finalize error = %v", err)
	}
	if writes != 1 {
		t.Fatalf("ForceEndOfUtterance writes = %d, want one while reference forced EOU active", writes)
	}
}

func TestSpeechmaticsSTTFinalizeAfterForcedEOUTimeoutSuppressesDuplicateReferenceForceEOU(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	writes := 0
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{speechDuration: 0.3, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			writes++
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("first Finalize error = %v", err)
	}
	readSpeechmaticsTestEvent(t, stream.events)
	readSpeechmaticsTestEvent(t, stream.events)

	if err := provider.Finalize(); err != nil {
		t.Fatalf("second Finalize error = %v", err)
	}
	if writes != 1 {
		t.Fatalf("ForceEndOfUtterance writes = %d, want no duplicate after forced EOU timeout", writes)
	}
	if len(stream.events) != 0 {
		t.Fatalf("second Finalize events = %d, want no duplicate end_of_speech", len(stream.events))
	}
}

func TestSpeechmaticsSTTFinalizeAfterVADRestartSendsReferenceForceEOU(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = 10 * time.Millisecond
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	writes := 0
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{speechDuration: 0.3, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			writes++
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("first Finalize error = %v", err)
	}
	readSpeechmaticsTestEvent(t, stream.events)
	readSpeechmaticsTestEvent(t, stream.events)

	stream.handleVADStartOfSpeech()

	if err := provider.Finalize(); err != nil {
		t.Fatalf("new-turn Finalize error = %v", err)
	}
	if writes != 2 {
		t.Fatalf("ForceEndOfUtterance writes = %d, want new write after VAD start", writes)
	}
}

func TestSpeechmaticsSTTForcedEOUEndsOnReferenceEndOfUtteranceAck(t *testing.T) {
	oldTimeout := speechmaticsForcedEOUTimeout
	speechmaticsForcedEOUTimeout = time.Second
	t.Cleanup(func() { speechmaticsForcedEOUTimeout = oldTimeout })

	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 2),
		state:  &speechmaticsStreamState{speechDuration: 0.3, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			return nil
		},
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	if ok := stream.handleResponse(smResponse{Message: "EndOfUtterance"}); !ok {
		t.Fatal("EndOfUtterance stopped read loop")
	}

	var event *stt.SpeechEvent
	select {
	case event = <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("EndOfUtterance ACK did not emit end_of_speech")
	}
	if event.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event type = %s, want end_of_speech", event.Type)
	}
	var usage *stt.SpeechEvent
	select {
	case usage = <-stream.events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("EndOfUtterance ACK did not emit recognition usage")
	}
	if usage.Type != stt.SpeechEventRecognitionUsage || usage.RecognitionUsage == nil || usage.RecognitionUsage.AudioDuration != 0.3 {
		t.Fatalf("usage event = %#v, want reference forced EOU ACK usage", usage)
	}
}

func TestSpeechmaticsSTTFixedEndOfUtteranceEmitsReferenceEndOfSpeech(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTFixedTurnDetection())
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 2),
		state:  &speechmaticsStreamState{speechDuration: 0.4, turnHasTranscript: true},
	}

	if ok := stream.handleResponse(smResponse{Message: "EndOfUtterance"}); !ok {
		t.Fatal("handleResponse returned false, want stream to continue after fixed EndOfUtterance")
	}

	var event *stt.SpeechEvent
	select {
	case event = <-stream.events:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fixed EndOfUtterance did not emit end_of_speech")
	}
	if event.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event type = %s, want end_of_speech", event.Type)
	}
	var usage *stt.SpeechEvent
	select {
	case usage = <-stream.events:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fixed EndOfUtterance did not emit recognition usage")
	}
	if usage.Type != stt.SpeechEventRecognitionUsage || usage.RecognitionUsage == nil || usage.RecognitionUsage.AudioDuration != 0.4 {
		t.Fatalf("usage event = %#v, want reference recognition usage after fixed EndOfUtterance", usage)
	}
	if stateDuration := stream.state.speechDuration; stateDuration != 0 {
		t.Fatalf("speech duration after EndOfUtterance = %v, want reset after usage emit", stateDuration)
	}
}

func TestSpeechmaticsSTTFixedLateEndOfTurnAfterEndOfUtteranceDoesNotDuplicate(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTFixedTurnDetection())
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 4),
		state:  &speechmaticsStreamState{speechDuration: 0.4, turnHasTranscript: true},
	}

	if ok := stream.handleResponse(smResponse{Message: "EndOfUtterance"}); !ok {
		t.Fatal("EndOfUtterance stopped read loop")
	}
	select {
	case <-stream.events:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fixed EndOfUtterance did not emit end_of_speech")
	}
	select {
	case <-stream.events:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fixed EndOfUtterance did not emit recognition usage")
	}
	if ok := stream.handleResponse(smResponse{Message: "EndOfTurn"}); !ok {
		t.Fatal("late EndOfTurn stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("late fixed EndOfTurn events = %d, want no duplicate end_of_speech", len(stream.events))
	}
}

func TestSpeechmaticsSTTFinalizeAfterProviderCloseDoesNotWriteStaleControl(t *testing.T) {
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
	provider.mu.Lock()
	provider.closed = true
	provider.mu.Unlock()

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize after provider Close error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("finalize writes after provider Close = %#v, want none", writes)
	}
}

func TestSpeechmaticsSTTFinalizeFixedModeEmitsReferenceLocalTurnEnd(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTFixedTurnDetection())
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 2),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		state:  &speechmaticsStreamState{speechDuration: 0.4, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			t.Fatalf("finalize write = %#v, want local reference finalization for fixed mode", message)
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	t.Cleanup(func() { _ = stream.Close() })
	provider.registerStream(stream)

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}

	end := readSpeechmaticsTestEvent(t, stream.events)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("first event = %#v, want fixed-mode end_of_speech", end)
	}
	usage := readSpeechmaticsTestEvent(t, stream.events)
	if usage.Type != stt.SpeechEventRecognitionUsage || usage.RecognitionUsage == nil || usage.RecognitionUsage.AudioDuration != 0.4 {
		t.Fatalf("second event = %#v, want fixed-mode recognition usage", usage)
	}
}

func TestSpeechmaticsSTTFinalizeFixedModeSuppressesDuplicateReferenceLocalTurnEnd(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTFixedTurnDetection())
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 4),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		state:  &speechmaticsStreamState{speechDuration: 0.4, turnHasTranscript: true},
		writeJSON: func(message interface{}) error {
			t.Fatalf("finalize write = %#v, want local reference finalization for fixed mode", message)
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	t.Cleanup(func() { _ = stream.Close() })
	provider.registerStream(stream)

	if err := provider.Finalize(); err != nil {
		t.Fatalf("first Finalize error = %v", err)
	}
	readSpeechmaticsTestEvent(t, stream.events)
	readSpeechmaticsTestEvent(t, stream.events)

	if err := provider.Finalize(); err != nil {
		t.Fatalf("second Finalize error = %v", err)
	}
	if len(stream.events) != 0 {
		t.Fatalf("second fixed Finalize events = %d, want no duplicate end_of_speech", len(stream.events))
	}
}

func TestSpeechmaticsSTTFinalizeFixedModeSuppressesLateReferenceEndOfUtterance(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTFixedTurnDetection())
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent, 4),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		state:  &speechmaticsStreamState{speechDuration: 0.4, turnHasTranscript: true},
	}
	t.Cleanup(func() { _ = stream.Close() })
	provider.registerStream(stream)

	if err := provider.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	readSpeechmaticsTestEvent(t, stream.events)
	readSpeechmaticsTestEvent(t, stream.events)

	if ok := stream.handleResponse(smResponse{Message: "EndOfUtterance"}); !ok {
		t.Fatal("late EndOfUtterance stopped read loop")
	}
	if len(stream.events) != 0 {
		t.Fatalf("late fixed EndOfUtterance events = %d, want no duplicate end_of_speech", len(stream.events))
	}
}

func readSpeechmaticsTestEvent(t *testing.T, events <-chan *stt.SpeechEvent) *stt.SpeechEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Speechmatics event")
	}
	return nil
}

func TestSpeechmaticsSTTFinalizeSkipsReferenceVADManagedTurnModes(t *testing.T) {
	tests := []struct {
		name string
		opt  SpeechmaticsSTTOption
	}{
		{name: "adaptive", opt: WithSpeechmaticsSTTAdaptiveTurnDetection()},
		{name: "smart_turn", opt: WithSpeechmaticsSTTSmartTurnDetection()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewSpeechmaticsSTT("test-key", tt.opt)
			stream := &speechmaticsSTTStream{
				writeJSON: func(message interface{}) error {
					t.Fatalf("finalize write = %#v, want skipped for provider-managed endpointing", message)
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
		})
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
	if len(writes) != 0 {
		t.Fatalf("speaker update writes = %d, want none because reference SDK updates local diarization config", len(writes))
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

func TestSpeechmaticsSTTUpdateSpeakersPreservesReferenceNotGivenFields(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{closeConn: func() error { return nil }}
	provider.registerStream(stream)

	if err := provider.UpdateSpeakers([]string{"agent"}, []string{"noise"}, "ignore"); err != nil {
		t.Fatalf("initial UpdateSpeakers error = %v", err)
	}
	if err := provider.UpdateSpeakers([]string{"customer"}, nil, ""); err != nil {
		t.Fatalf("partial UpdateSpeakers error = %v", err)
	}

	if got := strings.Join(provider.focusSpeakers, ","); got != "customer" {
		t.Fatalf("provider focus speakers = %q, want customer", got)
	}
	if got := strings.Join(provider.ignoreSpeakers, ","); got != "noise" {
		t.Fatalf("provider ignore speakers = %q, want preserved noise", got)
	}
	if provider.focusMode != "ignore" {
		t.Fatalf("provider focus mode = %q, want preserved ignore", provider.focusMode)
	}
	if got := strings.Join(stream.state.focusSpeakers, ","); got != "customer" {
		t.Fatalf("stream focus speakers = %q, want customer", got)
	}
	if got := strings.Join(stream.state.ignoreSpeakers, ","); got != "noise" {
		t.Fatalf("stream ignore speakers = %q, want preserved noise", got)
	}
	if stream.state.focusMode != "ignore" {
		t.Fatalf("stream focus mode = %q, want preserved ignore", stream.state.focusMode)
	}
}

func TestSpeechmaticsSTTUpdateSpeakersWithoutStreamsMatchesReferenceNoop(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	if err := provider.UpdateSpeakers([]string{"agent"}, []string{"noise"}, "ignore"); err != nil {
		t.Fatalf("UpdateSpeakers error = %v", err)
	}
	if len(provider.focusSpeakers) != 0 {
		t.Fatalf("provider focus speakers = %#v, want unchanged without active streams", provider.focusSpeakers)
	}
	if len(provider.ignoreSpeakers) != 0 {
		t.Fatalf("provider ignore speakers = %#v, want unchanged without active streams", provider.ignoreSpeakers)
	}
	if provider.focusMode != "retain" {
		t.Fatalf("provider focus mode = %q, want unchanged retain", provider.focusMode)
	}
}

func TestSpeechmaticsSTTUpdateSpeakersDisabledDiarizationWithoutStreamsMatchesReferenceNoop(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTEnableDiarization(false))

	if err := provider.UpdateSpeakers([]string{"agent"}, []string{"noise"}, "ignore"); err != nil {
		t.Fatalf("UpdateSpeakers without streams error = %v, want reference no-op", err)
	}
	if len(provider.focusSpeakers) != 0 {
		t.Fatalf("provider focus speakers = %#v, want unchanged without active streams", provider.focusSpeakers)
	}
	if len(provider.ignoreSpeakers) != 0 {
		t.Fatalf("provider ignore speakers = %#v, want unchanged without active streams", provider.ignoreSpeakers)
	}
	if provider.focusMode != "retain" {
		t.Fatalf("provider focus mode = %q, want unchanged retain", provider.focusMode)
	}
}

func TestSpeechmaticsSTTUpdateSpeakersFiltersFutureSegmentsLocally(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		writeJSON: func(message interface{}) error {
			t.Fatalf("speaker update write = %#v, want local-only reference update", message)
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.UpdateSpeakers([]string{"agent"}, []string{"noise"}, "ignore"); err != nil {
		t.Fatalf("UpdateSpeakers error = %v", err)
	}

	var resp smResponse
	if err := json.Unmarshal([]byte(`{
		"message":"AddSegment",
		"segments":[
			{"text":"hello","language":"en","speaker_id":"agent","metadata":{"start_time":0.0,"end_time":0.2}},
			{"text":"ignored","language":"en","speaker_id":"noise","metadata":{"start_time":0.2,"end_time":0.4}},
			{"text":"other","language":"en","speaker_id":"customer","metadata":{"start_time":0.4,"end_time":0.6}}
		]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal segments: %v", err)
	}

	events := speechmaticsEvents(resp, stream.state)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want only focused speaker segment", events)
	}
	if got := events[0].Alternatives[0].SpeakerID; got != "agent" {
		t.Fatalf("speaker id = %q, want agent", got)
	}
	if got := events[0].Alternatives[0].Text; got != "hello" {
		t.Fatalf("text = %q, want hello", got)
	}
}

func TestSpeechmaticsSTTConcurrentSpeakerUpdateAndTranscript(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	stream := &speechmaticsSTTStream{
		events:    make(chan *stt.SpeechEvent, 512),
		errCh:     make(chan error, 1),
		done:      make(chan struct{}),
		closeConn: func() error { return nil },
	}
	provider.registerStream(stream)
	t.Cleanup(func() { _ = stream.Close() })

	resp := smResponse{Message: "AddSegment"}
	resp.Segments = append(resp.Segments, struct {
		Text       string   `json:"text"`
		Language   string   `json:"language"`
		SpeakerID  string   `json:"speaker_id"`
		IsActive   *bool    `json:"is_active"`
		Annotation []string `json:"annotation"`
		Metadata   struct {
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"metadata"`
	}{Text: "hello", Language: "en", SpeakerID: "agent"})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = provider.UpdateSpeakers([]string{"agent"}, []string{fmt.Sprintf("noise-%d", i)}, "ignore")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if !stream.handleResponse(resp) {
				t.Errorf("handleResponse returned false")
				return
			}
			select {
			case <-stream.events:
			default:
			}
		}
	}()
	wg.Wait()
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

func TestSpeechmaticsSTTGetSpeakerIDGroupsPreservesReferenceStreamShape(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	for _, speakerID := range []string{"stream-1", "stream-2"} {
		var stream *speechmaticsSTTStream
		stream = &speechmaticsSTTStream{
			writeJSON: func(message interface{}) error {
				payload, ok := message.(map[string]interface{})
				if !ok || payload["message"] != "GetSpeakers" {
					t.Fatalf("speaker request message = %#v, want GetSpeakers", message)
				}
				stream.recordSpeakerResult([]SpeechmaticsSpeakerIdentifier{
					{Label: speakerID, SpeakerID: speakerID},
				})
				return nil
			},
			closeConn: func() error {
				return nil
			},
		}
		provider.registerStream(stream)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	groups, err := provider.GetSpeakerIDGroups(ctx)
	if err != nil {
		t.Fatalf("GetSpeakerIDGroups error = %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("speaker groups = %#v, want one group per active reference stream", groups)
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if len(group) != 1 {
			t.Fatalf("speaker group = %#v, want one speaker", group)
		}
		seen[group[0].SpeakerID] = true
	}
	if !seen["stream-1"] || !seen["stream-2"] {
		t.Fatalf("speaker groups = %#v, want both stream speaker ids", groups)
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

func TestSpeechmaticsSTTGetSpeakerIDGroupsKeepsReferenceDisabledDiarizationShape(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTEnableDiarization(false))
	for range 2 {
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
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	groups, err := provider.GetSpeakerIDGroups(ctx)
	if err != nil {
		t.Fatalf("GetSpeakerIDGroups error = %v, want nil for disabled diarization", err)
	}
	if len(groups) != 2 {
		t.Fatalf("speaker groups = %#v, want one empty group per active reference stream", groups)
	}
	for _, group := range groups {
		if len(group) != 0 {
			t.Fatalf("speaker group = %#v, want empty disabled-diarization result", group)
		}
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

func TestSpeechmaticsSTTCloseIgnoresReferenceTransportCloseError(t *testing.T) {
	stream := &speechmaticsSTTStream{
		closeConn: func() error {
			return errors.New("transport close failed")
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil for caller-owned cleanup", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after Close error = %v, want reference input-ended error", err)
	}
}

func TestSpeechmaticsSTTClosedStreamReportsInputEndedLikeReference(t *testing.T) {
	stream := &speechmaticsSTTStream{
		closeConn: func() error {
			return nil
		},
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	frame := &model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	for name, err := range map[string]error{
		"PushFrame": stream.PushFrame(frame),
		"Flush":     stream.Flush(),
		"EndInput":  stream.EndInput(),
	} {
		if err == nil || !strings.Contains(err.Error(), "stream input ended") {
			t.Fatalf("%s after Close error = %v, want reference input-ended error", name, err)
		}
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

func TestSpeechmaticsSTTCloseUnblocksPendingNextLikeReference(t *testing.T) {
	stream := &speechmaticsSTTStream{
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error),
		done:   make(chan struct{}),
		closeConn: func() error {
			return nil
		},
	}

	result := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		if event != nil {
			result <- errors.New("Next returned event after Close")
			return
		}
		result <- err
	}()

	select {
	case err := <-result:
		t.Fatalf("Next returned before Close with error %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case err := <-result:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next error after Close = %v, want %v", err, io.EOF)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close did not unblock pending Next")
	}
}

func TestSpeechmaticsSTTCloseUnblocksInFlightPushFrameLikeReference(t *testing.T) {
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	var releaseOnce sync.Once
	stream := &speechmaticsSTTStream{
		writeBinary: func([]byte) error {
			close(writeStarted)
			<-releaseWrite
			return io.ErrClosedPipe
		},
		closeConn: func() error {
			releaseOnce.Do(func() { close(releaseWrite) })
			return nil
		},
	}

	result := make(chan error, 1)
	go func() {
		result <- stream.PushFrame(&model.AudioFrame{
			Data:              make([]byte, 3200),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1600,
		})
	}()

	select {
	case <-writeStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PushFrame did not start provider audio write")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		releaseOnce.Do(func() { close(releaseWrite) })
		t.Fatal("Close did not unblock in-flight PushFrame")
	}

	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("PushFrame error = %v, want closed-pipe after Close interrupted write", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PushFrame remained blocked after Close")
	}
}

func TestSpeechmaticsSTTCloseUnblocksInFlightVADPushFrameLikeReference(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	pushStarted := make(chan struct{})
	vadStream.setPushStarted(pushStarted)
	stream := &speechmaticsSTTStream{
		vadStream: vadStream,
		writeBinary: func([]byte) error {
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}

	result := make(chan error, 1)
	go func() {
		result <- stream.PushFrame(&model.AudioFrame{
			Data:              make([]byte, 3200),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1600,
		})
	}()

	select {
	case <-pushStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PushFrame did not start VAD push")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close did not unblock in-flight VAD PushFrame")
	}

	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("PushFrame error = %v, want closed-pipe after Close interrupted VAD", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PushFrame remained blocked after VAD Close")
	}
}

func TestSpeechmaticsSTTCloseUnblocksInFlightVADEndInputLikeReference(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	endInputStarted := make(chan struct{})
	vadStream.setEndInputStarted(endInputStarted)
	stream := &speechmaticsSTTStream{
		vadStream: vadStream,
		writeJSON: func(interface{}) error {
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}

	result := make(chan error, 1)
	go func() {
		result <- stream.EndInput()
	}()

	select {
	case <-endInputStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("EndInput did not start VAD end input")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close did not unblock in-flight VAD EndInput")
	}

	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("EndInput error = %v, want closed-pipe after Close interrupted VAD", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("EndInput remained blocked after VAD Close")
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

func TestSpeechmaticsSTTProviderCloseCancelsReferenceStreamDial(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("ws://speechmatics.example/v2"))
	oldDialer := websocket.DefaultDialer
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			close(entered)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return nil, errors.New("released without provider close cancellation")
			}
		},
		Proxy: nil,
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		websocket.DefaultDialer = oldDialer
	})

	type streamResult struct {
		stream stt.RecognizeStream
		err    error
	}
	done := make(chan streamResult, 1)
	go func() {
		stream, err := provider.Stream(context.Background(), "")
		done <- streamResult{stream: stream, err: err}
	}()

	select {
	case <-entered:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stream did not start Speechmatics dial")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	select {
	case result := <-done:
		if result.stream != nil {
			t.Fatalf("Stream after provider Close = %#v, want nil", result.stream)
		}
		if !errors.Is(result.err, io.ErrClosedPipe) {
			t.Fatalf("Stream after provider Close error = %v, want io.ErrClosedPipe", result.err)
		}
	case <-time.After(500 * time.Millisecond):
		releaseOnce.Do(func() { close(release) })
		t.Fatal("provider Close did not cancel Speechmatics stream dial")
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
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-app", "livekit/1.5.19.rc1")
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-voice-sdk", "0.2.8")

	provider = NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("wss://speechmatics.example/v2/"))
	if provider.baseURL != "wss://speechmatics.example/v2/" {
		t.Fatalf("provider baseURL = %q, want reference custom base URL preserved", provider.baseURL)
	}
	streamURL, err = url.Parse(buildSpeechmaticsSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse custom stream URL: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "speechmatics.example" || streamURL.Path != "/v2/" {
		t.Fatalf("stream URL = %q, want reference custom base URL path", streamURL.String())
	}
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-app", "livekit/1.5.19.rc1")
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-voice-sdk", "0.2.8")
}

func TestSpeechmaticsSTTPreservesReferenceEmptyLanguage(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTLanguage(""))
	if provider.language != "" {
		t.Fatalf("language = %q, want explicit empty reference language", provider.language)
	}

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	if config["language"] != "" {
		t.Fatalf("language config = %#v, want explicit empty reference language", config["language"])
	}

	message = buildSpeechmaticsSTTStartMessage(provider, "fr")
	config = message["transcription_config"].(map[string]interface{})
	if config["language"] != "fr" {
		t.Fatalf("stream language override = %#v, want fr", config["language"])
	}
}

func TestSpeechmaticsSTTPreservesReferenceEmptyAudioEncoding(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTAudioEncoding(""))
	if provider.audioEncoding != "" {
		t.Fatalf("audioEncoding = %q, want explicit empty reference encoding", provider.audioEncoding)
	}

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	audioFormat := message["audio_format"].(map[string]interface{})
	if audioFormat["encoding"] != "" {
		t.Fatalf("encoding = %#v, want explicit empty reference encoding", audioFormat["encoding"])
	}
}

func TestSpeechmaticsSTTUsesEnvironmentRealtimeURL(t *testing.T) {
	t.Setenv("SPEECHMATICS_RT_URL", "wss://speechmatics.env/v2/")

	provider := NewSpeechmaticsSTT("test-key")
	if provider.baseURL != "wss://speechmatics.env/v2/" {
		t.Fatalf("provider baseURL = %q, want environment realtime URL preserved", provider.baseURL)
	}

	streamURL, err := url.Parse(buildSpeechmaticsSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse environment stream URL: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "speechmatics.env" || streamURL.Path != "/v2/" {
		t.Fatalf("stream URL = %q, want environment realtime URL", streamURL.String())
	}
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-app", "livekit/1.5.19.rc1")
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-voice-sdk", "0.2.8")

	provider = NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL("wss://speechmatics.explicit/v2/"))
	if provider.baseURL != "wss://speechmatics.explicit/v2/" {
		t.Fatalf("provider baseURL = %q, want explicit realtime URL preserved", provider.baseURL)
	}
	streamURL, err = url.Parse(buildSpeechmaticsSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse explicit stream URL: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "speechmatics.explicit" || streamURL.Path != "/v2/" {
		t.Fatalf("stream URL = %q, want explicit realtime URL", streamURL.String())
	}
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-app", "livekit/1.5.19.rc1")
	assertSpeechmaticsSTTQuery(t, streamURL.Query(), "sm-voice-sdk", "0.2.8")
}

func TestSpeechmaticsSTTPreservesReferenceEmptyBaseURL(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTBaseURL(""))
	if provider.baseURL != "" {
		t.Fatalf("baseURL = %q, want explicit empty reference base URL", provider.baseURL)
	}

	stream, err := provider.Stream(context.Background(), "")
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil for missing base URL", stream)
	}
	if err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("Stream error = %v, want missing base URL error", err)
	}
}

func assertSpeechmaticsSTTQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
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
	if _, ok := audioFormat["chunk_size"]; ok {
		t.Fatalf("chunk_size = %#v, want omitted from reference StartRecognition audio_format", audioFormat["chunk_size"])
	}
	if _, ok := message["chunk_size"]; ok {
		t.Fatalf("chunk_size sent outside audio_format in %#v", message)
	}
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "language", "de")
	assertSpeechmaticsConfig(t, config, "domain", "finance")
	assertSpeechmaticsConfig(t, config, "output_locale", "de-DE")
	assertSpeechmaticsConfig(t, config, "enable_partials", true)
	if _, ok := config["include_partials"]; ok {
		t.Fatalf("include_partials = %#v, want omitted because reference keeps partial-output filtering local", config["include_partials"])
	}
	if _, ok := config["diarization"]; ok {
		t.Fatalf("diarization = %#v, want omitted when reference diarization disabled", config["diarization"])
	}

	message = buildSpeechmaticsSTTStartMessage(provider, "fr")
	config = message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "language", "fr")
	assertSpeechmaticsConfig(t, config, "enable_partials", true)

	if _, err := json.Marshal(message); err != nil {
		t.Fatalf("marshal start message: %v", err)
	}
}

func TestSpeechmaticsSTTStartMessageEnablesReferenceDiarizationByDefault(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "diarization", "speaker")
	diarizationConfig, ok := config["speaker_diarization_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("speaker_diarization_config = %#v, want reference default object", config["speaker_diarization_config"])
	}
	assertSpeechmaticsConfig(t, diarizationConfig, "speaker_sensitivity", float64(0.5))
	assertSpeechmaticsConfig(t, diarizationConfig, "prefer_current_speaker", false)
}

func TestSpeechmaticsSTTStartMessageOmitsDiarizationConfigWhenDisabled(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTEnableDiarization(false),
		WithSpeechmaticsSTTSpeakerSensitivity(0.7),
		WithSpeechmaticsSTTMaxSpeakers(3),
		WithSpeechmaticsSTTPreferCurrentSpeaker(true),
		WithSpeechmaticsSTTKnownSpeakers([]SpeechmaticsSpeakerIdentifier{
			{Label: "agent", SpeakerID: "spk-1"},
		}),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	if _, ok := config["diarization"]; ok {
		t.Fatalf("diarization = %#v, want omitted when reference diarization disabled", config["diarization"])
	}
	if _, ok := config["speaker_diarization_config"]; ok {
		t.Fatalf("speaker_diarization_config = %#v, want omitted when reference diarization disabled", config["speaker_diarization_config"])
	}
}

func TestSpeechmaticsSTTStartMessageUsesReferencePresetDefaults(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "operating_point", "enhanced")
	assertSpeechmaticsConfig(t, config, "max_delay", float64(2.0))
	assertSpeechmaticsConfig(t, config, "max_delay_mode", "flexible")
	assertSpeechmaticsConfig(t, config, "enable_entities", false)
	audioFilteringConfig, ok := config["audio_filtering_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("audio_filtering_config = %#v, want map", config["audio_filtering_config"])
	}
	assertSpeechmaticsConfig(t, audioFilteringConfig, "volume_threshold", float64(0.0))
}

func TestSpeechmaticsSTTStartMessageUsesVocabularyAndSpeakerOptions(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdditionalVocab([]SpeechmaticsAdditionalVocabEntry{
			{Content: "LiveKit", SoundsLike: []string{"live kit"}},
			{Content: "Cavos"},
		}),
		WithSpeechmaticsSTTSpeakerFocus([]string{"agent"}, []string{"customer"}, "ignore"),
		WithSpeechmaticsSTTKnownSpeakers([]SpeechmaticsSpeakerIdentifier{
			{Label: "agent", SpeakerIdentifiers: []string{"spk-1", "spk-2"}},
		}),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})

	vocab := config["additional_vocab"].([]SpeechmaticsAdditionalVocabEntry)
	if len(vocab) != 2 || vocab[0].Content != "LiveKit" || vocab[0].SoundsLike[0] != "live kit" {
		t.Fatalf("additional_vocab = %#v, want LiveKit sounds-like entry", vocab)
	}
	if _, ok := config["speaker_config"]; ok {
		t.Fatalf("speaker_config = %#v, want omitted from StartRecognition because reference SDK keeps speaker focus local", config["speaker_config"])
	}
	diarizationConfig, ok := config["speaker_diarization_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("speaker_diarization_config = %#v, want object", config["speaker_diarization_config"])
	}
	knownSpeakersJSON, err := json.Marshal(diarizationConfig["speakers"])
	if err != nil {
		t.Fatalf("marshal speaker_diarization_config.speakers: %v", err)
	}
	var knownSpeakers []map[string]interface{}
	if err := json.Unmarshal(knownSpeakersJSON, &knownSpeakers); err != nil {
		t.Fatalf("decode speaker_diarization_config.speakers: %v", err)
	}
	if len(knownSpeakers) != 1 || knownSpeakers[0]["label"] != "agent" {
		t.Fatalf("speaker_diarization_config.speakers = %#v, want agent speaker", knownSpeakers)
	}
	identifiers, _ := knownSpeakers[0]["speaker_identifiers"].([]interface{})
	if len(identifiers) != 2 || identifiers[0] != "spk-1" || identifiers[1] != "spk-2" {
		t.Fatalf("speaker_identifiers = %#v, want spk-1", knownSpeakers[0]["speaker_identifiers"])
	}
	if _, ok := knownSpeakers[0]["speaker_id"]; ok {
		t.Fatalf("speaker_id = %#v, want omitted from known-speaker config", knownSpeakers[0]["speaker_id"])
	}
	if _, ok := config["known_speakers"]; ok {
		t.Fatalf("known_speakers sent at top level in %#v", config)
	}
}

func TestSpeechmaticsSTTKnownSpeakerConfigIgnoresReferenceResultSpeakerID(t *testing.T) {
	config := speechmaticsKnownSpeakerConfig([]SpeechmaticsSpeakerIdentifier{
		{Label: "agent", SpeakerID: "result-speaker-id"},
	})
	if len(config) != 1 {
		t.Fatalf("known speaker config = %#v, want one reference speaker entry", config)
	}
	if got := config[0]["label"]; got != "agent" {
		t.Fatalf("label = %#v, want agent", got)
	}
	identifiers, ok := config[0]["speaker_identifiers"].([]string)
	if !ok {
		t.Fatalf("speaker_identifiers = %#v, want string slice", config[0]["speaker_identifiers"])
	}
	if len(identifiers) != 0 {
		t.Fatalf("speaker_identifiers = %#v, want empty when only result speaker_id is set", identifiers)
	}
	if _, ok := config[0]["speaker_id"]; ok {
		t.Fatalf("speaker_id = %#v, want omitted from reference known-speaker config", config[0]["speaker_id"])
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
		WithSpeechmaticsSTTEndOfUtteranceMaxDelay(1.8),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	conversationConfig, ok := config["conversation_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("conversation_config = %#v, want reference fixed endpointing object", config["conversation_config"])
	}
	assertSpeechmaticsConfig(t, conversationConfig, "end_of_utterance_silence_trigger", float64(0.6))
	for _, key := range []string{"end_of_utterance_mode", "end_of_utterance_silence_trigger", "end_of_utterance_max_delay"} {
		if _, ok := config[key]; ok {
			t.Fatalf("%s = %#v, want omitted outside reference conversation_config", key, config[key])
		}
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
	for _, key := range []string{"end_of_utterance_mode", "end_of_utterance_silence_trigger", "end_of_utterance_max_delay"} {
		if _, ok := config[key]; ok {
			t.Fatalf("%s = %#v, want omitted for reference external turn detection", key, config[key])
		}
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
		t.Fatalf("conversation_config = %#v, want reference fixed endpointing object", config["conversation_config"])
	}
	assertSpeechmaticsConfig(t, conversationConfig, "end_of_utterance_silence_trigger", float64(0.5))
	for _, key := range []string{"end_of_utterance_mode", "end_of_utterance_silence_trigger", "end_of_utterance_max_delay"} {
		if _, ok := config[key]; ok {
			t.Fatalf("%s = %#v, want omitted outside reference conversation_config", key, config[key])
		}
	}
}

func TestSpeechmaticsSTTStartMessageUsesReferenceAdaptiveTurnDetectionMode(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdaptiveTurnDetection(),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	if _, ok := config["conversation_config"]; ok {
		t.Fatalf("conversation_config = %#v, want omitted for adaptive forced EOU", config["conversation_config"])
	}
	assertSpeechmaticsConfig(t, config, "diarization", "speaker")
	assertSpeechmaticsConfig(t, config, "operating_point", "enhanced")
	assertSpeechmaticsConfig(t, config, "max_delay", float64(2.0))
	for _, key := range []string{"end_of_utterance_mode", "end_of_utterance_silence_trigger", "end_of_utterance_max_delay", "vad_config", "smart_turn_config"} {
		if _, ok := config[key]; ok {
			t.Fatalf("%s = %#v, want omitted from reference adaptive wire config", key, config[key])
		}
	}
}

func TestSpeechmaticsSTTStartMessageUsesReferenceSmartTurnDetectionMode(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTSmartTurnDetection(),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})
	if _, ok := config["conversation_config"]; ok {
		t.Fatalf("conversation_config = %#v, want omitted for smart-turn forced EOU", config["conversation_config"])
	}
	assertSpeechmaticsConfig(t, config, "diarization", "speaker")
	assertSpeechmaticsConfig(t, config, "operating_point", "enhanced")
	assertSpeechmaticsConfig(t, config, "max_delay", float64(2.0))
	for _, key := range []string{"end_of_utterance_mode", "end_of_utterance_silence_trigger", "end_of_utterance_max_delay", "vad_config", "smart_turn_config"} {
		if _, ok := config[key]; ok {
			t.Fatalf("%s = %#v, want omitted from reference smart-turn wire config", key, config[key])
		}
	}
}

func TestSpeechmaticsSTTProviderManagedTurnDetectionOmitsReferenceWireEndpointingOverrides(t *testing.T) {
	tests := []struct {
		name string
		opt  SpeechmaticsSTTOption
	}{
		{name: "adaptive", opt: WithSpeechmaticsSTTAdaptiveTurnDetection()},
		{name: "smart_turn", opt: WithSpeechmaticsSTTSmartTurnDetection()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewSpeechmaticsSTT("test-key",
				tt.opt,
				WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(0.4),
				WithSpeechmaticsSTTEndOfUtteranceMaxDelay(1.5),
			)

			message := buildSpeechmaticsSTTStartMessage(provider, "")
			config := message["transcription_config"].(map[string]interface{})
			if _, ok := config["conversation_config"]; ok {
				t.Fatalf("conversation_config = %#v, want omitted for reference provider-managed turn detection", config["conversation_config"])
			}
			for _, key := range []string{"end_of_utterance_mode", "end_of_utterance_silence_trigger", "end_of_utterance_max_delay", "vad_config", "smart_turn_config"} {
				if _, ok := config[key]; ok {
					t.Fatalf("%s = %#v, want omitted for reference provider-managed turn detection", key, config[key])
				}
			}
		})
	}
}

func assertSpeechmaticsConfig(t *testing.T, config map[string]interface{}, key string, want interface{}) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %#v in %#v", key, got, want, config)
	}
}

func speechmaticsTestInt16PCM(samples int) []byte {
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(i)))
	}
	return data
}

func speechmaticsEveryNthInt16PCM(samples int, step int) []byte {
	if step <= 0 {
		return nil
	}
	data := make([]byte, 0, ((samples+step-1)/step)*2)
	for i := 0; i < samples; i += step {
		var sample [2]byte
		binary.LittleEndian.PutUint16(sample[:], uint16(int16(i)))
		data = append(data, sample[:]...)
	}
	return data
}

func speechmaticsResampledBytes(t *testing.T, chunks []int) []byte {
	t.Helper()
	var writes [][]byte
	stream := &speechmaticsSTTStream{
		owner: NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTSampleRate(16000)),
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}

	nextSample := 0
	for _, samples := range chunks {
		data := speechmaticsTestInt16PCMRange(nextSample, samples)
		nextSample += samples
		if err := stream.PushFrame(&model.AudioFrame{
			Data:              data,
			SampleRate:        44100,
			NumChannels:       1,
			SamplesPerChannel: uint32(samples),
		}); err != nil {
			t.Fatalf("PushFrame(%d) error = %v", samples, err)
		}
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	return bytes.Join(writes, nil)
}

func speechmaticsTestInt16PCMRange(startSample int, samples int) []byte {
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(startSample+i)))
	}
	return data
}

type speechmaticsFailAfterHandshakeWriteConn struct {
	net.Conn
	writes int
}

func (c *speechmaticsFailAfterHandshakeWriteConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writes > 1 {
		return 0, errors.New("startup write failed")
	}
	return c.Conn.Write(p)
}

type fakeSpeechmaticsVAD struct {
	stream *fakeSpeechmaticsVADStream
}

func (f *fakeSpeechmaticsVAD) Label() string { return "fake.speechmatics.vad" }
func (f *fakeSpeechmaticsVAD) Model() string { return "fake" }
func (f *fakeSpeechmaticsVAD) Provider() string {
	return "fake"
}
func (f *fakeSpeechmaticsVAD) Capabilities() vad.VADCapabilities {
	return vad.VADCapabilities{UpdateInterval: 0.1}
}
func (f *fakeSpeechmaticsVAD) OnMetricsCollected(vad.VADMetricsHandler) func() {
	return func() {}
}
func (f *fakeSpeechmaticsVAD) Stream(context.Context) (vad.VADStream, error) {
	return f.stream, nil
}

type fakeSpeechmaticsVADStream struct {
	mu              sync.Mutex
	events          chan *vad.VADEvent
	nextErr         error
	pushErr         error
	endInputErr     error
	nextStarted     chan struct{}
	pushStarted     chan struct{}
	endInputStarted chan struct{}
	pushed          []*model.AudioFrame
	ended           bool
	closed          bool
	closedOnce      sync.Once
}

func newFakeSpeechmaticsVADStream() *fakeSpeechmaticsVADStream {
	return &fakeSpeechmaticsVADStream{events: make(chan *vad.VADEvent, 1)}
}

func (s *fakeSpeechmaticsVADStream) setNextErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextErr = err
}

func (s *fakeSpeechmaticsVADStream) setPushErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushErr = err
}

func (s *fakeSpeechmaticsVADStream) setEndInputErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endInputErr = err
}

func (s *fakeSpeechmaticsVADStream) setNextStarted(ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextStarted = ch
}

func (s *fakeSpeechmaticsVADStream) setPushStarted(ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushStarted = ch
}

func (s *fakeSpeechmaticsVADStream) setEndInputStarted(ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endInputStarted = ch
}

func (s *fakeSpeechmaticsVADStream) pushedFrames() []*model.AudioFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*model.AudioFrame(nil), s.pushed...)
}

func (s *fakeSpeechmaticsVADStream) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

func (s *fakeSpeechmaticsVADStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *fakeSpeechmaticsVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	pushErr := s.pushErr
	pushStarted := s.pushStarted
	s.pushStarted = nil
	s.mu.Unlock()
	if pushErr != nil {
		return pushErr
	}
	if pushStarted != nil {
		close(pushStarted)
		if _, ok := <-s.events; !ok {
			return io.ErrClosedPipe
		}
	}
	s.mu.Lock()
	s.pushed = append(s.pushed, frame)
	s.mu.Unlock()
	return nil
}

func (s *fakeSpeechmaticsVADStream) Flush() error { return nil }
func (s *fakeSpeechmaticsVADStream) EndInput() error {
	s.mu.Lock()
	endInputErr := s.endInputErr
	endInputStarted := s.endInputStarted
	s.endInputStarted = nil
	s.mu.Unlock()
	if endInputErr != nil {
		return endInputErr
	}
	if endInputStarted != nil {
		close(endInputStarted)
		if _, ok := <-s.events; !ok {
			return io.ErrClosedPipe
		}
	}
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	s.closedOnce.Do(func() { close(s.events) })
	return nil
}
func (s *fakeSpeechmaticsVADStream) Close() error {
	s.closedOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.events)
	})
	return nil
}
func (s *fakeSpeechmaticsVADStream) Next() (*vad.VADEvent, error) {
	s.mu.Lock()
	nextStarted := s.nextStarted
	s.nextStarted = nil
	nextErr := s.nextErr
	s.mu.Unlock()
	if nextStarted != nil {
		close(nextStarted)
	}
	if nextErr != nil {
		return nil, nextErr
	}
	event, ok := <-s.events
	if !ok {
		return nil, io.EOF
	}
	return event, nil
}
