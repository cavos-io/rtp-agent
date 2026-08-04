package audio

import (
	"encoding/binary"
	"math"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

const (
	resampleZeroCrossings = 8
	resampleMaxHalfTaps   = 192
	resampleMaxPhases     = 512
)

type lowPassFilter struct {
	inputRate  uint64
	outputRate uint64
	halfTaps   int
	stride     uint64      // distinct phases repeat every gcd(in, out) remainders
	phases     [][]float64 // nil when the phase count exceeded resampleMaxPhases
}

var lowPassFilters sync.Map // [2]uint32 -> *lowPassFilter

// antiAliasFilter returns the shared low-pass for a rate reduction, or nil when
// outputRate is not below inputRate and no filtering is required.
func antiAliasFilter(inputRate, outputRate uint32) *lowPassFilter {
	if inputRate == 0 || outputRate == 0 || outputRate >= inputRate {
		return nil
	}
	key := [2]uint32{inputRate, outputRate}
	if cached, ok := lowPassFilters.Load(key); ok {
		return cached.(*lowPassFilter)
	}
	stored, _ := lowPassFilters.LoadOrStore(key, newLowPassFilter(inputRate, outputRate))
	return stored.(*lowPassFilter)
}

func newLowPassFilter(inputRate, outputRate uint32) *lowPassFilter {
	in := uint64(inputRate)
	out := uint64(outputRate)
	halfTaps := int((resampleZeroCrossings*in + out - 1) / out)
	if halfTaps > resampleMaxHalfTaps {
		halfTaps = resampleMaxHalfTaps
	}
	filter := &lowPassFilter{
		inputRate:  in,
		outputRate: out,
		halfTaps:   halfTaps,
		stride:     gcdUint64(in, out),
	}
	if phases := out / filter.stride; phases <= resampleMaxPhases {
		filter.phases = make([][]float64, phases)
		for phase := uint64(0); phase < phases; phase++ {
			filter.phases[phase] = filter.buildTaps(phase*filter.stride, make([]float64, 0, filter.width()))
		}
	}
	return filter
}

func (f *lowPassFilter) width() int {
	return 2*f.halfTaps + 1
}

func (f *lowPassFilter) taps(remainder uint64, scratch []float64) []float64 {
	if f.phases != nil {
		return f.phases[remainder/f.stride]
	}
	return f.buildTaps(remainder, scratch)
}

func (f *lowPassFilter) buildTaps(remainder uint64, dst []float64) []float64 {
	offset := float64(remainder) / float64(f.outputRate)
	cutoff := float64(f.outputRate) / float64(f.inputRate)
	window := float64(f.halfTaps) + 1
	dst = dst[:0]
	var sum float64
	for tap := 0; tap <= 2*f.halfTaps; tap++ {
		position := float64(tap-f.halfTaps) - offset
		value := cutoff * sinc(cutoff*position) * blackman(position/window)
		dst = append(dst, value)
		sum += value
	}
	// Normalizing to unit DC gain keeps the filter from shifting level as the
	// phase moves between taps.
	if sum != 0 {
		for i := range dst {
			dst[i] /= sum
		}
	}
	return dst
}

func (f *lowPassFilter) convolve(taps []float64, samples []int16, base, frames, center uint64, channels, channel int) int16 {
	first := int64(center) - int64(f.halfTaps)
	lowest := int64(base)
	highest := int64(base+frames) - 1
	var acc float64
	for tap := range taps {
		index := first + int64(tap)
		if index < lowest {
			index = lowest
		} else if index > highest {
			index = highest
		}
		acc += taps[tap] * float64(samples[(uint64(index)-base)*uint64(channels)+uint64(channel)])
	}
	return clampToInt16(acc)
}

func downsampleFrame(frame *model.AudioFrame, filter *lowPassFilter, outputRate uint32, outputSamples uint32) *model.AudioFrame {
	channels := int(frame.NumChannels)
	frames := uint64(frame.SamplesPerChannel)
	samples := int16SamplesFromPCM(frame.Data, int(frames)*channels)
	out := make([]byte, int(outputSamples)*channels*2)
	scratch := make([]float64, 0, filter.width())

	for outputIndex := uint64(0); outputIndex < uint64(outputSamples); outputIndex++ {
		position := outputIndex * filter.inputRate
		center := position / filter.outputRate
		taps := filter.taps(position%filter.outputRate, scratch)
		for channel := 0; channel < channels; channel++ {
			value := filter.convolve(taps, samples, 0, frames, center, channels, channel)
			offset := (int(outputIndex)*channels + channel) * 2
			binary.LittleEndian.PutUint16(out[offset:offset+2], uint16(value))
		}
	}

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        outputRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: outputSamples,
		ParticipantID:     frame.ParticipantID,
	}
}

func int16SamplesFromPCM(data []byte, count int) []int16 {
	samples := make([]int16, count)
	for i := 0; i < count; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
	}
	return samples
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	arg := math.Pi * x
	return math.Sin(arg) / arg
}

func blackman(position float64) float64 {
	if position <= -1 || position >= 1 {
		return 0
	}
	return 0.42 + 0.5*math.Cos(math.Pi*position) + 0.08*math.Cos(2*math.Pi*position)
}

func clampToInt16(value float64) int16 {
	rounded := math.Round(value)
	if rounded > math.MaxInt16 {
		return math.MaxInt16
	}
	if rounded < math.MinInt16 {
		return math.MinInt16
	}
	return int16(rounded)
}

func gcdUint64(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}
