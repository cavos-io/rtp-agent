package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
)

const lkJoinTTL = 20 * time.Second

// lkMetricsRecorder subscribes to an AgentSession's Timeline and records
// lk_agents_* metrics derived from pipeline timing events.
type lkMetricsRecorder struct {
	attrs telemetry.LKMetricsAttrs

	mu sync.Mutex

	// timing state for composing derived metrics
	speechEndAt   time.Time // set on VADEndedEvent
	transcriptAt  time.Time // set on final UserInputTranscribedEvent
	thinkingStart time.Time // set when AgentState → Thinking
	llmTTFT       float64   // set on LLMFirstTokenEvent
	llmFirstTokAt time.Time // set on LLMFirstTokenEvent
}

func newLKMetricsRecorder(attrs telemetry.LKMetricsAttrs) *lkMetricsRecorder {
	return &lkMetricsRecorder{attrs: attrs}
}

func (r *lkMetricsRecorder) onEvent(ev *AgentEvent) {
	ctx := context.Background()
	a := r.attrs

	switch ev.Type {
	case "vad_ended":
		if ev.VADEnded != nil {
			r.mu.Lock()
			r.speechEndAt = ev.VADEnded.CreatedAt
			r.mu.Unlock()
		}

	case "user_input_transcribed":
		if ev.UserInputTranscribed == nil || !ev.UserInputTranscribed.IsFinal {
			return
		}
		now := ev.UserInputTranscribed.CreatedAt
		r.mu.Lock()
		speechEndAt := r.speechEndAt
		r.transcriptAt = now
		r.mu.Unlock()

		if !speechEndAt.IsZero() && time.Since(speechEndAt) < lkJoinTTL {
			telemetry.RecordLKTranscriptionDelay(ctx, now.Sub(speechEndAt).Seconds(), a)
		}

	case "agent_state_changed":
		if ev.AgentStateChanged == nil {
			return
		}
		sc := ev.AgentStateChanged
		if sc.NewState == AgentStateThinking {
			r.mu.Lock()
			r.thinkingStart = sc.CreatedAt
			r.mu.Unlock()
		}
		if sc.OldState == AgentStateThinking && sc.NewState == AgentStateSpeaking {
			r.mu.Lock()
			thinkingStart := r.thinkingStart
			speechEndAt := r.speechEndAt
			ttft := r.llmTTFT
			r.mu.Unlock()

			if !thinkingStart.IsZero() {
				// TTS TTFB ≈ elapsed since LLM first token → first audio byte
				elapsed := sc.CreatedAt.Sub(thinkingStart).Seconds()
				ttfb := elapsed - ttft
				if ttfb > 0 {
					telemetry.RecordLKTTSTTFB(ctx, ttfb, a)
				}
			}
			if !speechEndAt.IsZero() && time.Since(speechEndAt) < lkJoinTTL {
				// E2E latency = from user stops speaking to first audio byte
				telemetry.RecordLKE2ELatency(ctx, sc.CreatedAt.Sub(speechEndAt).Seconds(), a)
			}
		}

	case "llm_first_token":
		if ev.LLMFirstToken == nil || ev.LLMFirstToken.TTFT < 0 {
			return
		}
		now := ev.LLMFirstToken.CreatedAt
		ttft := ev.LLMFirstToken.TTFT
		r.mu.Lock()
		r.llmTTFT = ttft
		r.llmFirstTokAt = now
		transcriptAt := r.transcriptAt
		speechEndAt := r.speechEndAt
		r.mu.Unlock()

		telemetry.RecordLKLLMTTFT(ctx, ttft, a)

		if !transcriptAt.IsZero() && time.Since(transcriptAt) < lkJoinTTL {
			telemetry.RecordLKOnUserTurnCompleted(ctx, now.Sub(transcriptAt).Seconds(), a)
		}
		if !speechEndAt.IsZero() && time.Since(speechEndAt) < lkJoinTTL {
			// end_of_turn_delay = speech end → LLM first token
			telemetry.RecordLKEndOfTurnDelay(ctx, now.Sub(speechEndAt).Seconds(), a)
		}

	case "metrics_collected":
		if ev.MetricsCollected == nil {
			return
		}
		if llmM, ok := ev.MetricsCollected.Metrics.(*telemetry.LLMMetrics); ok && llmM != nil && llmM.CompletionTokens > 0 {
			telemetry.AddLKLLMOutputTokens(ctx, int64(llmM.CompletionTokens), a)
		}
	}
}
