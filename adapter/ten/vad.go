package ten

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

const (
	defaultSampleRate          = 16000
	defaultHopSize             = 256
	defaultUpdateInterval      = float64(defaultHopSize) / float64(defaultSampleRate)
	defaultActivationThreshold = 0.5
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
	HopSize               int
	LibraryPath           string
	ModelPath             string

	minSpeechDurationSet     bool
	minSilenceDurationSet    bool
	prefixPaddingDurationSet bool
	maxBufferedSpeechSet     bool
	activationThresholdSet   bool
	deactivationThresholdSet bool
	updateIntervalSet        bool
	sampleRateSet            bool
	hopSizeSet               bool
}

func DefaultVADOptions() VADOptions {
	return VADOptions{
		MinSpeechDuration:     defaultUpdateInterval,
		MinSilenceDuration:    0.55,
		PrefixPaddingDuration: 0.5,
		MaxBufferedSpeech:     60.0,
		ActivationThreshold:   defaultActivationThreshold,
		DeactivationThreshold: defaultActivationThreshold,
		UpdateInterval:        defaultUpdateInterval,
		SampleRate:            defaultSampleRate,
		HopSize:               defaultHopSize,
	}
}

type VAD struct {
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
		o.sampleRateSet = true
	}
}

func WithHopSize(samples int) VADOption {
	return func(o *VADOptions) {
		o.HopSize = samples
		o.hopSizeSet = true
		if samples > 0 && o.SampleRate > 0 {
			o.UpdateInterval = float64(samples) / float64(o.SampleRate)
			o.updateIntervalSet = true
		}
	}
}

func WithUpdateInterval(d float64) VADOption {
	return func(o *VADOptions) {
		o.UpdateInterval = d
		o.updateIntervalSet = true
	}
}

func WithNativeLibrary(path string) VADOption {
	return func(o *VADOptions) {
		o.LibraryPath = path
	}
}

func WithModelPath(path string) VADOption {
	return func(o *VADOptions) {
		o.ModelPath = path
	}
}

type probabilityEstimatorFactory func(VADOptions) (vad.ProbabilityEstimatorFactory, error)

var newProbabilityEstimatorFactory probabilityEstimatorFactory = newNativeProbabilityEstimatorFactory

func NewVAD(opts ...VADOption) *VAD {
	options := buildVADOptions(opts...)
	detector, err := newVADWithResolvedOptions(options, false)
	if err != nil {
		return newVADFallback(options)
	}
	return detector
}

func NewVADWithOptions(opts ...VADOption) (*VAD, error) {
	options := buildVADOptions(opts...)
	if err := validateVADOptions(options); err != nil {
		return nil, err
	}
	return newVADWithResolvedOptions(options, options.LibraryPath != "" || options.ModelPath != "")
}

func buildVADOptions(opts ...VADOption) VADOptions {
	options := DefaultVADOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.hopSizeSet && !options.updateIntervalSet && options.SampleRate > 0 {
		options.UpdateInterval = float64(options.HopSize) / float64(options.SampleRate)
	}
	if options.activationThresholdSet && !options.deactivationThresholdSet {
		options.DeactivationThreshold = options.ActivationThreshold
	}
	return options
}

func newVADWithResolvedOptions(options VADOptions, requireNative bool) (*VAD, error) {
	if err := validateVADOptions(options); err != nil {
		return nil, err
	}
	if options.LibraryPath != "" || options.ModelPath != "" {
		factory, err := newProbabilityEstimatorFactory(options)
		if err != nil {
			if requireNative {
				return nil, err
			}
			return newVADFallback(options), nil
		}
		simpleOptions := simpleOptionsFromTen(options)
		simpleOptions.ProbabilityEstimator = factory
		return newVADFromSimpleOptions(simpleOptions, options), nil
	}
	return newVADFallback(options), nil
}

func newVADFallback(options VADOptions) *VAD {
	simpleOptions := simpleOptionsFromTen(options)
	simpleOptions.ProbabilityEstimator = func() vad.ProbabilityEstimator {
		return estimateRMSProbability
	}
	return newVADFromSimpleOptions(simpleOptions, options)
}

func newVADFromSimpleOptions(simpleOptions vad.SimpleVADOptions, options VADOptions) *VAD {
	inner := vad.NewSimpleVADWithOptions(simpleOptions)
	if options.activationThresholdSet {
		inner.UpdateOptionsWith(vad.WithThreshold(options.ActivationThreshold))
	}
	if options.deactivationThresholdSet {
		inner.UpdateOptionsWith(vad.WithDeactivationThreshold(options.DeactivationThreshold))
	}
	if options.minSpeechDurationSet {
		inner.UpdateOptionsWith(vad.WithMinSpeechDuration(options.MinSpeechDuration))
	}
	if options.minSilenceDurationSet {
		inner.UpdateOptionsWith(vad.WithMinSilenceDuration(options.MinSilenceDuration))
	}
	if options.prefixPaddingDurationSet {
		inner.UpdateOptionsWith(vad.WithPrefixPaddingDuration(options.PrefixPaddingDuration))
	}
	if options.maxBufferedSpeechSet {
		inner.UpdateOptionsWith(vad.WithMaxBufferedSpeechDuration(options.MaxBufferedSpeech))
	}
	detector := &VAD{
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

func (v *VAD) Label() string {
	return "ten.VAD"
}

func (v *VAD) Model() string {
	return "ten-vad"
}

func (v *VAD) Provider() string {
	return "TEN"
}

func (v *VAD) Capabilities() vad.VADCapabilities {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return vad.VADCapabilities{UpdateInterval: v.options.UpdateInterval}
}

func (v *VAD) OnMetricsCollected(handler vad.VADMetricsHandler) func() {
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

func (v *VAD) removeMetricsHandler(handler vad.VADMetricsHandler) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, registered := range v.handlers {
		if reflect.ValueOf(registered).Pointer() == reflect.ValueOf(handler).Pointer() {
			v.handlers = append(v.handlers[:i], v.handlers[i+1:]...)
			return
		}
	}
}

func (v *VAD) UpdateOptions(options VADOptions) {
	v.mu.Lock()
	options.SampleRate = 0
	options.HopSize = 0
	options.UpdateInterval = 0
	merged := mergeVADOptions(v.options, options)
	if err := validateVADOptions(merged); err != nil {
		v.mu.Unlock()
		return
	}
	v.options = merged
	v.mu.Unlock()
	v.inner.UpdateOptionsWith(simpleUpdateOptionsFromTen(merged)...)
}

func (v *VAD) UpdateOptionsWith(opts ...VADOption) {
	v.mu.Lock()
	merged := v.options
	sampleRate := merged.SampleRate
	hopSize := merged.HopSize
	updateInterval := merged.UpdateInterval
	for _, opt := range opts {
		if opt != nil {
			opt(&merged)
		}
	}
	merged.SampleRate = sampleRate
	merged.HopSize = hopSize
	merged.UpdateInterval = updateInterval
	if err := validateVADOptions(merged); err != nil {
		v.mu.Unlock()
		return
	}
	v.options = merged
	v.mu.Unlock()
	v.inner.UpdateOptionsWith(simpleUpdateOptionsFromTen(merged)...)
}

func (v *VAD) Stream(ctx context.Context) (vad.VADStream, error) {
	v.mu.RLock()
	options := v.options
	v.mu.RUnlock()
	if err := validateVADOptions(options); err != nil {
		return nil, err
	}
	return v.inner.Stream(ctx)
}

func (v *VAD) emitMetrics(metrics *telemetry.VADMetrics) {
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

func simpleOptionsFromTen(options VADOptions) vad.SimpleVADOptions {
	return vad.SimpleVADOptions{
		Threshold:                 options.ActivationThreshold,
		MinSpeechDuration:         options.MinSpeechDuration,
		MinSilenceDuration:        options.MinSilenceDuration,
		PrefixPaddingDuration:     options.PrefixPaddingDuration,
		MaxBufferedSpeechDuration: options.MaxBufferedSpeech,
		DeactivationThreshold:     options.DeactivationThreshold,
		UpdateInterval:            options.UpdateInterval,
		SampleRate:                uint32(options.SampleRate),
		WindowDuration:            options.UpdateInterval,
	}
}

func simpleUpdateOptionsFromTen(options VADOptions) []vad.SimpleVADOption {
	return []vad.SimpleVADOption{
		vad.WithThreshold(options.ActivationThreshold),
		vad.WithMinSpeechDuration(options.MinSpeechDuration),
		vad.WithMinSilenceDuration(options.MinSilenceDuration),
		vad.WithPrefixPaddingDuration(options.PrefixPaddingDuration),
		vad.WithMaxBufferedSpeechDuration(options.MaxBufferedSpeech),
		vad.WithDeactivationThreshold(options.DeactivationThreshold),
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
	if updates.LibraryPath != "" {
		current.LibraryPath = updates.LibraryPath
	}
	if updates.ModelPath != "" {
		current.ModelPath = updates.ModelPath
	}
	return current
}

func validateVADOptions(options VADOptions) error {
	if options.SampleRate != defaultSampleRate {
		return fmt.Errorf("TEN VAD only supports 16KHz sample rate")
	}
	if options.HopSize <= 0 {
		return fmt.Errorf("hop_size must be greater than 0")
	}
	if !finiteBetweenZeroAndOne(options.ActivationThreshold) {
		return fmt.Errorf("activation_threshold must be between 0 and 1")
	}
	if !finiteBetweenZeroAndOne(options.DeactivationThreshold) {
		return fmt.Errorf("deactivation_threshold must be between 0 and 1")
	}
	if invalidDuration(options.MinSpeechDuration) {
		return fmt.Errorf("min_speech_duration must be greater than or equal to 0")
	}
	if invalidDuration(options.MinSilenceDuration) {
		return fmt.Errorf("min_silence_duration must be greater than or equal to 0")
	}
	if invalidDuration(options.PrefixPaddingDuration) {
		return fmt.Errorf("prefix_padding_duration must be greater than or equal to 0")
	}
	if invalidDuration(options.MaxBufferedSpeech) {
		return fmt.Errorf("max_buffered_speech must be greater than or equal to 0")
	}
	if invalidDuration(options.UpdateInterval) || options.UpdateInterval == 0 {
		return fmt.Errorf("update_interval must be greater than 0")
	}
	return nil
}

func finiteBetweenZeroAndOne(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func invalidDuration(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0) || value < 0
}

func estimateRMSProbability(frame *model.AudioFrame) (float64, error) {
	if frame == nil || frame.NumChannels == 0 {
		return 0, nil
	}
	sampleCount := int(frame.SamplesPerChannel)
	if sampleCount <= 0 {
		return 0, nil
	}
	channels := int(frame.NumChannels)
	var sum float64
	var count int
	for i := 0; i < sampleCount; i++ {
		offset := i * channels * 2
		if offset+2 > len(frame.Data) {
			break
		}
		sample := int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2]))
		value := float64(sample) / float64(math.MaxInt16)
		sum += value * value
		count++
	}
	if count == 0 {
		return 0, nil
	}
	probability := math.Sqrt(sum/float64(count)) * 3
	if probability > 1 {
		return 1, nil
	}
	return probability, nil
}

var _ vad.VAD = (*VAD)(nil)
