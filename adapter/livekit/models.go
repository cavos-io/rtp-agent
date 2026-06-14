package livekit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
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
	languageThresholds  map[string]float64
	languagesPath       string
	languagesLoaded     bool
	runner              TurnDetectorRunner
}

type inferenceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type inferencePayload struct {
	ChatCtx []inferenceMessage `json:"chat_ctx"`
}

type TurnDetectorRunner interface {
	RunTurnDetector(ctx context.Context, payload []byte) (float64, error)
}

type turnDetectorRunnerFunc func(context.Context, []byte) (float64, error)

func (f turnDetectorRunnerFunc) RunTurnDetector(ctx context.Context, payload []byte) (float64, error) {
	return f(ctx, payload)
}

type ModelOption func(*Model)

func NewEnglishModel(opts ...ModelOption) *Model {
	return newModel(ModelEnglish, englishInferenceMethod, opts...)
}

func NewMultilingualModel(opts ...ModelOption) *Model {
	return newModel(ModelMultilingual, multilingualInferenceMethod, opts...)
}

func NewLocalEnglishModel(opts ...ModelOption) (*Model, error) {
	return newLocalModel(ModelEnglish, englishInferenceMethod, opts...)
}

func NewLocalMultilingualModel(opts ...ModelOption) (*Model, error) {
	return newLocalModel(ModelMultilingual, multilingualInferenceMethod, opts...)
}

func WithUnlikelyThreshold(threshold float64) ModelOption {
	return func(model *Model) {
		model.unlikelyThreshold = &threshold
	}
}

func WithLanguageThresholds(thresholds map[string]float64) ModelOption {
	return func(model *Model) {
		model.languageThresholds = copyLanguageThresholds(thresholds)
		model.languagesLoaded = len(thresholds) > 0
	}
}

func WithLanguagesPath(path string) ModelOption {
	return func(model *Model) {
		model.languagesPath = path
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

func WithTurnDetectorRunner(runner TurnDetectorRunner) ModelOption {
	return func(model *Model) {
		model.runner = runner
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
	if language == "" {
		return 0, false
	}
	if m.unlikelyThreshold != nil {
		return *m.unlikelyThreshold, true
	}
	if threshold, ok := m.lookupLanguageThreshold(language); ok {
		return threshold, true
	}
	if threshold, ok := m.fetchRemoteThreshold(language); ok {
		return threshold, true
	}
	return 0, false
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

func newLocalModel(modelType ModelType, inferenceMethod string, opts ...ModelOption) (*Model, error) {
	tokenizerPath, err := ModelTokenizerPath(modelType)
	if err != nil {
		return nil, err
	}
	tokenizer, err := NewHuggingFaceTurnDetectorTokenizer(modelType, tokenizerPath)
	if err != nil {
		return nil, err
	}
	onnxPath, err := ModelONNXPath(modelType)
	if err != nil {
		return nil, err
	}
	inputRunner, err := NewTurnDetectorONNXInputRunner(TurnDetectorONNXOptions{ModelPath: onnxPath})
	if err != nil {
		return nil, err
	}
	localRunner := NewLocalTurnDetectorRunner(tokenizer, inputRunner)
	return newModel(modelType, inferenceMethod, append([]ModelOption{WithTurnDetectorRunner(localRunner)}, opts...)...), nil
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

func copyLanguageThresholds(thresholds map[string]float64) map[string]float64 {
	if len(thresholds) == 0 {
		return nil
	}
	copied := make(map[string]float64, len(thresholds))
	for language, threshold := range thresholds {
		copied[language] = threshold
	}
	return copied
}

func (m *Model) lookupLanguageThreshold(language string) (float64, bool) {
	m.loadLanguageThresholds()
	if len(m.languageThresholds) == 0 {
		return 0, false
	}
	if threshold, ok := m.languageThresholds[language]; ok {
		return threshold, true
	}
	base, _, ok := strings.Cut(language, "-")
	if !ok {
		return 0, false
	}
	threshold, ok := m.languageThresholds[base]
	return threshold, ok
}

func (m *Model) loadLanguageThresholds() {
	if m.languagesLoaded {
		return
	}
	m.languagesLoaded = true
	path := m.languagesPath
	if path == "" {
		var err error
		path, err = ModelLanguagesPath(m.modelType)
		if err != nil {
			return
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	thresholds, err := parseLanguageThresholds(content)
	if err != nil {
		return
	}
	m.languageThresholds = thresholds
}

func parseLanguageThresholds(content []byte) (map[string]float64, error) {
	var languages map[string]struct {
		Threshold float64 `json:"threshold"`
	}
	if err := json.Unmarshal(content, &languages); err != nil {
		return nil, err
	}
	thresholds := make(map[string]float64, len(languages))
	for language, data := range languages {
		thresholds[language] = data.Threshold
	}
	return thresholds, nil
}

func (m *Model) fetchRemoteThreshold(language string) (float64, bool) {
	url := m.RemoteInferenceURL()
	if url == "" {
		return 0, false
	}
	payload, err := json.Marshal(map[string]string{"language": language})
	if err != nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), RemoteInferenceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, false
	}
	var data struct {
		Threshold float64 `json:"threshold"`
	}
	if err := json.Unmarshal(body, &data); err != nil || data.Threshold == 0 {
		return 0, false
	}
	m.cacheLanguageThreshold(language, data.Threshold)
	return data.Threshold, true
}

func (m *Model) cacheLanguageThreshold(language string, threshold float64) {
	if m.languageThresholds == nil {
		m.languageThresholds = make(map[string]float64)
	}
	base, _, ok := strings.Cut(language, "-")
	if ok {
		m.languageThresholds[base] = threshold
		return
	}
	m.languageThresholds[language] = threshold
}
