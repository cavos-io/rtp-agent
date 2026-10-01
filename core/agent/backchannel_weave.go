package agent

import (
	"sort"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

// backchannelCheckpoint is a suppressed barge-in transcript parked on the speech it
// overlapped, waiting to be woven into the chat context when that speech commits.
// At is unix seconds of the user's speech onset (VAD/STT), not STT-final arrival.
type backchannelCheckpoint struct {
	Text       string
	At         float64
	Confidence float64
}

// wovenPart is one chat message of the woven commit: the assistant utterance split at
// the checkpoints, with the user texts between the fragments. At is unix seconds and
// strictly increasing across the returned slice so a timestamp-sorted insert preserves
// this order.
type wovenPart struct {
	Role       llm.ChatRole
	Text       string
	At         float64
	Confidence float64
}

// weaveBackchannels ports the Python reference split (backchannel_weave.py): word
// boundaries at int(len(words)·clamp01((at-started)/duration)), monotonic so later
// checkpoints never backtrack, empty fragments skipped, empty utterance → user-only.
func weaveBackchannels(utterance string, startedAt, endedAt float64, checkpoints []backchannelCheckpoint) []wovenPart {
	ordered := make([]backchannelCheckpoint, len(checkpoints))
	copy(ordered, checkpoints)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At < ordered[j].At })

	appendPart := func(parts []wovenPart, part wovenPart) []wovenPart {
		if part.At < startedAt {
			part.At = startedAt
		}
		if len(parts) > 0 && part.At <= parts[len(parts)-1].At {
			part.At = parts[len(parts)-1].At + 0.001
		}
		return append(parts, part)
	}

	words := strings.Fields(utterance)
	if len(words) == 0 {
		parts := make([]wovenPart, 0, len(ordered))
		for _, c := range ordered {
			parts = appendPart(parts, wovenPart{Role: llm.ChatRoleUser, Text: c.Text, At: c.At, Confidence: c.Confidence})
		}
		return parts
	}

	duration := endedAt - startedAt
	if duration < 0.001 {
		duration = 0.001
	}

	boundaries := make([]int, 0, len(ordered))
	last := 0
	for _, c := range ordered {
		ratio := (c.At - startedAt) / duration
		if ratio < 0 {
			ratio = 0
		} else if ratio > 1 {
			ratio = 1
		}
		boundary := int(float64(len(words)) * ratio)
		if boundary > last {
			last = boundary
		}
		boundaries = append(boundaries, last)
	}

	fragmentAt := func(wordIdx int) float64 {
		return startedAt + float64(wordIdx)/float64(len(words))*duration
	}

	parts := make([]wovenPart, 0, 2*len(ordered)+1)
	cursor := 0
	for i, c := range ordered {
		boundary := boundaries[i]
		if fragment := strings.Join(words[cursor:boundary], " "); fragment != "" {
			parts = appendPart(parts, wovenPart{Role: llm.ChatRoleAssistant, Text: fragment, At: fragmentAt(cursor)})
		}
		parts = appendPart(parts, wovenPart{Role: llm.ChatRoleUser, Text: c.Text, At: c.At, Confidence: c.Confidence})
		cursor = boundary
	}
	if tail := strings.Join(words[cursor:], " "); tail != "" {
		parts = appendPart(parts, wovenPart{Role: llm.ChatRoleAssistant, Text: tail, At: fragmentAt(cursor)})
	}
	return parts
}

// commitAssistantWithWeave commits the assistant text for one speech step, split at the
// speech's parked backchannel checkpoints. In-window checkpoints weave: the assistant
// text becomes fragments with the LLM-visible user texts between them, timestamped so
// the chat context's CreatedAt sort preserves the interleave. Checkpoints that cannot
// be positioned (outside [startedAt, stoppedAt], or no valid window) fall back to
// transcript-only user messages, matching the non-weave suppression path. With no
// checkpoints the commit is byte-identical to today's single AddMessage.
//
// existing, when non-nil (the Say path), is the pre-built assistant message: it is
// mutated into the first assistant fragment so identity-based dedup keeps working.
// Caller holds the chat-context mutex. Returns the committed items in order.
func commitAssistantWithWeave(chatCtx *llm.ChatContext, speech *SpeechHandle, startedAt, stoppedAt float64, args llm.ChatMessageArgs, existing *llm.ChatMessage) []llm.ChatItem {
	var cps []backchannelCheckpoint
	if speech != nil {
		cps = speech.takeBackchannelCheckpoints()
	}
	var in, out []backchannelCheckpoint
	if startedAt > 0 && stoppedAt > startedAt {
		in, out = applicableCheckpoints(cps, startedAt, stoppedAt)
	} else {
		out = cps
	}

	items := make([]llm.ChatItem, 0, 2*len(in)+1+len(out))
	commitAssistant := func(a llm.ChatMessageArgs, first bool) llm.ChatItem {
		if existing != nil && first {
			existing.Content = []llm.ChatContent{{Text: a.Text}}
			if !a.CreatedAt.IsZero() {
				existing.CreatedAt = a.CreatedAt
			}
			insertChatItemIfMissing(chatCtx, existing)
			return existing
		}
		return chatCtx.AddMessage(a)
	}
	insertUser := func(text string, at float64, confidence float64, transcriptOnly bool) llm.ChatItem {
		return insertCheckpointUserMessage(chatCtx, text, at, confidence, transcriptOnly)
	}

	if len(in) == 0 {
		if existing != nil {
			// Preserve the Say path's semantics: full text, message's own CreatedAt.
			items = append(items, commitAssistant(llm.ChatMessageArgs{Text: args.Text}, true))
		} else {
			items = append(items, commitAssistant(args, true))
		}
	} else {
		firstAssistant := true
		for _, part := range weaveBackchannels(args.Text, startedAt, stoppedAt, in) {
			if part.Role == llm.ChatRoleAssistant {
				fragArgs := llm.ChatMessageArgs{
					Role:        llm.ChatRoleAssistant,
					Text:        part.Text,
					Interrupted: args.Interrupted,
					CreatedAt:   unixSecondsToTime(part.At),
				}
				if firstAssistant {
					// The step's identity (ID/Extra/Metrics) lives on the first fragment.
					fragArgs.ID = args.ID
					fragArgs.Extra = args.Extra
					fragArgs.Metrics = args.Metrics
				}
				items = append(items, commitAssistant(fragArgs, firstAssistant))
				firstAssistant = false
			} else {
				items = append(items, insertUser(part.Text, part.At, part.Confidence, false))
			}
		}
	}
	for _, c := range out {
		items = append(items, insertUser(c.Text, c.At, c.Confidence, true))
	}
	return items
}

// logWovenCommit prints the committed order when a weave (or fallback) actually
// happened — a single plain assistant commit stays quiet. Terminal pinpoint for
// "was the agent speech split and stored".
func logWovenCommit(session *AgentSession, committed []llm.ChatItem, startedAt, stoppedAt float64) {
	if session == nil || len(committed) <= 1 {
		return
	}
	order := make([]string, 0, len(committed))
	for _, item := range committed {
		m, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		text := []rune(m.TextContent())
		if len(text) > 40 {
			text = append(text[:40], '…')
		}
		tag := string(m.Role)
		if m.TranscriptOnly {
			tag += "(transcript_only)"
		}
		order = append(order, tag+": "+string(text))
	}
	session.Logger().Infow("backchannel_weave.commit",
		"parts", len(committed),
		"window_started", startedAt,
		"window_stopped", stoppedAt,
		"order", strings.Join(order, " | "))
}

// insertCheckpointUserMessage inserts one checkpoint as a user message, sorted by its
// onset timestamp. transcriptOnly hides it from the LLM (the non-weave fallback);
// woven messages pass false and stay visible. Caller holds the chat-context mutex.
func insertCheckpointUserMessage(chatCtx *llm.ChatContext, text string, at, confidence float64, transcriptOnly bool) llm.ChatItem {
	conf := confidence
	msg := &llm.ChatMessage{
		Role:                 llm.ChatRoleUser,
		Content:              []llm.ChatContent{{Text: text}},
		TranscriptConfidence: &conf,
		TranscriptOnly:       transcriptOnly,
		CreatedAt:            unixSecondsToTime(at),
	}
	chatCtx.Insert(msg)
	return msg
}

// insertTranscriptOnlyCheckpoints records checkpoints that could never be woven (their
// speech ended without a weavable commit) as transcript-only user messages. Caller
// holds the chat-context mutex.
func insertTranscriptOnlyCheckpoints(chatCtx *llm.ChatContext, cps []backchannelCheckpoint) {
	for _, c := range cps {
		insertCheckpointUserMessage(chatCtx, c.Text, c.At, c.Confidence, true)
	}
}

// committedFragment is one assistant chat message of a committed utterance with the
// audio sub-window it narrates. Sub-windows partition [startedAt, stoppedAt].
type committedFragment struct {
	msg      *llm.ChatMessage
	winStart float64
	winEnd   float64
}

// assistantCommitWindow retains the last committed assistant utterance so a suppressed
// backchannel whose final arrived after the commit can still be spliced into it.
type assistantCommitWindow struct {
	startedAt float64
	stoppedAt float64
	fragments []committedFragment
}

// buildAssistantCommitWindow derives the fragment windows of a finished commit from
// the committed items' order and CreatedAt stamps. Nil when there is no valid audio
// window or no LLM-visible assistant fragment — then no splice target exists.
func buildAssistantCommitWindow(committed []llm.ChatItem, startedAt, stoppedAt float64) *assistantCommitWindow {
	if startedAt <= 0 || stoppedAt <= startedAt {
		return nil
	}
	var msgs []*llm.ChatMessage
	for _, item := range committed {
		if m, ok := item.(*llm.ChatMessage); ok && m.Role == llm.ChatRoleAssistant && !m.TranscriptOnly {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	w := &assistantCommitWindow{startedAt: startedAt, stoppedAt: stoppedAt}
	for i, m := range msgs {
		start := startedAt
		if i > 0 {
			start = timeToUnixSeconds(m.CreatedAt)
		}
		end := stoppedAt
		if i+1 < len(msgs) {
			end = timeToUnixSeconds(msgs[i+1].CreatedAt)
		}
		w.fragments = append(w.fragments, committedFragment{msg: m, winStart: start, winEnd: end})
	}
	return w
}

// splice inserts one suppressed backchannel into an already-committed assistant
// utterance: the fragment narrating the onset instant is word-split at the time ratio
// (same arithmetic as weaveBackchannels) and the user text lands between the halves,
// LLM-visible. Ratio-0/1 onsets insert the user message without splitting so no empty
// fragment is created. Returns the inserted items, nil when at is outside the window.
// Caller holds the chat-context mutex.
func (w *assistantCommitWindow) splice(chatCtx *llm.ChatContext, text string, at, confidence float64) []llm.ChatItem {
	if w == nil || chatCtx == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	if at < w.startedAt || at > w.stoppedAt {
		return nil
	}
	idx := -1
	for i := range w.fragments {
		if at >= w.fragments[i].winStart && (at < w.fragments[i].winEnd || i == len(w.fragments)-1) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	frag := &w.fragments[idx]
	words := strings.Fields(frag.msg.TextContent())
	span := frag.winEnd - frag.winStart
	if span < 0.001 {
		span = 0.001
	}
	ratio := (at - frag.winStart) / span
	if ratio < 0 {
		ratio = 0
	} else if ratio > 1 {
		ratio = 1
	}
	boundary := int(float64(len(words)) * ratio)
	left := strings.Join(words[:boundary], " ")
	right := strings.Join(words[boundary:], " ")
	userAt := at
	if left == "" {
		// Onset at the fragment's very start: ChatContext inserts timestamp ties AFTER
		// existing items, so nudge the user message ahead of the untouched fragment to
		// keep the conversational order user → assistant.
		userAt = frag.winStart - 0.001
	}
	userMsg := insertCheckpointUserMessage(chatCtx, text, userAt, confidence, false)
	items := []llm.ChatItem{userMsg}
	if left == "" || right == "" {
		// Edge onset: no split — an empty fragment must never be created.
		return items
	}
	frag.msg.Content = []llm.ChatContent{{Text: left}}
	tail := &llm.ChatMessage{
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: right}},
		CreatedAt: unixSecondsToTime(at + 0.001),
	}
	chatCtx.Insert(tail)
	items = append(items, tail)
	oldEnd := frag.winEnd
	frag.winEnd = at
	w.fragments = append(w.fragments, committedFragment{})
	copy(w.fragments[idx+2:], w.fragments[idx+1:])
	w.fragments[idx+1] = committedFragment{msg: tail, winStart: at, winEnd: oldEnd}
	return items
}

// applicableCheckpoints splits checkpoints into those inside the [startedAt, stoppedAt]
// audio window (inclusive — they weave) and the rest (they fall back to a
// transcript-only append).
func applicableCheckpoints(checkpoints []backchannelCheckpoint, startedAt, stoppedAt float64) (in, out []backchannelCheckpoint) {
	for _, c := range checkpoints {
		if c.At >= startedAt && c.At <= stoppedAt {
			in = append(in, c)
		} else {
			out = append(out, c)
		}
	}
	return in, out
}
