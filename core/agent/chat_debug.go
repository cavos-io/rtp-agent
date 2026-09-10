package agent

import (
	protoLogger "github.com/livekit/protocol/logger"
)

// chatdbgCompletion logs what the LLM returned for a generation: the full
// text, timing (ttft/duration), token usage and any tool calls. One line per
// completion, plus one line per generated tool call.
func chatdbgCompletion(log protoLogger.Logger, data *LLMGenerationData) {
	if log == nil || data == nil {
		return
	}
	fields := []interface{}{
		"id", data.ID,
		"request_id", data.RequestID,
		"text", data.GeneratedText,
		"text_len", len([]rune(data.GeneratedText)),
		"ttft", data.TTFT.Seconds(),
		"duration", data.Duration.Seconds(),
		"tool_calls", len(data.GeneratedFunctions),
	}
	if data.Usage != nil {
		fields = append(fields,
			"prompt_tokens", data.Usage.PromptTokens,
			"completion_tokens", data.Usage.CompletionTokens)
	}
	if data.StreamErr != nil {
		fields = append(fields, "stream_err", data.StreamErr.Error())
	}
	log.Infow("chatdbg.completion", fields...)
	for _, fnc := range data.GeneratedFunctions {
		log.Infow("chatdbg.completion_tool", "name", fnc.Name, "args", fnc.Arguments, "call_id", fnc.CallID)
	}
}
