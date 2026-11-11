package agent

import (
	"github.com/cavos-io/rtp-agent/core/stt"
	"testing"
	"time"
)

func TestAdaptiveVADOwnershipLogsFirstTrigger(t *testing.T) {
	for _, source := range []string{"start_cooldown", "final_transcript"} {
		t.Run(source, func(t *testing.T) {
			a := newAdaptiveTranscriptActivity(t)
			log := &recordingLogger{}
			if err := a.Session.SetLogger(log); err != nil {
				t.Fatal(err)
			}
			base := time.Now()
			a.armBackchannelBoundary(base)
			if source == "start_cooldown" {
				a.startAdaptiveOverlap(base.Add(100 * time.Millisecond))
				a.startAdaptiveOverlap(base.Add(200 * time.Millisecond))
			} else {
				a.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "Oke.", Confidence: 1}}})
			}
			fields := log.infoFields["bargein.vad_owned"]
			if source == "start_cooldown" && fields["offset_from_agent_start_ms"] != float64(100) {
				t.Fatalf("onset offset = %v, want 100ms", fields["offset_from_agent_start_ms"])
			}
			if fields["trigger"] != source {
				t.Fatalf("trigger = %v, want %s", fields["trigger"], source)
			}
			n := 0
			for _, msg := range log.infoMessages {
				if msg == "bargein.vad_owned" {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("ownership logged %d times", n)
			}
			if !a.adaptiveVADOwned.Load() {
				t.Fatal("diagnostics changed ownership")
			}
			a.cancelPendingEOUDetection()
		})
	}
}

func TestInterruptionPauseLogsTrigger(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	a.adaptiveVADOwned.Store(true)
	a.restoreAudioActivityInterruption()
	log := &recordingLogger{}
	if err := a.Session.SetLogger(log); err != nil {
		t.Fatal(err)
	}
	a.Session.SetAudioOutputController(&recordingAudioOutputController{canPause: true})
	speech := NewSpeechHandle(true, DefaultInputDetails())
	a.currentSpeech = speech
	defer speech.MarkDone()
	a.interruptByAudioActivity("final transcript", "transcript", "Oke.", time.Time{})
	fields := log.infoFields["false_interruption.paused"]
	if fields["trigger"] != "final transcript" || fields["adaptive_vad_owned"] != true {
		t.Fatalf("pause attribution = %#v", fields)
	}
	a.cancelFalseInterruptionTimer()
}

func TestAdaptiveModelPauseLogsVerdictTrigger(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	log := &recordingLogger{}
	if err := a.Session.SetLogger(log); err != nil {
		t.Fatal(err)
	}
	a.Session.SetAudioOutputController(&recordingAudioOutputController{canPause: true})
	speech := NewSpeechHandle(true, DefaultInputDetails())
	a.currentSpeech = speech
	defer speech.MarkDone()
	a.setSpeaking(true)
	a.OnInterruption(OverlappingSpeechEvent{IsInterruption: true, DetectedAt: time.Now()})
	fields := log.infoFields["false_interruption.paused"]
	if fields["trigger"] != "adaptive model verdict" || fields["adaptive_vad_owned"] != false {
		t.Fatalf("model pause attribution = %#v", fields)
	}
	a.cancelFalseInterruptionTimer()
}
