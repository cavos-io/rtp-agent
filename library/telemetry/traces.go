package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var Tracer = otel.Tracer("livekit-agents")

const (
	AttrSpeechID   = "lk.speech_id"
	AttrAgentLabel = "lk.agent_label"
	AttrStartTime  = "lk.start_time"
	AttrEndTime    = "lk.end_time"
	AttrRetryCount = "lk.retry_count"

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

	AttrChatCtx                = "lk.chat_ctx"
	AttrFunctionTools          = "lk.function_tools"
	AttrProviderTools          = "lk.provider_tools"
	AttrToolSets               = "lk.tool_sets"
	AttrResponseText           = "lk.response.text"
	AttrResponseFunctionCalls  = "lk.response.function_calls"

	AttrFunctionToolID      = "lk.function_tool.id"
	AttrFunctionToolName    = "lk.function_tool.name"
	AttrFunctionToolArgs    = "lk.function_tool.arguments"
	AttrFunctionToolIsError = "lk.function_tool.is_error"
	AttrFunctionToolOutput  = "lk.function_tool.output"

	AttrTTSInputText = "lk.input_text"
	AttrTTSStreaming = "lk.tts.streaming"
	AttrTTSLabel     = "lk.tts.label"

	AttrEOUProbability      = "lk.eou.probability"
	AttrEOUUnlikelyThreshold = "lk.eou.unlikely_threshold"
	AttrEOUDelay            = "lk.eou.endpointing_delay"
	AttrEOULanguage         = "lk.eou.language"
	AttrUserTranscript      = "lk.user_transcript"
	AttrTranscriptConfidence = "lk.transcript_confidence"
	AttrTranscriptionDelay  = "lk.transcription_delay"
	AttrEndOfTurnDelay      = "lk.end_of_turn_delay"

	AttrGenAIOperationName     = "gen_ai.operation.name"
	AttrGenAIProviderName      = "gen_ai.provider.name"
	AttrGenAIRequestModel      = "gen_ai.request.model"
	AttrGenAIUsageInputTokens  = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutputTokens = "gen_ai.usage.output_tokens"
)

func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer.Start(ctx, name, opts...)
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

func NewSTTStreamSpan(ctx context.Context, model, provider string) (context.Context, trace.Span) {
	return StartSpan(ctx, "stt_stream", trace.WithAttributes(
		attribute.String(AttrGenAIRequestModel, model),
		attribute.String(AttrGenAIProviderName, provider),
	))
}

