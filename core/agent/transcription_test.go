package agent

import (
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestTranscriptSynchronizerInterruptStopsFurtherSyncing(t *testing.T) {
	syncer := NewTranscriptSynchronizer(20)
	defer syncer.Close()

	syncer.PushText("before ")
	waitForTranscriptBuffer(t, syncer, "before ")

	syncer.Interrupt()

	if got := readTranscriptEvent(t, syncer); got != "before " {
		t.Fatalf("interrupted transcript = %q, want before", got)
	}

	syncer.PushText("after ")
	syncer.PushAudio(&model.AudioFrame{
		SampleRate:        1000,
		SamplesPerChannel: 2000,
	})

	select {
	case got := <-syncer.EventCh():
		t.Fatalf("received transcript after interrupt = %q, want no further events", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestTranscriptSynchronizerInterruptFlushesQueuedText(t *testing.T) {
	syncer := &TranscriptSynchronizer{
		textCh:  make(chan string, 2),
		eventCh: make(chan string, 2),
	}
	syncer.textCh <- "queued "
	syncer.textCh <- "text"

	syncer.Interrupt()

	if got := readTranscriptEvent(t, syncer); got != "queued text" {
		t.Fatalf("interrupted transcript = %q, want queued text", got)
	}
}

func TestTranscriptSynchronizerCloseFlushesQueuedText(t *testing.T) {
	syncer := NewTranscriptSynchronizer(20)

	syncer.PushText("closing ")
	syncer.PushText("text")
	syncer.Close()

	if got := readTranscriptEvent(t, syncer); got != "closing text" {
		t.Fatalf("closed transcript = %q, want queued text", got)
	}
}

func waitForTranscriptBuffer(t *testing.T, syncer *TranscriptSynchronizer, want string) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("textBuffer did not become %q", want)
		case <-ticker.C:
			syncer.mu.Lock()
			got := syncer.textBuffer
			syncer.mu.Unlock()
			if got == want {
				return
			}
		}
	}
}

func readTranscriptEvent(t *testing.T, syncer *TranscriptSynchronizer) string {
	t.Helper()

	select {
	case got := <-syncer.EventCh():
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript event")
		return ""
	}
}
