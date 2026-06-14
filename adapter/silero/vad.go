package silero

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type VADOptions struct {
	MinSpeechDuration     float64
	MinSilenceDuration    float64
	PrefixPaddingDuration float64
	MaxBufferedSpeech     float64
	ActivationThreshold   float64
	DeactivationThreshold float64
	UpdateInterval        float64
	SampleRate            int
	UseONNXRuntime        bool
	ONNXFilePath          string
	ONNXRuntimeLibPath    string

	minSpeechDurationSet     bool
	minSilenceDurationSet    bool
	prefixPaddingDurationSet bool
	deactivationThresholdSet bool
	activationThresholdSet   bool
	maxBufferedSpeechSet     bool
	useONNXRuntimeSet        bool
}

func DefaultVADOptions() VADOptions {
	activationThreshold := 0.5
	return VADOptions{
		MinSpeechDuration:     0.05,
		MinSilenceDuration:    0.55,
		PrefixPaddingDuration: 0.5,
		MaxBufferedSpeech:     60.0,
		ActivationThreshold:   activationThreshold,
		DeactivationThreshold: max(activationThreshold-0.15, 0.01),
		UpdateInterval:        0.032,
		SampleRate:            16000,
	}
}

type SileroVAD struct {
	options  VADOptions
	inner    *vad.SimpleVAD
	mu       sync.RWMutex
	handlers []vad.VADMetricsHandler
}

type VADOption func(*VADOptions)

func WithMinSpeechDuration(d float64) VADOption {
	return func(o *VADOptions) {
		o.MinSpeechDuration = d
		o.minSpeechDurationSet = true
	}
}

func WithMinSilenceDuration(d float64) VADOption {
	return func(o *VADOptions) {
		o.MinSilenceDuration = d
		o.minSilenceDurationSet = true
	}
}

func WithPrefixPaddingDuration(d float64) VADOption {
	return func(o *VADOptions) {
		o.PrefixPaddingDuration = d
		o.prefixPaddingDurationSet = true
	}
}

func WithPaddingDuration(d float64) VADOption {
	return WithPrefixPaddingDuration(d)
}

func WithMaxBufferedSpeech(d float64) VADOption {
	return func(o *VADOptions) {
		o.MaxBufferedSpeech = d
		o.maxBufferedSpeechSet = true
	}
}

func WithActivationThreshold(t float64) VADOption {
	return func(o *VADOptions) {
		o.ActivationThreshold = t
		o.activationThresholdSet = true
	}
}

func WithDeactivationThreshold(t float64) VADOption {
	return func(o *VADOptions) {
		o.DeactivationThreshold = t
		o.deactivationThresholdSet = true
	}
}

func WithSampleRate(r int) VADOption {
	return func(o *VADOptions) {
		o.SampleRate = r
	}
}

func WithUpdateInterval(d float64) VADOption {
	return func(o *VADOptions) {
		o.UpdateInterval = d
	}
}

func WithONNXRuntime() VADOption {
	return func(o *VADOptions) {
		o.UseONNXRuntime = true
		o.useONNXRuntimeSet = true
	}
}

// WithONNXFilePath configures the Silero ONNX model file used when
// WithONNXRuntime is enabled. Clients that do not want cwd-dependent resource
// lookup can pass ModelPathIn(appRoot) or any absolute model path.
func WithONNXFilePath(path string) VADOption {
	return func(o *VADOptions) {
		o.ONNXFilePath = path
	}
}

func WithONNXRuntimeLibraryPath(path string) VADOption {
	return func(o *VADOptions) {
		o.ONNXRuntimeLibPath = path
	}
}

func NewSileroVAD(opts ...VADOption) *SileroVAD {
	options := buildVADOptions(opts...)
	if !options.deactivationThresholdSet {
		options.DeactivationThreshold = max(options.ActivationThreshold-0.15, 0.01)
	}
	detector, err := newSileroVADWithResolvedOptions(options, false)
	if err != nil {
		return newSileroVADFallback(options)
	}
	return detector
}

func NewSileroVADWithOptions(opts ...VADOption) (*SileroVAD, error) {
	options := buildVADOptions(opts...)
	if !options.deactivationThresholdSet {
		options.DeactivationThreshold = max(options.ActivationThreshold-0.15, 0.01)
	}
	if err := validateVADOptions(options); err != nil {
		return nil, err
	}
	return newSileroVADWithResolvedOptions(options, options.UseONNXRuntime)
}

func buildVADOptions(opts ...VADOption) VADOptions {
	options := DefaultVADOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

type sileroProbabilityEstimatorFactory func(VADOptions) (vad.ProbabilityEstimatorFactory, error)

var newSileroProbabilityEstimatorFactory sileroProbabilityEstimatorFactory = newSileroONNXProbabilityEstimatorFactory

func newSileroVADWithResolvedOptions(options VADOptions, requireONNX bool) (*SileroVAD, error) {
	if options.UseONNXRuntime {
		factory, err := newSileroProbabilityEstimatorFactory(options)
		if err != nil {
			if requireONNX {
				return nil, err
			}
			return newSileroVADFallback(options), nil
		}
		simpleOptions := simpleOptionsFromSileroONNX(options)
		simpleOptions.ProbabilityEstimator = factory
		return newSileroVADFromSimpleOptions(simpleOptions, options, false), nil
	}
	return newSileroVADFallback(options), nil
}

func newSileroVADFallback(options VADOptions) *SileroVAD {
	return newSileroVADFromSimpleOptions(simpleOptionsFromSilero(options), options, true)
}

func newSileroVADFromSimpleOptions(simpleOptions vad.SimpleVADOptions, options VADOptions, scaleThreshold bool) *SileroVAD {
	inner := vad.NewSimpleVADWithOptions(simpleOptions)
	if options.activationThresholdSet {
		threshold := options.ActivationThreshold
		if scaleThreshold {
			threshold /= 10.0
		}
		inner.UpdateOptionsWith(vad.WithThreshold(threshold))
	}
	if options.maxBufferedSpeechSet {
		inner.UpdateOptionsWith(vad.WithMaxBufferedSpeechDuration(options.MaxBufferedSpeech))
	}

	detector := &SileroVAD{
		options: options,
		inner:   inner,
	}
	inner.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
		metrics.Label = detector.Label()
		metrics.Metadata = &telemetry.Metadata{
			ModelName:     detector.Model(),
			ModelProvider: detector.Provider(),
		}
		detector.emitMetrics(metrics)
	})
	return detector
}

func (v *SileroVAD) Label() string {
	return "silero.VAD"
}

func (v *SileroVAD) Model() string {
	return "silero"
}

func (v *SileroVAD) Provider() string {
	return "ONNX"
}

func (v *SileroVAD) Capabilities() vad.VADCapabilities {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return vad.VADCapabilities{UpdateInterval: v.options.UpdateInterval}
}

func (v *SileroVAD) OnMetricsCollected(handler vad.VADMetricsHandler) func() {
	if handler == nil {
		return func() {}
	}
	v.mu.Lock()
	v.handlers = append(v.handlers, handler)
	v.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			v.removeMetricsHandler(handler)
		})
	}
}

func (v *SileroVAD) removeMetricsHandler(handler vad.VADMetricsHandler) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, registered := range v.handlers {
		if reflect.ValueOf(registered).Pointer() == reflect.ValueOf(handler).Pointer() {
			v.handlers = append(v.handlers[:i], v.handlers[i+1:]...)
			return
		}
	}
}

func (v *SileroVAD) UpdateOptions(options VADOptions) {
	v.mu.Lock()
	options.SampleRate = 0
	options.UpdateInterval = 0
	merged := mergeVADOptions(v.options, options)
	if err := validateVADOptions(merged); err != nil {
		v.mu.Unlock()
		return
	}
	v.options = merged
	v.mu.Unlock()
	v.inner.UpdateOptionsWith(simpleUpdateOptionsFromSilero(merged)...)
}

func (v *SileroVAD) UpdateOptionsWith(opts ...VADOption) {
	v.mu.Lock()
	merged := v.options
	sampleRate := merged.SampleRate
	updateInterval := merged.UpdateInterval
	for _, opt := range opts {
		if opt != nil {
			opt(&merged)
		}
	}
	merged.SampleRate = sampleRate
	merged.UpdateInterval = updateInterval
	if err := validateVADOptions(merged); err != nil {
		v.mu.Unlock()
		return
	}
	v.options = merged
	v.mu.Unlock()
	v.inner.UpdateOptionsWith(simpleUpdateOptionsFromSilero(merged)...)
}

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	v.mu.RLock()
	options := v.options
	v.mu.RUnlock()
	if err := validateVADOptions(options); err != nil {
		return nil, err
	}
	return v.inner.Stream(ctx)
}

func simpleOptionsFromSilero(options VADOptions) vad.SimpleVADOptions {
	return vad.SimpleVADOptions{
		Threshold:                 options.ActivationThreshold / 10.0, // Scale threshold for RMS vs probability.
		MinSpeechDuration:         options.MinSpeechDuration,
		MinSilenceDuration:        options.MinSilenceDuration,
		PrefixPaddingDuration:     options.PrefixPaddingDuration,
		MaxBufferedSpeechDuration: options.MaxBufferedSpeech,
		DeactivationThreshold:     options.DeactivationThreshold / 10.0,
		UpdateInterval:            options.UpdateInterval,
		SampleRate:                uint32(options.SampleRate),
		WindowDuration:            options.UpdateInterval,
		ProbabilitySmoothingAlpha: 0.35,
	}
}

func simpleOptionsFromSileroONNX(options VADOptions) vad.SimpleVADOptions {
	simpleOptions := simpleOptionsFromSilero(options)
	simpleOptions.Threshold = options.ActivationThreshold
	simpleOptions.DeactivationThreshold = options.DeactivationThreshold
	return simpleOptions
}

func simpleUpdateOptionsFromSilero(options VADOptions) []vad.SimpleVADOption {
	return []vad.SimpleVADOption{
		vad.WithThreshold(options.ActivationThreshold / 10.0),
		vad.WithMinSpeechDuration(options.MinSpeechDuration),
		vad.WithMinSilenceDuration(options.MinSilenceDuration),
		vad.WithPrefixPaddingDuration(options.PrefixPaddingDuration),
		vad.WithMaxBufferedSpeechDuration(options.MaxBufferedSpeech),
		vad.WithDeactivationThreshold(options.DeactivationThreshold / 10.0),
		vad.WithUpdateInterval(options.UpdateInterval),
		vad.WithSampleRate(uint32(options.SampleRate)),
		vad.WithWindowDuration(options.UpdateInterval),
		vad.WithProbabilitySmoothingAlpha(0.35),
	}
}

func mergeVADOptions(current, updates VADOptions) VADOptions {
	if updates.MinSpeechDuration != 0 || updates.minSpeechDurationSet {
		current.MinSpeechDuration = updates.MinSpeechDuration
		current.minSpeechDurationSet = updates.minSpeechDurationSet
	}
	if updates.MinSilenceDuration != 0 || updates.minSilenceDurationSet {
		current.MinSilenceDuration = updates.MinSilenceDuration
		current.minSilenceDurationSet = updates.minSilenceDurationSet
	}
	if updates.PrefixPaddingDuration != 0 || updates.prefixPaddingDurationSet {
		current.PrefixPaddingDuration = updates.PrefixPaddingDuration
		current.prefixPaddingDurationSet = updates.prefixPaddingDurationSet
	}
	if updates.MaxBufferedSpeech != 0 || updates.maxBufferedSpeechSet {
		current.MaxBufferedSpeech = updates.MaxBufferedSpeech
		current.maxBufferedSpeechSet = updates.maxBufferedSpeechSet
	}
	if updates.ActivationThreshold != 0 || updates.activationThresholdSet {
		current.ActivationThreshold = updates.ActivationThreshold
		current.activationThresholdSet = updates.activationThresholdSet
	}
	if updates.DeactivationThreshold != 0 || updates.deactivationThresholdSet {
		current.DeactivationThreshold = updates.DeactivationThreshold
		current.deactivationThresholdSet = updates.deactivationThresholdSet
	}
	if updates.UpdateInterval != 0 {
		current.UpdateInterval = updates.UpdateInterval
	}
	if updates.SampleRate != 0 {
		current.SampleRate = updates.SampleRate
	}
	if updates.UseONNXRuntime || updates.useONNXRuntimeSet {
		current.UseONNXRuntime = updates.UseONNXRuntime
		current.useONNXRuntimeSet = updates.useONNXRuntimeSet
	}
	if updates.ONNXFilePath != "" {
		current.ONNXFilePath = updates.ONNXFilePath
	}
	if updates.ONNXRuntimeLibPath != "" {
		current.ONNXRuntimeLibPath = updates.ONNXRuntimeLibPath
	}
	return current
}

func validateVADOptions(options VADOptions) error {
	if options.SampleRate != 8000 && options.SampleRate != 16000 {
		return fmt.Errorf("silero VAD only supports 8KHz and 16KHz sample rates")
	}
	if math.IsNaN(options.ActivationThreshold) || math.IsInf(options.ActivationThreshold, 0) || options.ActivationThreshold < 0 {
		return fmt.Errorf("activation_threshold must be greater than or equal to 0")
	}
	if math.IsNaN(options.MinSpeechDuration) || math.IsInf(options.MinSpeechDuration, 0) || options.MinSpeechDuration < 0 {
		return fmt.Errorf("min_speech_duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.MinSilenceDuration) || math.IsInf(options.MinSilenceDuration, 0) || options.MinSilenceDuration < 0 {
		return fmt.Errorf("min_silence_duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.PrefixPaddingDuration) || math.IsInf(options.PrefixPaddingDuration, 0) || options.PrefixPaddingDuration < 0 {
		return fmt.Errorf("prefix_padding_duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.MaxBufferedSpeech) || math.IsInf(options.MaxBufferedSpeech, 0) || options.MaxBufferedSpeech < 0 {
		return fmt.Errorf("max_buffered_speech must be greater than or equal to 0")
	}
	if math.IsNaN(options.DeactivationThreshold) || math.IsInf(options.DeactivationThreshold, 0) || options.DeactivationThreshold <= 0 {
		return fmt.Errorf("deactivation_threshold must be greater than 0")
	}
	return nil
}

func (v *SileroVAD) emitMetrics(metrics *telemetry.VADMetrics) {
	v.mu.RLock()
	handlers := append([]vad.VADMetricsHandler(nil), v.handlers...)
	v.mu.RUnlock()
	for _, handler := range handlers {
		func(handler vad.VADMetricsHandler) {
			defer func() {
				_ = recover()
			}()
			handler(metrics)
		}(handler)
	}
}
