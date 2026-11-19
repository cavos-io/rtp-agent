package livekit

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

// DecodePreConnectAudio decodes a bounded completed startup stream to mono 24kHz
// PCM. Encoded chunks retain packet boundaries; raw PCM may split anywhere.
// It reuses the room input decoders and converter, without opening a Pion room.
func DecodePreConnectAudio(chunks [][]byte, mime string, attrs map[string]string) ([]*model.AudioFrame, error) {
	rate, err := strconv.Atoi(attrs["sampleRate"])
	if err != nil || rate < 8000 || rate > 48000 {
		return nil, fmt.Errorf("invalid pre-connect sampleRate")
	}
	channels, err := strconv.Atoi(attrs["channels"])
	if err != nil || channels < 1 || channels > 2 {
		return nil, fmt.Errorf("invalid pre-connect channels")
	}
	size := 0
	for _, chunk := range chunks {
		size += len(chunk)
	}
	if size > 4*1024*1024 || len(chunks) > 4096 {
		return nil, fmt.Errorf("pre-connect payload limit exceeded")
	}
	var frames []*model.AudioFrame
	if mime == "audio/opus" || strings.Contains(mime, "codecs=opus") {
		for _, chunk := range chunks {
			if len(chunk) == 0 || len(chunk) > 4096 {
				return nil, fmt.Errorf("invalid pre-connect opus packet")
			}
		}
		frames, err = readPreConnectOpusFramesLimited(&preConnectPacketReader{chunks: chunks}, rate, channels, rate*30)
	} else {
		if size%(2*channels) != 0 {
			return nil, fmt.Errorf("incomplete pre-connect PCM sample")
		}
		frames, err = readPreConnectRawPCMFrames(bytes.NewReader(bytes.Join(chunks, nil)), rate, channels)
	}
	if err != nil {
		return nil, err
	}
	var samples uint64
	for _, f := range frames {
		samples += uint64(f.SamplesPerChannel)
	}
	if samples > uint64(rate)*30 {
		return nil, fmt.Errorf("pre-connect audio exceeds 30 seconds")
	}
	converter := newRoomIOInputConverter(24000)
	stream := audio.NewAudioByteStream(24000, 1, 480)
	var out []*model.AudioFrame
	push := func(f *model.AudioFrame) {
		if f != nil {
			out = append(out, stream.Push(f.Data)...)
		}
	}
	for _, f := range frames {
		push(converter.Convert(f))
	}
	push(converter.Flush())
	out = append(out, stream.Flush()...)
	return out, nil
}

type preConnectPacketReader struct{ chunks [][]byte }

func (r *preConnectPacketReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	if len(chunk) > len(p) {
		return 0, io.ErrShortBuffer
	}
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}
