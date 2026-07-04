package aws

import (
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/google/uuid"
)

const awsRealtimeBargeInContent = `{ "interrupted" : true }`

type awsRealtimeTurnPhase int

const (
	awsRealtimeTurnIdle awsRealtimeTurnPhase = iota
	awsRealtimeTurnUserSpeaking
	awsRealtimeTurnUserFinished
	awsRealtimeTurnAssistantResponding
	awsRealtimeTurnDone
)

type awsRealtimeTurn struct {
	inputID                string
	transcript             []string
	phase                  awsRealtimeTurnPhase
	inputStartedEmitted    bool
	inputStoppedEmitted    bool
	transcriptFinalEmitted bool
	generationEmitted      bool
}

func newAWSRealtimeTurn() *awsRealtimeTurn {
	return &awsRealtimeTurn{inputID: uuid.NewString()}
}

func (t *awsRealtimeTurn) addPartialText(text string) {
	t.transcript = append(t.transcript, text)
}

func (t *awsRealtimeTurn) currentTranscript() string {
	return strings.Join(t.transcript, " ")
}

type awsRealtimeTurnTracker struct {
	emit           func(llm.RealtimeEvent)
	emitGeneration func()
	currentTurn    *awsRealtimeTurn
}

func newAWSRealtimeTurnTracker(emit func(llm.RealtimeEvent), emitGeneration func()) *awsRealtimeTurnTracker {
	return &awsRealtimeTurnTracker{
		emit:           emit,
		emitGeneration: emitGeneration,
	}
}

func (t *awsRealtimeTurnTracker) feed(event map[string]any) {
	turn := t.ensureTurn()

	switch classifyAWSRealtimeTurnEvent(event) {
	case "USER_TEXT_PARTIAL":
		turn.addPartialText(awsRealtimeNestedString(event, "event", "textOutput", "content"))
		t.maybeEmitInputStarted(turn)
		t.emitTranscript(turn, false)
	case "TOOL_OUTPUT_CONTENT_START", "ASSISTANT_SPEC_START":
		t.maybeEmitInputStopped(turn)
		t.maybeEmitTranscriptCompleted(turn)
		t.maybeEmitGenerationCreated(turn)
		if turn.transcriptFinalEmitted {
			turn.phase = awsRealtimeTurnDone
			t.currentTurn = nil
			return
		}
	case "BARGE_IN":
		t.emit(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted})
		turn.phase = awsRealtimeTurnDone
	case "ASSISTANT_AUDIO_END":
		if awsRealtimeNestedString(event, "event", "contentEnd", "stopReason") == "END_TURN" {
			turn.phase = awsRealtimeTurnDone
		}
	}

	if turn.phase == awsRealtimeTurnDone {
		t.currentTurn = nil
	}
}

func (t *awsRealtimeTurnTracker) ensureTurn() *awsRealtimeTurn {
	if t.currentTurn == nil {
		t.currentTurn = newAWSRealtimeTurn()
	}
	return t.currentTurn
}

func (t *awsRealtimeTurnTracker) maybeEmitInputStarted(turn *awsRealtimeTurn) {
	if turn.inputStartedEmitted {
		return
	}
	turn.inputStartedEmitted = true
	turn.phase = awsRealtimeTurnUserSpeaking
	t.emit(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted})
}

func (t *awsRealtimeTurnTracker) maybeEmitInputStopped(turn *awsRealtimeTurn) {
	if turn.inputStoppedEmitted {
		return
	}
	turn.inputStoppedEmitted = true
	turn.phase = awsRealtimeTurnUserFinished
	t.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeSpeechStopped,
		SpeechStopped: &llm.InputSpeechStoppedEvent{
			UserTranscriptionEnabled: true,
		},
	})
}

func (t *awsRealtimeTurnTracker) emitTranscript(turn *awsRealtimeTurn, final bool) {
	t.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     turn.inputID,
			Transcript: turn.currentTranscript(),
			IsFinal:    final,
		},
	})
}

func (t *awsRealtimeTurnTracker) maybeEmitTranscriptCompleted(turn *awsRealtimeTurn) {
	if turn.transcriptFinalEmitted {
		return
	}
	turn.transcriptFinalEmitted = true
	t.emitTranscript(turn, true)
}

func (t *awsRealtimeTurnTracker) maybeEmitGenerationCreated(turn *awsRealtimeTurn) {
	if turn.generationEmitted {
		return
	}
	turn.generationEmitted = true
	turn.phase = awsRealtimeTurnAssistantResponding
	t.emitGeneration()
}

func classifyAWSRealtimeTurnEvent(event map[string]any) string {
	if textOutput := awsRealtimeNestedMap(event, "event", "textOutput"); textOutput != nil {
		role, ok := awsRealtimeRequiredMapString(textOutput, "role")
		if !ok {
			return ""
		}
		if role == "USER" {
			return "USER_TEXT_PARTIAL"
		}
		if awsRealtimeMapString(textOutput, "content") == awsRealtimeBargeInContent {
			return "BARGE_IN"
		}
	}
	if awsRealtimeNestedString(event, "event", "contentStart", "type") == "TOOL" {
		return "TOOL_OUTPUT_CONTENT_START"
	}
	if awsRealtimeNestedString(event, "event", "contentStart", "role") == "ASSISTANT" {
		if strings.Contains(awsRealtimeNestedString(event, "event", "contentStart", "additionalModelFields"), "SPECULATIVE") {
			return "ASSISTANT_SPEC_START"
		}
	}
	if awsRealtimeNestedString(event, "event", "contentEnd", "type") == "AUDIO" {
		return "ASSISTANT_AUDIO_END"
	}
	return ""
}

func awsRealtimeNestedString(root map[string]any, path ...string) string {
	var current any = root
	for _, key := range path {
		asMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = asMap[key]
	}
	value, _ := current.(string)
	return value
}
