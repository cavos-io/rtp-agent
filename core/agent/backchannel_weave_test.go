package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestWeaveBackchannelsEmptyUtteranceReturnsUserOnlyParts(t *testing.T) {
	parts := weaveBackchannels("", 100, 110, []backchannelCheckpoint{
		{Text: "iya", At: 101, Confidence: 0.9},
		{Text: "oke", At: 105, Confidence: 0.8},
	})
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 user-only parts", len(parts))
	}
	for i, p := range parts {
		if p.Role != llm.ChatRoleUser {
			t.Fatalf("parts[%d].Role = %s, want user", i, p.Role)
		}
	}
	if parts[0].Text != "iya" || parts[1].Text != "oke" {
		t.Fatalf("texts = %q,%q, want iya,oke", parts[0].Text, parts[1].Text)
	}
}

func TestWeaveBackchannelsSplitsAtIntFloorBoundary(t *testing.T) {
	// 10 words, checkpoint at ratio 0.55 → int(10*0.55) = 5.
	utterance := "w1 w2 w3 w4 w5 w6 w7 w8 w9 w10"
	parts := weaveBackchannels(utterance, 100, 110, []backchannelCheckpoint{
		{Text: "iya", At: 105.5, Confidence: 0.9},
	})
	want := []struct {
		role llm.ChatRole
		text string
	}{
		{llm.ChatRoleAssistant, "w1 w2 w3 w4 w5"},
		{llm.ChatRoleUser, "iya"},
		{llm.ChatRoleAssistant, "w6 w7 w8 w9 w10"},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %d, want %d: %+v", len(parts), len(want), parts)
	}
	for i, w := range want {
		if parts[i].Role != w.role || parts[i].Text != w.text {
			t.Fatalf("parts[%d] = {%s %q}, want {%s %q}", i, parts[i].Role, parts[i].Text, w.role, w.text)
		}
	}
	if parts[1].Confidence != 0.9 {
		t.Fatalf("user part Confidence = %v, want 0.9", parts[1].Confidence)
	}
}

func TestWeaveBackchannelsClampsRatioBelowZero(t *testing.T) {
	// Checkpoint before startedAt → boundary 0 → user first, full utterance as tail.
	parts := weaveBackchannels("a b c", 100, 110, []backchannelCheckpoint{
		{Text: "iya", At: 90, Confidence: 1},
	})
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2: %+v", len(parts), parts)
	}
	if parts[0].Role != llm.ChatRoleUser || parts[0].Text != "iya" {
		t.Fatalf("parts[0] = {%s %q}, want user iya", parts[0].Role, parts[0].Text)
	}
	if parts[1].Role != llm.ChatRoleAssistant || parts[1].Text != "a b c" {
		t.Fatalf("parts[1] = {%s %q}, want assistant full text", parts[1].Role, parts[1].Text)
	}
}

func TestWeaveBackchannelsClampsRatioAboveOne(t *testing.T) {
	// Checkpoint after endedAt → boundary len(words) → full text, then user.
	parts := weaveBackchannels("a b c", 100, 110, []backchannelCheckpoint{
		{Text: "iya", At: 999, Confidence: 1},
	})
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2: %+v", len(parts), parts)
	}
	if parts[0].Role != llm.ChatRoleAssistant || parts[0].Text != "a b c" {
		t.Fatalf("parts[0] = {%s %q}, want assistant full text", parts[0].Role, parts[0].Text)
	}
	if parts[1].Role != llm.ChatRoleUser || parts[1].Text != "iya" {
		t.Fatalf("parts[1] = {%s %q}, want user iya", parts[1].Role, parts[1].Text)
	}
}

func TestWeaveBackchannelsSortsOutOfOrderCheckpointsMonotonically(t *testing.T) {
	// Given out of order; later checkpoint maps to earlier boundary must not backtrack.
	utterance := "w1 w2 w3 w4 w5 w6 w7 w8 w9 w10"
	parts := weaveBackchannels(utterance, 100, 110, []backchannelCheckpoint{
		{Text: "second", At: 108, Confidence: 1}, // ratio .8 → 8
		{Text: "first", At: 103, Confidence: 1},  // ratio .3 → 3
	})
	want := []struct {
		role llm.ChatRole
		text string
	}{
		{llm.ChatRoleAssistant, "w1 w2 w3"},
		{llm.ChatRoleUser, "first"},
		{llm.ChatRoleAssistant, "w4 w5 w6 w7 w8"},
		{llm.ChatRoleUser, "second"},
		{llm.ChatRoleAssistant, "w9 w10"},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %d, want %d: %+v", len(parts), len(want), parts)
	}
	for i, w := range want {
		if parts[i].Role != w.role || parts[i].Text != w.text {
			t.Fatalf("parts[%d] = {%s %q}, want {%s %q}", i, parts[i].Role, parts[i].Text, w.role, w.text)
		}
	}
}

func TestWeaveBackchannelsSameBoundarySkipsEmptyFragment(t *testing.T) {
	// Two checkpoints in the same word gap → no empty assistant fragment between them.
	utterance := "w1 w2 w3 w4"
	parts := weaveBackchannels(utterance, 100, 110, []backchannelCheckpoint{
		{Text: "iya", At: 105, Confidence: 1},  // ratio .5 → 2
		{Text: "oke", At: 105.4, Confidence: 1}, // ratio .54 → 2
	})
	want := []struct {
		role llm.ChatRole
		text string
	}{
		{llm.ChatRoleAssistant, "w1 w2"},
		{llm.ChatRoleUser, "iya"},
		{llm.ChatRoleUser, "oke"},
		{llm.ChatRoleAssistant, "w3 w4"},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %d, want %d: %+v", len(parts), len(want), parts)
	}
	for i, w := range want {
		if parts[i].Role != w.role || parts[i].Text != w.text {
			t.Fatalf("parts[%d] = {%s %q}, want {%s %q}", i, parts[i].Role, parts[i].Text, w.role, w.text)
		}
	}
}

func TestWeaveBackchannelsNormalizesWhitespace(t *testing.T) {
	// Python str.split() semantics: runs of whitespace collapse, fragments re-join with single spaces.
	parts := weaveBackchannels("  a   b\tc  ", 100, 110, nil)
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1: %+v", len(parts), parts)
	}
	if parts[0].Text != "a b c" {
		t.Fatalf("text = %q, want %q", parts[0].Text, "a b c")
	}
	if strings.Contains(parts[0].Text, "  ") {
		t.Fatalf("text %q contains double space", parts[0].Text)
	}
}

func TestWeaveBackchannelsPartTimesStrictlyIncrease(t *testing.T) {
	utterance := "w1 w2 w3 w4 w5 w6 w7 w8 w9 w10"
	parts := weaveBackchannels(utterance, 100, 110, []backchannelCheckpoint{
		{Text: "a", At: 105, Confidence: 1},
		{Text: "b", At: 105.01, Confidence: 1},
		{Text: "c", At: 90, Confidence: 1}, // clamped to boundary 0
	})
	for i := 1; i < len(parts); i++ {
		if parts[i].At <= parts[i-1].At {
			t.Fatalf("parts[%d].At = %v <= parts[%d].At = %v; want strictly increasing: %+v",
				i, parts[i].At, i-1, parts[i-1].At, parts)
		}
	}
	if parts[0].At < 100 {
		// First part may be the clamped user checkpoint; it must still be anchored at
		// or after the utterance start so exporter StartAt stays sane.
		t.Fatalf("parts[0].At = %v, want >= startedAt", parts[0].At)
	}
}

func TestWeaveBackchannelsZeroDurationUsesFloor(t *testing.T) {
	// ended == started → duration floor 0.001; must not divide by zero or panic.
	parts := weaveBackchannels("a b", 100, 100, []backchannelCheckpoint{
		{Text: "iya", At: 100, Confidence: 1},
	})
	if len(parts) == 0 {
		t.Fatal("no parts returned")
	}
}

func TestSpeechHandleBackchannelCheckpointsAddAndDrain(t *testing.T) {
	h := NewSpeechHandle(true, DefaultInputDetails())
	h.AddBackchannelCheckpoint(backchannelCheckpoint{Text: "iya", At: 100, Confidence: 0.9})
	h.AddBackchannelCheckpoint(backchannelCheckpoint{Text: "oke", At: 101, Confidence: 0.8})

	got := h.takeBackchannelCheckpoints()
	if len(got) != 2 || got[0].Text != "iya" || got[1].Text != "oke" {
		t.Fatalf("takeBackchannelCheckpoints = %+v, want iya,oke in order", got)
	}
	if again := h.takeBackchannelCheckpoints(); len(again) != 0 {
		t.Fatalf("second take = %+v, want drained empty", again)
	}
}

func TestAgentActivityWeaveParksCheckpointOnCurrentSpeech(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	agent.AudioTurnDetector = &recordingAudioTurnDetector{probability: 0.9}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		BargeInDecider:                    fakeIgnoreBargeInDecider{},
		RecordSuppressedBargeInTranscript: true,
		WeaveSuppressedBargeIn:            true,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	onset := time.Now().Add(-300 * time.Millisecond)
	activity.userSpeechStartedAt = onset
	activity.appendSpeechEpoch(onset, true)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "oke", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("suppressed barge-in must not commit a turn, got %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
	if agent.ChatCtx != nil && len(agent.ChatCtx.Items) != 0 {
		t.Fatalf("weave mode must not append to ChatCtx at suppression time, got %v", agent.ChatCtx.Items)
	}
	cps := current.takeBackchannelCheckpoints()
	if len(cps) != 1 {
		t.Fatalf("checkpoints on current speech = %+v, want exactly one", cps)
	}
	if cps[0].Text != "oke" || cps[0].Confidence != 0.9 {
		t.Fatalf("checkpoint = %+v, want text oke conf 0.9", cps[0])
	}
	wantAt := float64(onset.UnixNano()) / 1e9
	if diff := cps[0].At - wantAt; diff < -0.001 || diff > 0.001 {
		t.Fatalf("checkpoint At = %v, want user speech onset %v (VAD onset, not STT-final time)", cps[0].At, wantAt)
	}
}

func TestAgentActivityWeaveFallsBackToTranscriptOnlyWithoutCurrentSpeech(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	agent.AudioTurnDetector = &recordingAudioTurnDetector{probability: 0.9}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		BargeInDecider:                    fakeIgnoreBargeInDecider{},
		RecordSuppressedBargeInTranscript: true,
		WeaveSuppressedBargeIn:            true,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.appendSpeechEpoch(time.Now().Add(-300*time.Millisecond), true)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "oke", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("suppressed barge-in must not commit a turn, got %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
	if agent.ChatCtx == nil || len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("no current speech: want TranscriptOnly fallback item, got %v", agent.ChatCtx)
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok || !msg.TranscriptOnly || msg.TextContent() != "oke" {
		t.Fatalf("fallback item = %#v, want TranscriptOnly user message oke", agent.ChatCtx.Items[0])
	}
}

func speechWithCheckpoints(cps ...backchannelCheckpoint) *SpeechHandle {
	h := NewSpeechHandle(true, DefaultInputDetails())
	for _, cp := range cps {
		h.AddBackchannelCheckpoint(cp)
	}
	return h
}

func TestCommitAssistantWithWeaveInterleavesInWindowCheckpoints(t *testing.T) {
	chatCtx := llm.NewChatContext()
	speech := speechWithCheckpoints(backchannelCheckpoint{Text: "iya", At: 105.5, Confidence: 0.9})
	args := llm.ChatMessageArgs{
		ID:          "item_first",
		Role:        llm.ChatRoleAssistant,
		Text:        "w1 w2 w3 w4 w5 w6 w7 w8 w9 w10",
		Interrupted: false,
		CreatedAt:   unixSecondsToTime(100),
		Extra:       map[string]any{"trace_id": "t1"},
		Metrics:     map[string]any{"m": 1},
	}
	items := commitAssistantWithWeave(chatCtx, speech, 100, 110, args, nil)

	if len(items) != 3 || len(chatCtx.Items) != 3 {
		t.Fatalf("items = %d, chatCtx = %d, want 3 each: %+v", len(items), len(chatCtx.Items), chatCtx.Items)
	}
	first, _ := chatCtx.Items[0].(*llm.ChatMessage)
	user, _ := chatCtx.Items[1].(*llm.ChatMessage)
	tail, _ := chatCtx.Items[2].(*llm.ChatMessage)
	if first == nil || first.Role != llm.ChatRoleAssistant || first.TextContent() != "w1 w2 w3 w4 w5" {
		t.Fatalf("items[0] = %#v, want first assistant fragment", chatCtx.Items[0])
	}
	if first.ID != "item_first" || first.Extra["trace_id"] != "t1" || first.Metrics["m"] != 1 {
		t.Fatalf("first fragment must carry ID/Extra/Metrics: %#v", first)
	}
	if user == nil || user.Role != llm.ChatRoleUser || user.TextContent() != "iya" {
		t.Fatalf("items[1] = %#v, want woven user message", chatCtx.Items[1])
	}
	if user.TranscriptOnly {
		t.Fatal("woven user message must be LLM-visible (TranscriptOnly=false)")
	}
	if user.TranscriptConfidence == nil || *user.TranscriptConfidence != 0.9 {
		t.Fatalf("woven user confidence = %v, want 0.9", user.TranscriptConfidence)
	}
	if tail == nil || tail.Role != llm.ChatRoleAssistant || tail.TextContent() != "w6 w7 w8 w9 w10" {
		t.Fatalf("items[2] = %#v, want tail assistant fragment", chatCtx.Items[2])
	}
	if tail.ID == "" || tail.ID == first.ID {
		t.Fatalf("tail fragment ID = %q, want fresh non-empty ID distinct from first %q", tail.ID, first.ID)
	}
	if !first.CreatedAt.Before(user.CreatedAt) || !user.CreatedAt.Before(tail.CreatedAt) {
		t.Fatalf("CreatedAt not increasing: %v / %v / %v", first.CreatedAt, user.CreatedAt, tail.CreatedAt)
	}
	// The LLM filter must keep the woven user message.
	if filtered := excludeTranscriptOnlyItems(chatCtx); len(filtered.Items) != 3 {
		t.Fatalf("excludeTranscriptOnlyItems dropped woven items: %d, want 3", len(filtered.Items))
	}
	if speech.takeBackchannelCheckpoints() != nil {
		t.Fatal("checkpoints must be drained by commit")
	}
}

func TestCommitAssistantWithWeaveOutOfWindowFallsBackToTranscriptOnly(t *testing.T) {
	chatCtx := llm.NewChatContext()
	speech := speechWithCheckpoints(backchannelCheckpoint{Text: "iya", At: 50, Confidence: 0.7})
	args := llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "a b c", CreatedAt: unixSecondsToTime(100)}
	items := commitAssistantWithWeave(chatCtx, speech, 100, 110, args, nil)

	if len(items) != 2 || len(chatCtx.Items) != 2 {
		t.Fatalf("items = %d, chatCtx = %d, want 2 (assistant + fallback): %+v", len(items), len(chatCtx.Items), chatCtx.Items)
	}
	assistant, _ := chatCtx.Items[1].(*llm.ChatMessage)
	fallback, _ := chatCtx.Items[0].(*llm.ChatMessage)
	if assistant == nil || assistant.Role != llm.ChatRoleAssistant || assistant.TextContent() != "a b c" {
		t.Fatalf("assistant = %#v, want unsplit text", chatCtx.Items[1])
	}
	if fallback == nil || fallback.Role != llm.ChatRoleUser || !fallback.TranscriptOnly || fallback.TextContent() != "iya" {
		t.Fatalf("fallback = %#v, want TranscriptOnly user iya", chatCtx.Items[0])
	}
}

func TestCommitAssistantWithWeaveNoCheckpointsMatchesPlainAddMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	args := llm.ChatMessageArgs{ID: "item_x", Role: llm.ChatRoleAssistant, Text: "plain", CreatedAt: unixSecondsToTime(100)}
	items := commitAssistantWithWeave(chatCtx, speech, 100, 110, args, nil)
	if len(items) != 1 || len(chatCtx.Items) != 1 {
		t.Fatalf("want exactly one committed message, got %d/%d", len(items), len(chatCtx.Items))
	}
	msg, _ := chatCtx.Items[0].(*llm.ChatMessage)
	if msg == nil || msg.ID != "item_x" || msg.TextContent() != "plain" {
		t.Fatalf("msg = %#v, want plain AddMessage semantics", chatCtx.Items[0])
	}
}

func TestCommitAssistantWithWeaveInvalidWindowFallsBackAll(t *testing.T) {
	// TTS never reported a speaking window → cannot position; checkpoints become
	// TranscriptOnly, assistant commits unsplit.
	chatCtx := llm.NewChatContext()
	speech := speechWithCheckpoints(backchannelCheckpoint{Text: "iya", At: 105, Confidence: 1})
	args := llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "a b", CreatedAt: unixSecondsToTime(100)}
	items := commitAssistantWithWeave(chatCtx, speech, 0, 0, args, nil)
	if len(items) != 2 {
		t.Fatalf("items = %d, want assistant + TranscriptOnly fallback: %+v", len(items), chatCtx.Items)
	}
	var sawTranscriptOnly bool
	for _, it := range chatCtx.Items {
		if m, ok := it.(*llm.ChatMessage); ok && m.TranscriptOnly {
			sawTranscriptOnly = true
		}
	}
	if !sawTranscriptOnly {
		t.Fatalf("no TranscriptOnly fallback in %+v", chatCtx.Items)
	}
}

func TestCommitAssistantWithWeaveMutatesExistingSayMessageAsFirstFragment(t *testing.T) {
	chatCtx := llm.NewChatContext()
	speech := speechWithCheckpoints(backchannelCheckpoint{Text: "iya", At: 105, Confidence: 1})
	existing := &llm.ChatMessage{
		ID:        "say_msg",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "original full text"}},
		CreatedAt: unixSecondsToTime(99),
	}
	args := llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "w1 w2 w3 w4", CreatedAt: unixSecondsToTime(100)}
	items := commitAssistantWithWeave(chatCtx, speech, 100, 110, args, existing)

	if len(items) != 3 || len(chatCtx.Items) != 3 {
		t.Fatalf("items = %d, chatCtx = %d, want 3: %+v", len(items), len(chatCtx.Items), chatCtx.Items)
	}
	if chatCtx.Items[0] != llm.ChatItem(existing) {
		t.Fatalf("items[0] = %#v, want the mutated existing Say message first", chatCtx.Items[0])
	}
	if existing.TextContent() != "w1 w2" {
		t.Fatalf("existing.Text = %q, want first fragment %q", existing.TextContent(), "w1 w2")
	}
	// Committing again must not duplicate the existing message (identity dedup).
	count := 0
	for _, it := range chatCtx.Items {
		if it == llm.ChatItem(existing) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("existing message appears %d times, want 1", count)
	}
}

func TestInterruptOrderingTruncatedAssistantSortsBeforeUserTurn(t *testing.T) {
	// Regression: interrupting user turn Appends at tail with CreatedAt=now; the
	// truncated assistant then Inserts with CreatedAt=StartedSpeakingAt (earlier)
	// and must land BEFORE the user turn.
	chatCtx := llm.NewChatContext()
	started := float64(time.Now().Add(-5*time.Second).UnixNano()) / 1e9
	chatCtx.Append(&llm.ChatMessage{
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "tunggu dulu"}},
		CreatedAt: time.Now(),
	})
	chatCtx.AddMessage(llm.ChatMessageArgs{
		Role:        llm.ChatRoleAssistant,
		Text:        "truncated spoken",
		Interrupted: true,
		CreatedAt:   unixSecondsToTime(started),
	})
	first, _ := chatCtx.Items[0].(*llm.ChatMessage)
	second, _ := chatCtx.Items[1].(*llm.ChatMessage)
	if first == nil || first.Role != llm.ChatRoleAssistant {
		t.Fatalf("items[0] = %#v, want truncated assistant first", chatCtx.Items[0])
	}
	if second == nil || second.Role != llm.ChatRoleUser {
		t.Fatalf("items[1] = %#v, want interrupting user turn second", chatCtx.Items[1])
	}
}

func TestApplicableCheckpointsSplitsByWindow(t *testing.T) {
	cps := []backchannelCheckpoint{
		{Text: "early", At: 99},
		{Text: "in1", At: 100},
		{Text: "in2", At: 110},
		{Text: "late", At: 110.5},
	}
	in, out := applicableCheckpoints(cps, 100, 110)
	if len(in) != 2 || in[0].Text != "in1" || in[1].Text != "in2" {
		t.Fatalf("in = %+v, want in1,in2 (window inclusive)", in)
	}
	if len(out) != 2 || out[0].Text != "early" || out[1].Text != "late" {
		t.Fatalf("out = %+v, want early,late", out)
	}
}

func TestBuildAssistantCommitWindowSingleFragment(t *testing.T) {
	chatCtx := llm.NewChatContext()
	msg := chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "halo selamat pagi"})
	w := buildAssistantCommitWindow([]llm.ChatItem{msg}, 100.0, 106.0)
	if w == nil || len(w.fragments) != 1 {
		t.Fatalf("window = %+v, want 1 fragment", w)
	}
	f := w.fragments[0]
	if f.msg != msg || f.winStart != 100.0 || f.winEnd != 106.0 {
		t.Fatalf("fragment = %+v, want full window on the committed message", f)
	}
}

func TestBuildAssistantCommitWindowSkipsInvalid(t *testing.T) {
	chatCtx := llm.NewChatContext()
	msg := chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "halo"})
	if w := buildAssistantCommitWindow([]llm.ChatItem{msg}, 0, 0); w != nil {
		t.Fatal("no audio window: want nil")
	}
	user := chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "ya"})
	if w := buildAssistantCommitWindow([]llm.ChatItem{user}, 100, 106); w != nil {
		t.Fatal("no assistant items: want nil")
	}
}

func TestBuildAssistantCommitWindowWovenFragments(t *testing.T) {
	// A woven commit: assistant / user / assistant. Sub-windows come from the
	// second fragment's CreatedAt.
	chatCtx := llm.NewChatContext()
	first := chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "halo selamat", CreatedAt: unixSecondsToTime(100.0)})
	userMsg := insertCheckpointUserMessage(chatCtx, "ya", 103.0, 0.9, false)
	second := chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "pagi semua", CreatedAt: unixSecondsToTime(103.5)})
	w := buildAssistantCommitWindow([]llm.ChatItem{first, userMsg, second}, 100.0, 106.0)
	if w == nil || len(w.fragments) != 2 {
		t.Fatalf("window = %+v, want 2 assistant fragments", w)
	}
	if w.fragments[0].winStart != 100.0 || w.fragments[0].winEnd != 103.5 {
		t.Fatalf("fragment[0] window = [%v,%v], want [100,103.5]", w.fragments[0].winStart, w.fragments[0].winEnd)
	}
	if w.fragments[1].winStart != 103.5 || w.fragments[1].winEnd != 106.0 {
		t.Fatalf("fragment[1] window = [%v,%v], want [103.5,106]", w.fragments[1].winStart, w.fragments[1].winEnd)
	}
}
