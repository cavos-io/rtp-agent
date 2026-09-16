package telemetry

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var Tracer = otel.Tracer("livekit-agents")

const (
	AttrSpeechID           = "lk.speech_id"
	AttrAgentLabel         = "lk.agent_label"
	AttrStartTime          = "lk.start_time"
	AttrEndTime            = "lk.end_time"
	AttrRetryCount         = "lk.retry_count"
	AttrProviderRequestIDs = "lk.provider_request_ids"
	AttrLLMMetrics         = "lk.llm_metrics"
	AttrTTSMetrics         = "lk.tts_metrics"
	AttrRedactionEnabled   = "lk.redaction.enabled"

	AttrParticipantID       = "lk.participant_id"
	AttrParticipantIdentity = "lk.participant_identity"
	AttrParticipantKind     = "lk.participant_kind"

	AttrJobID          = "lk.job_id"
	AttrAgentName      = "lk.agent_name"
	AttrRoomName       = "lk.room_name"
	AttrSessionOptions = "lk.session_options"

	AttrAgentTurnID       = "lk.generation_id"
	AttrAgentParentTurnID = "lk.parent_generation_id"
	AttrUserInput         = "lk.user_input"
	AttrInstructions      = "lk.instructions"
	AttrSpeechInterrupted = "lk.interrupted"

	AttrChatCtx               = "lk.chat_ctx"
	AttrFunctionTools         = "lk.function_tools"
	AttrProviderTools         = "lk.provider_tools"
	AttrToolSets              = "lk.tool_sets"
	AttrResponseText          = "lk.response.text"
	AttrResponseFunctionCalls = "lk.response.function_calls"
	AttrResponseTTFT          = "lk.response.ttft"
	AttrResponseTTFB          = "lk.response.ttfb"

	AttrFunctionToolID      = "lk.function_tool.id"
	AttrFunctionToolName    = "lk.function_tool.name"
	AttrFunctionToolArgs    = "lk.function_tool.arguments"
	AttrFunctionToolIsError = "lk.function_tool.is_error"
	AttrFunctionToolOutput  = "lk.function_tool.output"

	AttrTTSInputText = "lk.pii.input_text"
	AttrTTSStreaming = "lk.tts.streaming"
	AttrTTSLabel     = "lk.tts.label"

	AttrEOUProbability       = "lk.eou.probability"
	AttrEOUUnlikelyThreshold = "lk.eou.unlikely_threshold"
	AttrEOUDelay             = "lk.eou.endpointing_delay"
	AttrEOULanguage          = "lk.eou.language"
	AttrUserTranscript       = "lk.user_transcript"
	AttrTranscriptConfidence = "lk.transcript_confidence"
	AttrTranscriptionDelay   = "lk.transcription_delay"
	AttrEndOfTurnDelay       = "lk.end_of_turn_delay"
	AttrE2ELatency           = "lk.e2e_latency"

	AttrGenAIOperationName      = "gen_ai.operation.name"
	AttrGenAIProviderName       = "gen_ai.provider.name"
	AttrGenAIRequestModel       = "gen_ai.request.model"
	AttrGenAIRequestStream      = "gen_ai.request.stream"
	AttrGenAIResponseID         = "gen_ai.response.id"
	AttrGenAIResponseModel      = "gen_ai.response.model"
	AttrGenAIResponseReasons    = "gen_ai.response.finish_reasons"
	AttrGenAIResponseTTFC       = "gen_ai.response.time_to_first_chunk"
	AttrGenAIUsageInputTokens   = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutputTokens  = "gen_ai.usage.output_tokens"
	AttrGenAIUsageCacheRead     = "gen_ai.usage.cache_read.input_tokens"
	AttrGenAIUsageCacheWrite    = "gen_ai.usage.cache_write.input_tokens"
	AttrGenAIInputCachedTokens  = "gen_ai.usage.input_cached_tokens"
	AttrGenAISystemInstructions = "gen_ai.system_instructions"
	AttrGenAIInputMessages      = "gen_ai.input.messages"
	AttrGenAIOutputMessages     = "gen_ai.output.messages"
	AttrGenAIToolDefinitions    = "gen_ai.tool.definitions"
	AttrGenAIOutputType         = "gen_ai.output.type"
	AttrErrorType               = "error.type"

	EventGenAISystemMessage    = "gen_ai.system.message"
	EventGenAIUserMessage      = "gen_ai.user.message"
	EventGenAIAssistantMessage = "gen_ai.assistant.message"
	EventGenAIToolMessage      = "gen_ai.tool.message"
	EventGenAIChoice           = "gen_ai.choice"
)

func RedactionEnabled(ctx context.Context) bool {
	observability := JobObservabilityFromContext(ctx)

	return observability != nil && observability.redactionEnabled
}

func CaptureGenAIContent(ctx context.Context) bool {
	if RedactionEnabled(ctx) {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

type ChatTraceEvent struct {
	Name       string
	Attributes []attribute.KeyValue
}

func AddChatTraceEvents(span trace.Span, events []ChatTraceEvent) {
	if span == nil {
		return
	}
	for _, event := range events {
		if event.Name == "" {
			continue
		}
		span.AddEvent(event.Name, trace.WithAttributes(event.Attributes...))
	}
}

func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if observability := JobObservabilityFromContext(ctx); observability != nil && name == "agent_session" {
		opts = append(opts, trace.WithAttributes(observability.sessionAttrs...))
	}
	return TracerFromContext(ctx).Start(ctx, name, opts...)
}

func TracerFromContext(ctx context.Context) trace.Tracer {
	if observability := JobObservabilityFromContext(ctx); observability != nil {
		return observability.Tracer()
	}
	return Tracer
}

type SpanContext struct {
	SpeechID string
	Span     trace.Span
}

func NewLLMSpan(ctx context.Context, model, provider string) (context.Context, trace.Span) {
	return StartSpan(ctx, "llm_inference", trace.WithAttributes(
		attribute.String(AttrGenAIRequestModel, model),
		attribute.String(AttrGenAIProviderName, provider),
	))
}

func NewTTSStreamSpan(ctx context.Context, model, provider string) (context.Context, trace.Span) {
	return StartSpan(ctx, "tts_stream", trace.WithAttributes(
		attribute.String(AttrGenAIRequestModel, model),
		attribute.String(AttrGenAIProviderName, provider),
	))
}

func NewTTSNodeSpan(ctx context.Context, model, provider string) (context.Context, trace.Span) {
	return StartSpan(ctx, "tts_node", trace.WithAttributes(
		attribute.String(AttrGenAIRequestModel, model),
		attribute.String(AttrGenAIProviderName, provider),
	))
}

func NewSTTStreamSpan(ctx context.Context, model, provider string) (context.Context, trace.Span) {
	return StartSpan(ctx, "stt_stream", trace.WithAttributes(
		attribute.String(AttrGenAIRequestModel, model),
		attribute.String(AttrGenAIProviderName, provider),
	))
}
