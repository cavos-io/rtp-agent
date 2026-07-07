package cavos

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/agent"
)

var BackchannelsID = map[string]struct{}{
	"hm":       {},
	"hmm":      {},
	"mhmm":     {},
	"mhm":      {},
	"mm":       {},
	"mm-hmm":   {},
	"mmm":      {},
	"em":       {},
	"e":        {},
	"uh":       {},
	"uh-uh":    {},
	"iya":      {},
	"Nuh-uh":   {},
	"nuh-uh":   {},
	"uh-huh":   {},
	"ya":       {},
	"ya?":      {},
	"yap":      {},
	"yeah":     {},
	"oke":      {},
	"okei":     {},
	"Oke":      {},
	"ok":       {},
	"baik":     {},
	"betul":    {},
	"benar":    {},
	"oh":       {},
	"oh iya":   {},
	"he eh":    {},
	"he-eh":    {},
	"sip":      {},
	"lanjut":   {},
	"ya sudah": {},
}

var clearInterruptPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\btunggu\b`),
	regexp.MustCompile(`(?i)\bsaya\s+mau\s+tanya\b`),
}

type InterruptionGateConfig struct {
	MinSpeechMs                   int
	StrongBargeInMs               int
	BackchannelSuppressionEnabled bool
	BackchannelWords              map[string]struct{}
}

func DefaultInterruptionGateConfig() InterruptionGateConfig {
	return InterruptionGateConfig{
		MinSpeechMs:                   500,
		StrongBargeInMs:               1200,
		BackchannelSuppressionEnabled: true,
		BackchannelWords:              BackchannelsID,
	}
}

type CavosInterruptionGate struct {
	config InterruptionGateConfig
}

type InterruptionGateOption func(*CavosInterruptionGate)

func WithInterruptionGateConfig(config InterruptionGateConfig) InterruptionGateOption {
	return func(g *CavosInterruptionGate) {
		g.config = config
	}
}

func NewInterruptionGate(opts ...InterruptionGateOption) *CavosInterruptionGate {
	g := &CavosInterruptionGate{
		config: DefaultInterruptionGateConfig(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g
}

func (g *CavosInterruptionGate) Decide(agentSpeaking bool, speechMs int, transcript string) agent.InterruptionGateResult {
	backchannel := g.isBackchannelOnly(transcript)

	if agentSpeaking {
		if speechMs < g.config.MinSpeechMs {
			return agent.InterruptionGateResult{
				Decision: agent.InterruptionIgnore,
				Reason:   "speech_too_short",
			}
		}
		if g.config.BackchannelSuppressionEnabled && backchannel {
			return agent.InterruptionGateResult{
				Decision: agent.InterruptionIgnore,
				Reason:   "backchannel_suppressed",
			}
		}
		if hasClearInterruptIntent(transcript) {
			return agent.InterruptionGateResult{
				Decision: agent.InterruptionInterruptAgent,
				Reason:   "clear_interrupt_intent",
			}
		}
		normalized := normalizeTranscript(transcript)
		if normalized != "" && !backchannel && speechMs >= g.config.StrongBargeInMs {
			return agent.InterruptionGateResult{
				Decision: agent.InterruptionInterruptAgent,
				Reason:   "strong_barge_in",
			}
		}
		if normalized != "" && !backchannel && len(strings.Fields(normalized)) >= 4 {
			return agent.InterruptionGateResult{
				Decision: agent.InterruptionInterruptAgent,
				Reason:   "long_non_backchannel",
			}
		}
		return agent.InterruptionGateResult{
			Decision: agent.InterruptionContinueListening,
			Reason:   "needs_more_speech",
		}
	}

	return agent.InterruptionGateResult{
		Decision: agent.InterruptionAcceptUserTurn,
		Reason:   "agent_not_speaking",
	}
}

func (g *CavosInterruptionGate) isBackchannelOnly(transcript string) bool {
	normalized := normalizeTranscript(transcript)
	if normalized == "" {
		return false
	}
	words := g.config.BackchannelWords
	if len(words) == 0 {
		return false
	}
	_, found := words[normalized]
	return found
}

func hasClearInterruptIntent(transcript string) bool {
	normalized := normalizeTranscript(transcript)
	for _, pattern := range clearInterruptPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

func normalizeTranscript(text string) string {
	cleaned := strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	for _, r := range cleaned {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
