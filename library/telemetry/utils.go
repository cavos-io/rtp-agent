package telemetry

import (
	"github.com/cavos-io/rtp-agent/library/logger"
)

func LogMetrics(metrics AgentMetrics) {
	var metadata map[string]interface{}

	switch m := metrics.(type) {
	case *LLMMetrics:
		if m.Metadata != nil {
			metadata = map[string]interface{}{
				"model_name":     m.Metadata.ModelName,
				"model_provider": m.Metadata.ModelProvider,
			}
		}
		logger.Logger.Infow("LLM metrics",
			"ttft", m.TTFT,
			"prompt_tokens", m.PromptTokens,
			"prompt_cached_tokens", m.PromptCachedTokens,
			"completion_tokens", m.CompletionTokens,
			"tokens_per_second", m.TokensPerSecond,
			"metadata", metadata,
		)
	case *RealtimeModelMetrics:
		if m.Metadata != nil {
			metadata = map[string]interface{}{
				"model_name":     m.Metadata.ModelName,
				"model_provider": m.Metadata.ModelProvider,
			}
		}
		
		inputCachedTextTokens := 0
		inputCachedImageTokens := 0
		inputCachedAudioTokens := 0
		
		if m.InputTokenDetails.CachedTokensDetails != nil {
			inputCachedTextTokens = m.InputTokenDetails.CachedTokensDetails.TextTokens
			inputCachedImageTokens = m.InputTokenDetails.CachedTokensDetails.ImageTokens
			inputCachedAudioTokens = m.InputTokenDetails.CachedTokensDetails.AudioTokens
		}

		logger.Logger.Infow("RealtimeModel metrics",
			"ttft", m.TTFT,
			"input_tokens", m.InputTokens,
			"cached_input_tokens", m.InputTokenDetails.CachedTokens,
			"input_text_tokens", m.InputTokenDetails.TextTokens,
			"input_cached_text_tokens", inputCachedTextTokens,
			"input_image_tokens", m.InputTokenDetails.ImageTokens,
			"input_cached_image_tokens", inputCachedImageTokens,
			"input_audio_tokens", m.InputTokenDetails.AudioTokens,
			"input_cached_audio_tokens", inputCachedAudioTokens,
			"output_tokens", m.OutputTokens,
			"output_text_tokens", m.OutputTokenDetails.TextTokens,
			"output_audio_tokens", m.OutputTokenDetails.AudioTokens,
			"output_image_tokens", m.OutputTokenDetails.ImageTokens,
			"total_tokens", m.TotalTokens,
			"tokens_per_second", m.TokensPerSecond,
			"metadata", metadata,
		)
	case *TTSMetrics:
		if m.Metadata != nil {
			metadata = map[string]interface{}{
				"model_name":     m.Metadata.ModelName,
				"model_provider": m.Metadata.ModelProvider,
			}
		}
		logger.Logger.Infow("TTS metrics",
			"ttfb", m.TTFB,
			"audio_duration", m.AudioDuration,
			"metadata", metadata,
		)
	case *EOUMetrics:
		if m.Metadata != nil {
			metadata = map[string]interface{}{
				"model_name":     m.Metadata.ModelName,
				"model_provider": m.Metadata.ModelProvider,
			}
		}
		logger.Logger.Infow("EOU metrics",
			"end_of_utterance_delay", m.EndOfUtteranceDelay,
			"transcription_delay", m.TranscriptionDelay,
			"metadata", metadata,
		)
	case *STTMetrics:
		if m.Metadata != nil {
			metadata = map[string]interface{}{
				"model_name":     m.Metadata.ModelName,
				"model_provider": m.Metadata.ModelProvider,
			}
		}
		logger.Logger.Infow("STT metrics",
			"audio_duration", m.AudioDuration,
			"metadata", metadata,
		)
	}
}

