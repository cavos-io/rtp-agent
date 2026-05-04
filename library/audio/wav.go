package audio

import (
	"bytes"
	"encoding/binary"

	"github.com/cavos-io/rtp-agent/model"
)

// FramesToWAV combines all AudioFrames into a single WAV buffer (16-bit PCM)
// with a valid RIFF header. The resulting buffer is ready to be sent to
// STT services (e.g., Azure, Google) as the request body with
// Content-Type: audio/wav.
//
// Audio metadata (sample rate, channels) is taken from the first frame.
// If unavailable, it falls back to 16 kHz mono.
func FramesToWAV(frames []*model.AudioFrame) bytes.Buffer {
	sampleRate := uint32(16000)
	numChannels := uint16(1)
	if len(frames) > 0 {
		if frames[0].SampleRate > 0 {
			sampleRate = frames[0].SampleRate
		}
		if frames[0].NumChannels > 0 {
			numChannels = uint16(frames[0].NumChannels)
		}
	}

	// Combine raw PCM from all frames
	var pcm []byte
	for _, f := range frames {
		pcm = append(pcm, f.Data...)
	}

	const bitsPerSample = uint16(16)
	dataSize := uint32(len(pcm))
	byteRate := sampleRate * uint32(numChannels) * uint32(bitsPerSample) / 8
	blockAlign := numChannels * bitsPerSample / 8

	var buf bytes.Buffer
	buf.Write([]byte("RIFF"))
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize)) // total file size - 8
	buf.Write([]byte("WAVEfmt "))
	binary.Write(&buf, binary.LittleEndian, uint32(16))    // fmt chunk size
	binary.Write(&buf, binary.LittleEndian, uint16(1))     // audio format: PCM
	binary.Write(&buf, binary.LittleEndian, numChannels)   // channels
	binary.Write(&buf, binary.LittleEndian, sampleRate)    // sample rate
	binary.Write(&buf, binary.LittleEndian, byteRate)      // byte rate
	binary.Write(&buf, binary.LittleEndian, blockAlign)    // block align
	binary.Write(&buf, binary.LittleEndian, bitsPerSample) // bits per sample
	buf.Write([]byte("data"))
	binary.Write(&buf, binary.LittleEndian, dataSize)
	buf.Write(pcm)

	return buf
}
