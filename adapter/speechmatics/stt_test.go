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
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/gorilla/websocket"
)

func TestSpeechmaticsTranscriptEventPreservesWordTimings(t *testing.T) {
	resp := smResponse{
		Message: "AddTranscript",
		Results: []struct {
			Alternatives []struct {
				Content    string  `json:"content"`
				Confidence float64 `json:"confidence"`
				SpeakerID  string  `json:"speaker"`
				Language   string  `json:"language"`
			} `json:"alternatives"`
			Type      string  `json:"type"`
			Attaches  string  `json:"attaches_to"`
			IsEOS     bool    `json:"is_eos"`
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
					SpeakerID  string  `json:"speaker"`
					Language   string  `json:"language"`
				}{{Content: "hello", Confidence: 0.92}},
			},
			{
				Type:      "punctuation",
				Attaches:  "previous",
				StartTime: 0.3,
				EndTime:   0.3,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
					SpeakerID  string  `json:"speaker"`
					Language   string  `json:"language"`
				}{{Content: ",", Confidence: 1.0}},
			},
			{
				Type:      "word",
				StartTime: 0.4,
				EndTime:   0.8,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
					SpeakerID  string  `json:"speaker"`
					Language   string  `json:"language"`
				}{{Content: "world", Confidence: 0.88}},
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
		Results: []struct {
			Alternatives []struct {
				Content    string  `json:"content"`
				Confidence float64 `json:"confidence"`
				SpeakerID  string  `json:"speaker"`
				Language   string  `json:"language"`
			} `json:"alternatives"`
			Type      string  `json:"type"`
			Attaches  string  `json:"attaches_to"`
			IsEOS     bool    `json:"is_eos"`
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		}{
			{
				StartTime: 0.15,
				EndTime:   0.45,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
					SpeakerID  string  `json:"speaker"`
					Language   string  `json:"language"`
				}{{Content: "defaulted", Confidence: 0.91, SpeakerID: "S1", Language: "en"}},
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

func TestSpeechmaticsEventsMapReferenceRawTranscriptFallback(t *testing.T) {
	resp := smResponse{
		Message: "AddTranscript",
		Results: []struct {
			Alternatives []struct {
				Content    string  `json:"content"`
				Confidence float64 `json:"confidence"`
				SpeakerID  string  `json:"speaker"`
				Language   string  `json:"language"`
			} `json:"alternatives"`
			Type      string  `json:"type"`
			Attaches  string  `json:"attaches_to"`
			IsEOS     bool    `json:"is_eos"`
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
					SpeakerID  string  `json:"speaker"`
					Language   string  `json:"language"`
				}{{Content: "hello", Confidence: 0.92}},
			},
			{
				Type:      "punctuation",
				Attaches:  "previous",
				StartTime: 0.3,
				EndTime:   0.3,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
					SpeakerID  string  `json:"speaker"`
					Language   string  `json:"language"`
				}{{Content: ",", Confidence: 1.0}},
			},
			{
				Type:      "word",
				StartTime: 0.4,
				EndTime:   0.8,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
					SpeakerID  string  `json:"speaker"`
					Language   string  `json:"language"`
				}{{Content: "world", Confidence: 0.88}},
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
	events := speechmaticsEvents(stablePartial, state)
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
	if !vadStream.closed {
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
	vadStream := newFakeSpeechmaticsVADStream()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.UnderlyingConn().Close()
	}))
	defer server.Close()

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
	if !vadStream.closed {
		t.Fatal("VAD stream closed = false after startup write failure")
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

func TestSpeechmaticsSTTVADEndOfSpeechFinalizesReferenceExternalTurn(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdaptiveTurnDetection(),
		WithSpeechmaticsSTTVAD(&fakeSpeechmaticsVAD{stream: vadStream}),
	)
	if provider.turnDetectionMode != "external" {
		t.Fatalf("turn detection mode = %q, want external when explicit VAD is provided", provider.turnDetectionMode)
	}

	var controlMessages []map[string]interface{}
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
			controlMessages = append(controlMessages, control)
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
	if got := vadStream.pushed[0]; got != frame {
		t.Fatalf("VAD pushed frame = %#v, want original pre-normalized frame", got)
	}

	vadStream.events <- &vad.VADEvent{Type: vad.VADEventEndOfSpeech}
	waitForSpeechmaticsControlMessage(t, &controlMessages, "ForceEndOfUtterance")
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
	vadStream.nextErr = errors.New("vad failed")
	vadStream.nextStarted = make(chan struct{})
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
	case <-vadStream.nextStarted:
	case <-time.After(time.Second):
		t.Fatal("VAD stream did not start")
	}
	select {
	case <-transportClosed:
	case <-time.After(time.Second):
		t.Fatal("VAD error did not close Speechmatics stream transport")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01}}); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after VAD error = %v, want reference input-ended error", err)
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
			if vadStream.ended {
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
	if len(vadStream.pushed) != 0 {
		t.Fatalf("VAD pushed frames before RecognitionStarted = %d, want buffered original audio", len(vadStream.pushed))
	}
	if len(writes) != 0 {
		t.Fatalf("provider writes before RecognitionStarted = %d, want buffered provider audio", len(writes))
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() before RecognitionStarted error = %v", err)
	}
	if vadStream.ended {
		t.Fatal("VAD EndInput before RecognitionStarted = true, want buffered end input")
	}

	if keepReading := stream.handleResponse(smResponse{Message: "RecognitionStarted"}); !keepReading {
		t.Fatal("RecognitionStarted stopped read loop")
	}
	if len(vadStream.pushed) != 1 || vadStream.pushed[0] != frame {
		t.Fatalf("VAD pushed frames after RecognitionStarted = %#v, want original frame", vadStream.pushed)
	}
	if !vadStream.ended {
		t.Fatal("VAD EndInput after RecognitionStarted = false, want drained end input after frames")
	}
	if len(writes) != 1 {
		t.Fatalf("provider writes after RecognitionStarted = %d, want buffered provider audio", len(writes))
	}
}

func TestSpeechmaticsCloseUnblocksReferenceVADStartupDrain(t *testing.T) {
	vadStream := newFakeSpeechmaticsVADStream()
	vadStream.pushStarted = make(chan struct{})
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
	case <-vadStream.pushStarted:
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

func waitForSpeechmaticsControlMessage(t *testing.T, messages *[]map[string]interface{}, want string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, message := range *messages {
			if message["message"] == want {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("control messages = %#v, want %s", *messages, want)
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
	if len(vadStream.pushed) != 1 {
		t.Fatalf("VAD pushed frames = %d, want only first valid sample-rate frame", len(vadStream.pushed))
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
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
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

func TestSpeechmaticsSTTStreamRejectsInvalidReferenceSampleRates(t *testing.T) {
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
			return nil, errors.New("unexpected speechmatics stt dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewSpeechmaticsSTT("test-key",
				WithSpeechmaticsSTTBaseURL("ws://speechmatics.example/v2"),
				WithSpeechmaticsSTTSampleRate(tt.sampleRate),
			)

			stream, err := provider.Stream(context.Background(), "")
			if stream != nil {
				t.Fatalf("Stream = %#v, want nil for invalid sample rate", stream)
			}
			if err == nil || !strings.Contains(err.Error(), "sample_rate must be 8000 or 16000") {
				t.Fatalf("Stream error = %v, want reference sample_rate validation", err)
			}
		})
	}
	if dials != 0 {
		t.Fatalf("invalid sample-rate streams dialed %d times, want none", dials)
	}
}

func TestSpeechmaticsSTTStreamRejectsInvalidReferenceDisabledDiarizationOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []SpeechmaticsSTTOption
		want string
	}{
		{
			name: "focus speakers",
			opts: []SpeechmaticsSTTOption{
				WithSpeechmaticsSTTEnableDiarization(false),
				WithSpeechmaticsSTTSpeakerFocus([]string{"agent"}, nil, "retain"),
			},
			want: "SpeakerFocusConfig.focus_speakers and SpeakerFocusConfig.ignore_speakers must be empty when enable_diarization is False",
		},
		{
			name: "ignore speakers",
			opts: []SpeechmaticsSTTOption{
				WithSpeechmaticsSTTEnableDiarization(false),
				WithSpeechmaticsSTTSpeakerFocus(nil, []string{"noise"}, "retain"),
			},
			want: "SpeakerFocusConfig.focus_speakers and SpeakerFocusConfig.ignore_speakers must be empty when enable_diarization is False",
		},
		{
			name: "max speakers",
			opts: []SpeechmaticsSTTOption{
				WithSpeechmaticsSTTEnableDiarization(false),
				WithSpeechmaticsSTTMaxSpeakers(3),
			},
			want: "max_speakers cannot be set when enable_diarization is False",
		},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := append([]SpeechmaticsSTTOption{WithSpeechmaticsSTTBaseURL("ws://speechmatics.example/v2")}, tt.opts...)
			provider := NewSpeechmaticsSTT("test-key", opts...)

			stream, err := provider.Stream(context.Background(), "")
			if stream != nil {
				t.Fatalf("Stream = %#v, want nil for invalid disabled diarization options", stream)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stream error = %v, want %q", err, tt.want)
			}
		})
	}
	if dials != 0 {
		t.Fatalf("invalid disabled-diarization streams dialed %d times, want none", dials)
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

func TestSpeechmaticsSTTFinalizeTimesOutReferenceForcedEOU(t *testing.T) {
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

func TestSpeechmaticsSTTFixedEndOfUtteranceEmitsReferenceEndOfSpeech(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key", WithSpeechmaticsSTTFixedTurnDetection())
	stream := &speechmaticsSTTStream{
		owner:  provider,
		events: make(chan *stt.SpeechEvent, 2),
		state:  &speechmaticsStreamState{speechDuration: 0.4},
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

func TestSpeechmaticsSTTFinalizeSkipsReferenceProviderManagedTurnModes(t *testing.T) {
	tests := []struct {
		name string
		opt  SpeechmaticsSTTOption
	}{
		{name: "fixed", opt: WithSpeechmaticsSTTFixedTurnDetection()},
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
	vadStream.pushStarted = make(chan struct{})
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
	case <-vadStream.pushStarted:
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
	vadStream.endInputStarted = make(chan struct{})
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
	case <-vadStream.endInputStarted:
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
			{Label: "agent", SpeakerID: "spk-1"},
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
	if len(identifiers) != 1 || identifiers[0] != "spk-1" {
		t.Fatalf("speaker_identifiers = %#v, want spk-1", knownSpeakers[0]["speaker_identifiers"])
	}
	if _, ok := knownSpeakers[0]["speaker_id"]; ok {
		t.Fatalf("speaker_id = %#v, want omitted from known-speaker config", knownSpeakers[0]["speaker_id"])
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
	events          chan *vad.VADEvent
	nextErr         error
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

func (s *fakeSpeechmaticsVADStream) PushFrame(frame *model.AudioFrame) error {
	if s.pushStarted != nil {
		close(s.pushStarted)
		s.pushStarted = nil
		if _, ok := <-s.events; !ok {
			return io.ErrClosedPipe
		}
	}
	s.pushed = append(s.pushed, frame)
	return nil
}

func (s *fakeSpeechmaticsVADStream) Flush() error { return nil }
func (s *fakeSpeechmaticsVADStream) EndInput() error {
	if s.endInputStarted != nil {
		close(s.endInputStarted)
		s.endInputStarted = nil
		if _, ok := <-s.events; !ok {
			return io.ErrClosedPipe
		}
	}
	s.ended = true
	s.closedOnce.Do(func() { close(s.events) })
	return nil
}
func (s *fakeSpeechmaticsVADStream) Close() error {
	s.closedOnce.Do(func() {
		s.closed = true
		close(s.events)
	})
	return nil
}
func (s *fakeSpeechmaticsVADStream) Next() (*vad.VADEvent, error) {
	if s.nextStarted != nil {
		close(s.nextStarted)
		s.nextStarted = nil
	}
	if s.nextErr != nil {
		return nil, s.nextErr
	}
	event, ok := <-s.events
	if !ok {
		return nil, io.EOF
	}
	return event, nil
}
