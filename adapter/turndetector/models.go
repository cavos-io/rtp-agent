package turndetector

import "time"

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
