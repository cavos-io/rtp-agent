package tts

import (
	"fmt"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

const finalTailMillis = 10

func splitSynthesizedAudioTail(audio *SynthesizedAudio) (*SynthesizedAudio, *SynthesizedAudio, bool) {
	if audio == nil || audio.Frame == nil {
		return nil, cloneSynthesizedAudio(audio), false
	}
	headFrame, tailFrame, ok := splitAudioFrameTail(audio.Frame)
	tail := cloneSynthesizedAudio(audio)
	tail.Frame = tailFrame
	if !ok {
		return nil, tail, false
	}
	tail.TimedTranscript = nil
	head := cloneSynthesizedAudio(audio)
	head.Frame = headFrame
	head.IsFinal = false
	return head, tail, true
}

func splitAudioFrameTail(frame *model.AudioFrame) (*model.AudioFrame, *model.AudioFrame, bool) {
	if frame == nil || frame.SampleRate == 0 || frame.NumChannels == 0 {
		return nil, cloneAudioFrame(frame), false
	}
	tailSamples := frame.SampleRate * finalTailMillis / 1000
	if tailSamples == 0 || frame.SamplesPerChannel <= tailSamples {
		return nil, cloneAudioFrame(frame), false
	}
	bytesPerSample := uint32(2)
	frameBytes := frame.SamplesPerChannel * frame.NumChannels * bytesPerSample
	if uint32(len(frame.Data)) < frameBytes {
		return nil, cloneAudioFrame(frame), false
	}

	headSamples := frame.SamplesPerChannel - tailSamples
	headBytes := headSamples * frame.NumChannels * bytesPerSample
	tailBytes := tailSamples * frame.NumChannels * bytesPerSample

	head := cloneAudioFrame(frame)
	head.SamplesPerChannel = headSamples
	head.Data = append([]byte(nil), frame.Data[:headBytes]...)

	tail := cloneAudioFrame(frame)
	tail.SamplesPerChannel = tailSamples
	tail.Data = append([]byte(nil), frame.Data[headBytes:headBytes+tailBytes]...)

	return head, tail, true
}

func combineAudioFrames(first, second *model.AudioFrame) (*model.AudioFrame, error) {
	if first == nil {
		return cloneAudioFrame(second), nil
	}
	if second == nil {
		return cloneAudioFrame(first), nil
	}
	if first.SampleRate != second.SampleRate || first.NumChannels != second.NumChannels {
		return nil, fmt.Errorf("cannot combine audio frames with different formats")
	}
	combined := cloneAudioFrame(first)
	combined.SamplesPerChannel = first.SamplesPerChannel + second.SamplesPerChannel
	combined.Data = append(append([]byte(nil), first.Data...), second.Data...)
	return combined, nil
}

func cloneAudioFrame(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil {
		return nil
	}
	clone := *frame
	clone.Data = append([]byte(nil), frame.Data...)
	return &clone
}
