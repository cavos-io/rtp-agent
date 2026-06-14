package livekit

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/llm"
	"golang.org/x/text/unicode/norm"
)

const (
	HGModel          = "livekit/turn-detector"
	ONNXFilename     = "model_q8.onnx"
	MaxHistoryTokens = 128
	MaxHistoryTurns  = 6

	RemoteInferenceTimeout = 2 * time.Second
)

type ModelType string

const (
	ModelEnglish      ModelType = "en"
	ModelMultilingual ModelType = "multilingual"
)

const (
	englishInferenceMethod      = "lk_end_of_utterance_en"
	multilingualInferenceMethod = "lk_end_of_utterance_multilingual"
)

var modelRevisions = map[ModelType]string{
	ModelEnglish:      "v1.2.2-en",
	ModelMultilingual: "v0.4.1-intl",
}

type Model struct {
	modelType           ModelType
	inferenceMethod     string
	unlikelyThreshold   *float64
	remoteInferenceBase string
	httpClient          *http.Client
}

type inferenceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type inferencePayload struct {
	ChatCtx []inferenceMessage `json:"chat_ctx"`
}

type ModelOption func(*Model)

func NewEnglishModel(opts ...ModelOption) *Model {
	return newModel(ModelEnglish, englishInferenceMethod, opts...)
}

func NewMultilingualModel(opts ...ModelOption) *Model {
	return newModel(ModelMultilingual, multilingualInferenceMethod, opts...)
}

func WithUnlikelyThreshold(threshold float64) ModelOption {
	return func(model *Model) {
		model.unlikelyThreshold = &threshold
	}
}

func WithRemoteInferenceBaseURL(urlBase string) ModelOption {
	return func(model *Model) {
		model.remoteInferenceBase = urlBase
	}
}

func WithHTTPClient(client *http.Client) ModelOption {
	return func(model *Model) {
		model.httpClient = client
	}
}

func (m *Model) Model() ModelType {
	return m.modelType
}

func (m *Model) Provider() string {
	return "livekit"
}

func (m *Model) ModelRevision() string {
	return modelRevisions[m.modelType]
}

func (m *Model) InferenceMethod() string {
	return m.inferenceMethod
}

func (m *Model) UnlikelyThreshold(language string) (float64, bool) {
	if language == "" || m.unlikelyThreshold == nil {
		return 0, false
	}
	return *m.unlikelyThreshold, true
}

func (m *Model) RemoteInferenceURL() string {
	return RemoteInferenceURL(m.remoteInferenceBase)
}

func (m *Model) InferencePayload(chatCtx *llm.ChatContext) ([]byte, error) {
	messages := m.inferenceMessages(chatCtx)
	return json.Marshal(inferencePayload{ChatCtx: messages})
}

func RemoteInferenceURL(urlBase string) string {
	if urlBase == "" {
		return ""
	}
	return urlBase + "/eot/multi"
}

func newModel(modelType ModelType, inferenceMethod string, opts ...ModelOption) *Model {
	model := &Model{
		modelType:       modelType,
		inferenceMethod: inferenceMethod,
	}
	for _, opt := range opts {
		opt(model)
	}
	return model
}

func (m *Model) inferenceMessages(chatCtx *llm.ChatContext) []inferenceMessage {
	if chatCtx == nil {
		return nil
	}
	messages := make([]inferenceMessage, 0, MaxHistoryTurns)
	for _, item := range chatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok || (msg.Role != llm.ChatRoleUser && msg.Role != llm.ChatRoleAssistant) {
			continue
		}
		content := msg.TextContent()
		if strings.TrimSpace(content) == "" {
			continue
		}
		if m.modelType == ModelMultilingual {
			content = normalizeTurnDetectorText(content)
		}
		messages = append(messages, inferenceMessage{
			Role:    string(msg.Role),
			Content: content,
		})
		if len(messages) > MaxHistoryTurns {
			copy(messages, messages[1:])
			messages = messages[:MaxHistoryTurns]
		}
	}
	return messages
}

func normalizeTurnDetectorText(text string) string {
	text = norm.NFKC.String(strings.ToLower(text))
	text = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) && r != '\'' && r != '-' {
			return -1
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
}
