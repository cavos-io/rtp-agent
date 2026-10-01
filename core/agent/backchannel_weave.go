package agent

import (
	"sort"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

// backchannelCheckpoint is a suppressed barge-in parked on the speech it overlapped,
// to be woven in when that speech commits. At is the onset, not STT-final arrival.
type backchannelCheckpoint struct {
	Text       string
	At         float64
	Confidence float64
	// SpokenChars is the caption text published when the gate suppressed this — the same
	// position the transport cuts at, so stored and live agree. Zero splits nothing.
	SpokenChars int
}

// wordsWithinChars turns a spoken-character count into a whole-word boundary, so the
// weave splits where the caption was cut. Partial words are excluded.
func wordsWithinChars(words []string, spokenChars int) int {
	used := 0
	for i, w := range words {
		if i > 0 {
			used++ // the space between words
		}
		used += len(w)
		if used > spokenChars {
			return i
		}
	}
	return len(words)
}

// wovenPart is one message of the woven commit. At is strictly increasing so a
// timestamp-sorted insert preserves the interleave.
type wovenPart struct {
	Role       llm.ChatRole
	Text       string
	At         float64
	Confidence float64
}

// weaveBackchannels splits the utterance at each checkpoint's SpokenChars, the point the
// caption was cut. Boundaries are monotonic; empty fragments are skipped.
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
		// Split where playout reached. SpokenChars == 0 means nothing was published yet,
		// so the boundary is 0 and the caller's text simply precedes the utterance.
		boundary := wordsWithinChars(words, c.SpokenChars)
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

// commitAssistantWithWeave commits one speech step split at its parked checkpoints; unpositionable
// ones fall back to transcript-only. existing (Say path) becomes the first fragment, keeping dedup.
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

// logWovenCommit prints the committed order only when a weave or fallback happened;
// a plain single commit stays quiet.
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

// insertCheckpointUserMessage inserts a checkpoint as a user message sorted by onset.
// transcriptOnly hides it from the LLM. Caller holds the chat-context mutex.
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

// insertTranscriptOnlyCheckpoints records checkpoints whose speech ended without a
// weavable commit. Caller holds the chat-context mutex.
func insertTranscriptOnlyCheckpoints(chatCtx *llm.ChatContext, cps []backchannelCheckpoint) {
	for _, c := range cps {
		insertCheckpointUserMessage(chatCtx, c.Text, c.At, c.Confidence, true)
	}
}

// applicableCheckpoints splits checkpoints into those inside the audio window (they
// weave) and the rest (transcript-only append).
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
