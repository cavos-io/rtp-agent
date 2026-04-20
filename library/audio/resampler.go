package audio

import "math"

// Simple linear resampler for 16-bit PCM mono audio
func Resample(input []byte, fromRate, toRate int) []byte {
	if fromRate == toRate || fromRate <= 0 || toRate <= 0 {
		return input
	}

	// Assuming 16-bit PCM (2 bytes per sample)
	inputSamples := len(input) / 2
	ratio := float64(toRate) / float64(fromRate)
	outputSamples := int(math.Floor(float64(inputSamples) * ratio))
	output := make([]byte, outputSamples*2)

	for i := 0; i < outputSamples; i++ {
		// Calculate source position
		pos := float64(i) / ratio
		index := int(math.Floor(pos))
		frac := pos - float64(index)

		if index >= inputSamples-1 {
			// Last sample or beyond
			val := getSample(input, inputSamples-1)
			setSample(output, i, val)
			continue
		}

		// Linear interpolation
		s1 := float64(getSample(input, index))
		s2 := float64(getSample(input, index+1))
		interpolated := s1 + frac*(s2-s1)
		
		setSample(output, i, int16(interpolated))
	}

	return output
}

func getSample(data []byte, index int) int16 {
	return int16(uint16(data[index*2]) | uint16(data[index*2+1])<<8)
}

func setSample(data []byte, index int, val int16) {
	data[index*2] = byte(uint16(val) & 0xFF)
	data[index*2+1] = byte(uint16(val) >> 8)
}

