package audio

import "testing"

func TestAudioByteStreamDefaultsToHundredMillisecondFrames(t *testing.T) {
	stream := NewAudioByteStream(16000, 1, 0)
	data := make([]byte, 1600*2)

	frames := stream.Push(data)

	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].SamplesPerChannel != 1600 {
		t.Fatalf("SamplesPerChannel = %d, want 1600", frames[0].SamplesPerChannel)
	}
}

func TestAudioByteStreamProgressiveFrameSizes(t *testing.T) {
	stream := NewAudioByteStreamWithOptions(16000, 1, 3200, AudioByteStreamOptions{
		Progressive: true,
	})
	data := make([]byte, (320+640+1280)*2)

	frames := stream.Push(data)

	want := []uint32{320, 640, 1280}
	if len(frames) != len(want) {
		t.Fatalf("frames = %d, want %d", len(frames), len(want))
	}
	for i, frame := range frames {
		if frame.SamplesPerChannel != want[i] {
			t.Fatalf("frame %d SamplesPerChannel = %d, want %d", i, frame.SamplesPerChannel, want[i])
		}
	}
}

func TestAudioByteStreamFlushDropsIncompleteSample(t *testing.T) {
	stream := NewAudioByteStream(16000, 2, 1600)
	stream.Push([]byte{1, 2, 3})

	if frames := stream.Flush(); len(frames) != 0 {
		t.Fatalf("Flush() frames = %d, want incomplete sample dropped", len(frames))
	}
}

func TestAudioByteStreamClearResetsProgressiveSize(t *testing.T) {
	stream := NewAudioByteStreamWithOptions(16000, 1, 3200, AudioByteStreamOptions{
		Progressive: true,
	})
	stream.Push(make([]byte, 320*2))

	stream.Clear()
	frames := stream.Push(make([]byte, 320*2))

	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].SamplesPerChannel != 320 {
		t.Fatalf("SamplesPerChannel after Clear = %d, want initial progressive size 320", frames[0].SamplesPerChannel)
	}
}

func TestSilenceFrameHelpersMatchReferenceShape(t *testing.T) {
	frame := SilenceFrame(0.02, 16000, 2)
	if frame.SampleRate != 16000 || frame.NumChannels != 2 || frame.SamplesPerChannel != 320 {
		t.Fatalf("SilenceFrame shape = rate %d channels %d samples %d", frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel)
	}
	if len(frame.Data) != 320*2*2 {
		t.Fatalf("SilenceFrame data bytes = %d, want %d", len(frame.Data), 320*2*2)
	}

	like := SilenceFrameLike(frame)
	if like.SampleRate != frame.SampleRate || like.NumChannels != frame.NumChannels || like.SamplesPerChannel != frame.SamplesPerChannel {
		t.Fatalf("SilenceFrameLike shape = %#v, want %#v", like, frame)
	}
	if got := CalculateAudioDuration([]*AudioFrame{frame, like}); got != 0.04 {
		t.Fatalf("CalculateAudioDuration() = %v, want 0.04", got)
	}
}
