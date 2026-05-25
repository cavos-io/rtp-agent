package telemetry

import (
	"context"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var lkJobCount atomic.Int64

type lkInstruments struct {
	cActiveJobs          metric.Int64UpDownCounter
	hProcInit            metric.Float64Histogram
	hE2ELatency          metric.Float64Histogram
	hEndOfTurnDelay      metric.Float64Histogram
	hTranscriptionDelay  metric.Float64Histogram
	hLLMTTFT             metric.Float64Histogram
	hOnUserTurnCompleted metric.Float64Histogram
	hTTSTTFB             metric.Float64Histogram
	cLLMOutputTokens     metric.Int64Counter
	gWorkerLoad          metric.Float64Gauge
}

var (
	lkOnce  sync.Once
	lkInstr *lkInstruments
)

func ensureLK() *lkInstruments {
	lkOnce.Do(func() {
		m := otel.GetMeterProvider().Meter("rtp-agent")
		ins := &lkInstruments{}

		hist := func(name, unit, desc string) metric.Float64Histogram {
			h, _ := m.Float64Histogram(name, metric.WithUnit(unit), metric.WithDescription(desc))
			return h
		}
		ins.cActiveJobs, _ = m.Int64UpDownCounter("lk_agents_active_job_count",
			metric.WithUnit("1"), metric.WithDescription("Currently active agent jobs"))
		ins.hProcInit = hist("lk_agents_proc_initialize_duration", "s",
			"Agent process initialization duration (job received → on-call)")
		ins.hE2ELatency = hist("lk_agents_turn_e2e_latency", "s",
			"End-to-end turn latency (speech end → first audio byte)")
		ins.hEndOfTurnDelay = hist("lk_agents_turn_end_of_turn_delay", "s",
			"End-of-utterance delay (speech end → LLM first token)")
		ins.hTranscriptionDelay = hist("lk_agents_turn_transcription_delay", "s",
			"STT transcription delay (speech end → final transcript)")
		ins.hLLMTTFT = hist("lk_agents_turn_llm_ttft", "s",
			"LLM time to first token")
		ins.hOnUserTurnCompleted = hist("lk_agents_turn_on_user_turn_completed_delay", "s",
			"Delay from final transcript to LLM first token")
		ins.hTTSTTFB = hist("lk_agents_turn_tts_ttfb", "s",
			"TTS time to first byte (LLM first token → first audio)")
		ins.cLLMOutputTokens, _ = m.Int64Counter("lk_agents_usage_llm_output_tokens_total",
			metric.WithUnit("1"), metric.WithDescription("Total LLM completion tokens"))
		ins.gWorkerLoad, _ = m.Float64Gauge("lk_agents_worker_load",
			metric.WithUnit("{job}"), metric.WithDescription("Active agent jobs count"))

		lkInstr = ins
	})
	return lkInstr
}

// LKMetricsAttrs holds per-session label values for lk_agents_* metrics.
type LKMetricsAttrs struct {
	JobID    string
	Model    string
	Language string
}

func (a LKMetricsAttrs) toOtelAttrs() []attribute.KeyValue {
	jobID, model, lang := a.JobID, a.Model, a.Language
	if jobID == "" {
		jobID = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	if lang == "" {
		lang = "unknown"
	}
	return []attribute.KeyValue{
		attribute.String("agent_id", jobID),
		attribute.String("model", model),
		attribute.String("language", lang),
	}
}

// AdjustLKActiveJobCount increments or decrements lk_agents_active_job_count
// and updates lk_agents_worker_load. delta should be +1 or -1.
func AdjustLKActiveJobCount(ctx context.Context, delta int64) {
	n := lkJobCount.Add(delta)
	if ins := ensureLK(); ins != nil {
		ins.cActiveJobs.Add(ctx, delta)
		ins.gWorkerLoad.Record(ctx, float64(n))
	}
}

func RecordLKProcInitDuration(ctx context.Context, v float64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.hProcInit.Record(ctx, v, metric.WithAttributes(a.toOtelAttrs()...))
	}
}

func RecordLKE2ELatency(ctx context.Context, v float64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.hE2ELatency.Record(ctx, v, metric.WithAttributes(a.toOtelAttrs()...))
	}
}

func RecordLKEndOfTurnDelay(ctx context.Context, v float64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.hEndOfTurnDelay.Record(ctx, v, metric.WithAttributes(a.toOtelAttrs()...))
	}
}

func RecordLKTranscriptionDelay(ctx context.Context, v float64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.hTranscriptionDelay.Record(ctx, v, metric.WithAttributes(a.toOtelAttrs()...))
	}
}

func RecordLKLLMTTFT(ctx context.Context, v float64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.hLLMTTFT.Record(ctx, v, metric.WithAttributes(a.toOtelAttrs()...))
	}
}

func RecordLKOnUserTurnCompleted(ctx context.Context, v float64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.hOnUserTurnCompleted.Record(ctx, v, metric.WithAttributes(a.toOtelAttrs()...))
	}
}

func RecordLKTTSTTFB(ctx context.Context, v float64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.hTTSTTFB.Record(ctx, v, metric.WithAttributes(a.toOtelAttrs()...))
	}
}

func AddLKLLMOutputTokens(ctx context.Context, n int64, a LKMetricsAttrs) {
	if ins := ensureLK(); ins != nil {
		ins.cLLMOutputTokens.Add(ctx, n, metric.WithAttributes(a.toOtelAttrs()...))
	}
}
