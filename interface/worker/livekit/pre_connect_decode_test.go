package livekit

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDecodePreConnectPCMChunkingAndFormat(t *testing.T) {
	data := make([]byte, 960)
	for i := 0; i < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 1234)
	}
	attrs := map[string]string{"sampleRate": "24000", "channels": "1"}
	frames, err := DecodePreConnectAudio([][]byte{data[:3], data[3:311], data[311:]}, "audio/pcm", attrs)
	if err != nil || len(frames) != 1 || !bytes.Equal(frames[0].Data, data) {
		t.Fatal(frames, err)
	}
	stereo := make([]byte, 3840)
	frames, err = DecodePreConnectAudio([][]byte{stereo}, "audio/pcm", map[string]string{"sampleRate": "48000", "channels": "2"})
	if err != nil || len(frames) != 1 || frames[0].SampleRate != 24000 || frames[0].NumChannels != 1 || len(frames[0].Data) != 960 {
		t.Fatal(frames, err)
	}
	for _, bad := range []map[string]string{nil, {"sampleRate": "0", "channels": "1"}, {"sampleRate": "24000", "channels": "99"}} {
		if _, err := DecodePreConnectAudio([][]byte{data}, "audio/pcm", bad); err == nil {
			t.Fatal("bad format accepted")
		}
	}
	if _, err := DecodePreConnectAudio([][]byte{{1}}, "audio/pcm", attrs); err == nil {
		t.Fatal("partial sample accepted")
	}
	if _, err := DecodePreConnectAudio([][]byte{make([]byte, 4097)}, "audio/opus", attrs); err == nil {
		t.Fatal("oversized packet accepted")
	}
}

func TestDecodePreConnectOpusPreservesPackets(t *testing.T) {
	encoder, err := newOpusEncoder(24000, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	var chunks [][]byte
	for i := 0; i < 3; i++ {
		packet, err := encoder.Encode(make([]byte, 960))
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, append([]byte(nil), packet...))
	}
	frames, err := DecodePreConnectAudio(chunks, "audio/opus", map[string]string{"sampleRate": "24000", "channels": "1"})
	if err != nil || len(frames) != 3 {
		t.Fatal(len(frames), err)
	}
	for _, f := range frames {
		if f.SamplesPerChannel != 480 {
			t.Fatal(f.SamplesPerChannel)
		}
	}
}
