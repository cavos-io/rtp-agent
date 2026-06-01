package silero

import (
	"context"
	"fmt"
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

	deactivationThresholdSet bool
	activationThresholdSet   bool
	maxBufferedSpeechSet     bool
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
	}
}

func WithMinSilenceDuration(d float64) VADOption {
	return func(o *VADOptions) {
		o.MinSilenceDuration = d
	}
}

func WithPrefixPaddingDuration(d float64) VADOption {
	return func(o *VADOptions) {
		o.PrefixPaddingDuration = d
	}
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

func NewSileroVAD(opts ...VADOption) *SileroVAD {
	options := buildVADOptions(opts...)
	if !options.deactivationThresholdSet {
		options.DeactivationThreshold = max(options.ActivationThreshold-0.15, 0.01)
	}
	return newSileroVADWithResolvedOptions(options)
}

func NewSileroVADWithOptions(opts ...VADOption) (*SileroVAD, error) {
	options := buildVADOptions(opts...)
	if !options.deactivationThresholdSet {
		options.DeactivationThreshold = max(options.ActivationThreshold-0.15, 0.01)
	}
	if err := validateVADOptions(options); err != nil {
		return nil, err
	}
	return newSileroVADWithResolvedOptions(options), nil
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

func newSileroVADWithResolvedOptions(options VADOptions) *SileroVAD {
	// Fallback to simple VAD for now to provide out-of-the-box working plugin
	// without requiring CGO/ONNX dependencies in the base install.
	inner := vad.NewSimpleVADWithOptions(simpleOptionsFromSilero(options))
	if options.activationThresholdSet {
		inner.UpdateOptionsWith(vad.WithThreshold(options.ActivationThreshold / 10.0))
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

func (v *SileroVAD) OnMetricsCollected(handler vad.VADMetricsHandler) {
	if handler == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.handlers = append(v.handlers, handler)
}

func (v *SileroVAD) UpdateOptions(options VADOptions) {
	v.mu.Lock()
	options.SampleRate = 0
	v.options = mergeVADOptions(v.options, options)
	merged := v.options
	v.mu.Unlock()
	v.inner.UpdateOptions(simpleOptionsFromSilero(merged))
}

func (v *SileroVAD) UpdateOptionsWith(opts ...VADOption) {
	v.mu.Lock()
	sampleRate := v.options.SampleRate
	for _, opt := range opts {
		if opt != nil {
			opt(&v.options)
		}
	}
	v.options.SampleRate = sampleRate
	merged := v.options
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
	if updates.MinSpeechDuration != 0 {
		current.MinSpeechDuration = updates.MinSpeechDuration
	}
	if updates.MinSilenceDuration != 0 {
		current.MinSilenceDuration = updates.MinSilenceDuration
	}
	if updates.PrefixPaddingDuration != 0 {
		current.PrefixPaddingDuration = updates.PrefixPaddingDuration
	}
	if updates.MaxBufferedSpeech != 0 {
		current.MaxBufferedSpeech = updates.MaxBufferedSpeech
		current.maxBufferedSpeechSet = true
	}
	if updates.ActivationThreshold != 0 {
		current.ActivationThreshold = updates.ActivationThreshold
		current.activationThresholdSet = true
	}
	if updates.DeactivationThreshold != 0 {
		current.DeactivationThreshold = updates.DeactivationThreshold
		current.deactivationThresholdSet = true
	}
	if updates.UpdateInterval != 0 {
		current.UpdateInterval = updates.UpdateInterval
	}
	if updates.SampleRate != 0 {
		current.SampleRate = updates.SampleRate
	}
	return current
}

func validateVADOptions(options VADOptions) error {
	if options.SampleRate != 8000 && options.SampleRate != 16000 {
		return fmt.Errorf("silero VAD only supports 8KHz and 16KHz sample rates")
	}
	if options.DeactivationThreshold <= 0 {
		return fmt.Errorf("deactivation_threshold must be greater than 0")
	}
	return nil
}

func (v *SileroVAD) emitMetrics(metrics *telemetry.VADMetrics) {
	v.mu.RLock()
	handlers := append([]vad.VADMetricsHandler(nil), v.handlers...)
	v.mu.RUnlock()
	for _, handler := range handlers {
		handler(metrics)
	}
}
