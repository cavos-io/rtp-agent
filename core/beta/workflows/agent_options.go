package workflows

import (
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
)

type AgentOptions struct {
	TurnDetection      *agent.TurnDetectionMode
	STT                stt.STT
	VAD                vad.VAD
	LLM                llm.LLM
	RealtimeModel      llm.RealtimeModel
	TTS                tts.TTS
	AllowInterruptions *bool
}

func applyAgentOptions(dst *agent.Agent, opts AgentOptions) {
	if dst == nil {
		return
	}
	if opts.TurnDetection != nil {
		dst.TurnDetection = *opts.TurnDetection
	}
	if opts.STT != nil {
		dst.STT = opts.STT
	}
	if opts.VAD != nil {
		dst.VAD = opts.VAD
	}
	if opts.LLM != nil {
		dst.LLM = opts.LLM
	}
	if opts.RealtimeModel != nil {
		dst.RealtimeModel = opts.RealtimeModel
	}
	if opts.TTS != nil {
		dst.TTS = opts.TTS
	}
	if opts.AllowInterruptions != nil {
		dst.AllowInterruptions = *opts.AllowInterruptions
		dst.AllowInterruptionsSet = true
	}
}
