package slng

import (
	"fmt"
	"strings"
)

const (
	defaultSLNGBaseURL         = "api.slng.ai"
	defaultSLNGSTTModel        = "deepgram/nova:3"
	defaultSLNGTTSModel        = "deepgram/aura:2"
	defaultSLNGSTTSampleRate   = 16000
	defaultSLNGTTSSampleRate   = 24000
	defaultSLNGBufferSeconds   = 0.064
	defaultSLNGSTTEncoding     = "pcm_s16le"
	defaultSLNGTTSEncoding     = "linear16"
	defaultSLNGTTSVoice        = "default"
	defaultSLNGLanguage        = "en"
	defaultSLNGVADThreshold    = 0.5
	defaultSLNGVADMinSilenceMS = 300
	defaultSLNGVADSpeechPadMS  = 30
	defaultSLNGSpeed           = 1.0
	slngAPIKeyEnv              = "SLNG_API_KEY"
	slngNumChannels            = 1
	slngFlushMessage           = `{"type":"flush"}`
	slngCancelMessage          = `{"type":"cancel"}`
)

func defaultSTTEndpoint(baseURL, model string) string {
	return defaultSLNGEndpoint(baseURL, "stt", model)
}

func defaultTTSEndpoint(baseURL, model string) string {
	return defaultSLNGEndpoint(baseURL, "tts", model)
}

func defaultSLNGEndpoint(baseURL, kind, modelName string) string {
	host := strings.Split(baseURL, ":")[0]
	scheme := "wss"
	if host == "localhost" || host == "127.0.0.1" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/v1/%s/%s", scheme, strings.TrimRight(baseURL, "/"), kind, modelName)
}

func normalizeRegionOverride(region any) string {
	var raw []string
	switch v := region.(type) {
	case nil:
		return ""
	case string:
		raw = strings.Split(v, ",")
	case []string:
		raw = v
	default:
		raw = []string{fmt.Sprint(v)}
	}
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		cleaned := strings.ToLower(strings.TrimSpace(value))
		if cleaned != "" {
			values = append(values, cleaned)
		}
	}
	return strings.Join(values, ", ")
}

func slngOptionDefault(options map[string]any, key string, fallback any) any {
	if value, ok := options[key]; ok {
		return value
	}
	return fallback
}

type modelRef struct {
	raw           string
	routeProvider string
	routeModel    string
	variant       string
}

func parseModelRef(modelName string) (modelRef, error) {
	raw := strings.TrimSpace(modelName)
	if raw == "" {
		return modelRef{}, fmt.Errorf("model must not be empty")
	}
	modelPath, variant, _ := strings.Cut(raw, ":")
	if strings.Contains(raw, ":") {
		before, after, _ := strings.Cut(raw, ":")
		modelPath, variant = before, after
		if variant == "" {
			return modelRef{}, fmt.Errorf("model variant must not be empty")
		}
	}
	parts := strings.Split(modelPath, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) < 2 {
		return modelRef{}, fmt.Errorf("invalid model %q", raw)
	}
	if cleaned[0] == "slng" {
		if len(cleaned) < 3 {
			return modelRef{}, fmt.Errorf("invalid model %q", raw)
		}
		return modelRef{raw: raw, routeProvider: cleaned[1], routeModel: strings.Join(cleaned[2:], "/"), variant: variant}, nil
	}
	return modelRef{raw: raw, routeProvider: cleaned[0], routeModel: strings.Join(cleaned[1:], "/"), variant: variant}, nil
}

func normalizeLanguageForModel(modelName, language string, options map[string]any) string {
	cleaned := strings.TrimSpace(language)
	if candidate, ok := options["target_language_code"].(string); ok && strings.TrimSpace(candidate) != "" {
		cleaned = strings.TrimSpace(candidate)
	}
	ref, err := parseModelRef(modelName)
	if err != nil || ref.routeProvider != "sarvam" {
		return cleaned
	}
	if mapped := sarvamLanguageMap[strings.ToLower(cleaned)]; mapped != "" {
		return mapped
	}
	return cleaned
}

var sarvamLanguageMap = map[string]string{
	"bn": "bn-IN", "bn-in": "bn-IN",
	"en": "en-IN", "en-in": "en-IN",
	"gu": "gu-IN", "gu-in": "gu-IN",
	"hi": "hi-IN", "hi-in": "hi-IN",
	"kn": "kn-IN", "kn-in": "kn-IN",
	"ml": "ml-IN", "ml-in": "ml-IN",
	"mr": "mr-IN", "mr-in": "mr-IN",
	"od": "od-IN", "od-in": "od-IN",
	"pa": "pa-IN", "pa-in": "pa-IN",
	"ta": "ta-IN", "ta-in": "ta-IN",
	"te": "te-IN", "te-in": "te-IN",
}

func extractSLNGError(message map[string]any) string {
	for _, key := range []string{"message", "description", "error"} {
		if value := slngString(message[key]); value != "" {
			return value
		}
	}
	return "Unknown error"
}

func slngString(value any) string {
	text, _ := value.(string)
	return text
}

func slngStringDefault(value any, fallback string) string {
	if text := slngString(value); text != "" {
		return text
	}
	return fallback
}

func slngMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func slngSlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func slngFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func slngBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func cloneSLNGMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
