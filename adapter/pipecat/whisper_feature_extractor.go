package pipecat

import (
	"context"
	"fmt"
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	whisperFeatureSize = 80
	whisperSampleRate  = 16000
	whisperHopLength   = 160
	whisperNFFT        = 400
	whisperFrames      = 800
	whisperMinMelHz    = 0.0
	whisperMaxMelHz    = 8000.0
)

type WhisperFeatureExtractor struct {
	melFilters [][]float64
	window     []float64
	fft        *fourier.FFT
}

func NewWhisperFeatureExtractor() *WhisperFeatureExtractor {
	return &WhisperFeatureExtractor{
		melFilters: buildWhisperMelFilters(),
		window:     periodicHannWindow(whisperNFFT),
		fft:        fourier.NewFFT(whisperNFFT),
	}
}

func (e *WhisperFeatureExtractor) Extract(ctx context.Context, audio []float32) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("pipecat whisper feature extractor is nil")
	}

	waveform := SmartTurnAudioWindow(audio, defaultSmartTurnWindowSec, defaultSmartTurnSampleRate)
	normalized := normalizeWhisperAudio(waveform)
	padded := reflectPadFloat32(normalized, whisperNFFT/2)

	spec := make([]float32, smartTurnONNXFeatureCount)
	frameBuffer := make([]float64, whisperNFFT)
	coefficients := make([]complex128, whisperNFFT/2+1)
	melValues := make([]float64, smartTurnONNXFeatureCount)
	maxLog := math.Inf(-1)

	for frame := 0; frame <= whisperFrames; frame++ {
		start := frame * whisperHopLength
		for i := range frameBuffer {
			frameBuffer[i] = float64(padded[start+i]) * e.window[i]
		}
		coefficients = e.fft.Coefficients(coefficients, frameBuffer)
		if frame == whisperFrames {
			break
		}
		for mel := 0; mel < whisperFeatureSize; mel++ {
			value := 0.0
			for bin := 0; bin <= whisperNFFT/2; bin++ {
				realPart := real(coefficients[bin])
				imagPart := imag(coefficients[bin])
				power := realPart*realPart + imagPart*imagPart
				value += power * e.melFilters[bin][mel]
			}
			if value < 1e-10 {
				value = 1e-10
			}
			logValue := math.Log10(value)
			melValues[mel*whisperFrames+frame] = logValue
			if logValue > maxLog {
				maxLog = logValue
			}
		}
	}

	floor := maxLog - 8.0
	for i, value := range melValues {
		if value < floor {
			value = floor
		}
		spec[i] = float32((value + 4.0) / 4.0)
	}
	return spec, nil
}

func normalizeWhisperAudio(audio []float32) []float32 {
	out := make([]float32, len(audio))
	if len(audio) == 0 {
		return out
	}
	mean := 0.0
	for _, sample := range audio {
		mean += float64(sample)
	}
	mean /= float64(len(audio))
	variance := 0.0
	for _, sample := range audio {
		diff := float64(sample) - mean
		variance += diff * diff
	}
	variance /= float64(len(audio))
	scale := math.Sqrt(variance + 1e-7)
	for i, sample := range audio {
		out[i] = float32((float64(sample) - mean) / scale)
	}
	return out
}

func reflectPadFloat32(input []float32, pad int) []float32 {
	if pad <= 0 {
		return append([]float32(nil), input...)
	}
	if len(input) == 0 {
		return make([]float32, 2*pad)
	}
	out := make([]float32, len(input)+2*pad)
	for i := range out {
		source := i - pad
		for source < 0 || source >= len(input) {
			if source < 0 {
				source = -source
				continue
			}
			source = 2*len(input) - 2 - source
			if source < 0 {
				source = 0
			}
		}
		out[i] = input[source]
	}
	return out
}

func periodicHannWindow(length int) []float64 {
	window := make([]float64, length)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(length))
	}
	return window
}

func buildWhisperMelFilters() [][]float64 {
	numFrequencyBins := whisperNFFT/2 + 1
	filterFreqs := slaneyMelFrequencies(whisperFeatureSize+2, whisperMinMelHz, whisperMaxMelHz)
	filters := make([][]float64, numFrequencyBins)
	for bin := range filters {
		fftFreq := (float64(whisperSampleRate) / 2.0) * float64(bin) / float64(numFrequencyBins-1)
		filters[bin] = make([]float64, whisperFeatureSize)
		for mel := 0; mel < whisperFeatureSize; mel++ {
			left := filterFreqs[mel]
			center := filterFreqs[mel+1]
			right := filterFreqs[mel+2]
			down := (fftFreq - left) / (center - left)
			up := (right - fftFreq) / (right - center)
			weight := math.Min(down, up)
			if weight < 0 {
				weight = 0
			}
			enorm := 2.0 / (right - left)
			filters[bin][mel] = weight * enorm
		}
	}
	return filters
}

func slaneyMelFrequencies(count int, minHz float64, maxHz float64) []float64 {
	minMel := hertzToSlaneyMel(minHz)
	maxMel := hertzToSlaneyMel(maxHz)
	frequencies := make([]float64, count)
	for i := range frequencies {
		mel := minMel
		if count > 1 {
			mel += (maxMel - minMel) * float64(i) / float64(count-1)
		}
		frequencies[i] = slaneyMelToHertz(mel)
	}
	return frequencies
}

func hertzToSlaneyMel(freq float64) float64 {
	const (
		minLogHz  = 1000.0
		minLogMel = 15.0
	)
	logStep := 27.0 / math.Log(6.4)
	mel := 3.0 * freq / 200.0
	if freq >= minLogHz {
		mel = minLogMel + math.Log(freq/minLogHz)*logStep
	}
	return mel
}

func slaneyMelToHertz(mel float64) float64 {
	const (
		minLogHz  = 1000.0
		minLogMel = 15.0
	)
	logStep := math.Log(6.4) / 27.0
	freq := 200.0 * mel / 3.0
	if mel >= minLogMel {
		freq = minLogHz * math.Exp(logStep*(mel-minLogMel))
	}
	return freq
}
