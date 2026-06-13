package main

import (
	"encoding/json"
	"fmt"

	lkaudio "github.com/cavos-io/rtp-agent/core/audio"
	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
)

func runAudioUtils(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "silence_shape"
	}
	frameEvent := func(name string, frame *audiomodel.AudioFrame, original *audiomodel.AudioFrame) map[string]any {
		allZero := true
		for _, b := range frame.Data {
			if b != 0 {
				allZero = false
				break
			}
		}
		return map[string]any{
			"name":                name,
			"sample_rate":         frame.SampleRate,
			"num_channels":        frame.NumChannels,
			"samples_per_channel": frame.SamplesPerChannel,
			"data_bytes":          len(frame.Data),
			"all_zero":            allZero,
			"duration":            lkaudio.CalculateFrameDuration(frame),
			"same_object":         frame == original && original != nil,
		}
	}
	samples := func(frames []*audiomodel.AudioFrame) []uint32 {
		out := make([]uint32, 0, len(frames))
		for _, frame := range frames {
			out = append(out, frame.SamplesPerChannel)
		}
		return out
	}
	switch payload.Action {
	case "silence_shape":
		frame := lkaudio.SilenceFrame(0.02, 16000, 2)
		event := frameEvent("silence_shape", frame, nil)
		event["calculated_duration"] = lkaudio.CalculateAudioDuration([]*audiomodel.AudioFrame{frame})
		return map[string]any{"contract": "audio-utils", "events": []map[string]any{event}}, nil
	case "silence_zero":
		frame := lkaudio.SilenceFrame(0, 16000, 1)
		return map[string]any{"contract": "audio-utils", "events": []map[string]any{frameEvent("silence_zero", frame, nil)}}, nil
	case "silence_like":
		source := &audiomodel.AudioFrame{
			Data:              []byte{1, 2, 3, 4},
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}
		frame := lkaudio.SilenceFrameLike(source)
		return map[string]any{"contract": "audio-utils", "events": []map[string]any{frameEvent("silence_like", frame, source)}}, nil
	case "byte_stream_default":
		stream := lkaudio.NewAudioByteStream(16000, 1, 0)
		frames := stream.Push(make([]byte, 1600*2))
		return map[string]any{
			"contract": "audio-utils",
			"events": []map[string]any{
				{"name": "byte_stream_default", "frame_count": len(frames), "samples": samples(frames)},
			},
		}, nil
	case "byte_stream_write_alias":
		stream := lkaudio.NewAudioByteStream(16000, 1, 320)
		frames := stream.Write(make([]byte, 320*2))
		return map[string]any{
			"contract": "audio-utils",
			"events": []map[string]any{
				{"name": "byte_stream_write_alias", "frame_count": len(frames), "samples": samples(frames)},
			},
		}, nil
	case "byte_stream_progressive":
		stream := lkaudio.NewAudioByteStreamWithOptions(16000, 1, 3200, lkaudio.AudioByteStreamOptions{Progressive: true})
		frames := stream.Push(make([]byte, (320+640+1280)*2))
		return map[string]any{
			"contract": "audio-utils",
			"events": []map[string]any{
				{"name": "byte_stream_progressive", "frame_count": len(frames), "samples": samples(frames)},
			},
		}, nil
	case "byte_stream_flush_incomplete":
		stream := lkaudio.NewAudioByteStream(16000, 2, 1600)
		stream.Push([]byte{1, 2, 3})
		first := stream.Flush()
		stream.Push([]byte{4})
		second := stream.Flush()
		secondData := []byte{}
		if len(second) > 0 {
			secondData = second[0].Data
		}
		secondDataValues := make([]int, 0, len(secondData))
		for _, value := range secondData {
			secondDataValues = append(secondDataValues, int(value))
		}
		return map[string]any{
			"contract": "audio-utils",
			"events": []map[string]any{
				{"name": "byte_stream_flush_incomplete", "first_frame_count": len(first), "second_frame_count": len(second), "second_data": secondDataValues},
			},
		}, nil
	case "byte_stream_clear_progressive":
		stream := lkaudio.NewAudioByteStreamWithOptions(16000, 1, 3200, lkaudio.AudioByteStreamOptions{Progressive: true})
		stream.Push(make([]byte, 320*2))
		stream.Clear()
		frames := stream.Push(make([]byte, 320*2))
		return map[string]any{
			"contract": "audio-utils",
			"events": []map[string]any{
				{"name": "byte_stream_clear_progressive", "frame_count": len(frames), "samples": samples(frames)},
			},
		}, nil
	case "array_buffer_reject_oversized":
		buffer := lkaudio.NewAudioArrayBuffer(2, 16000)
		_, err := buffer.PushFrame(&audiomodel.AudioFrame{
			Data:              make([]byte, 6),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 3,
		})
		message := ""
		if err != nil {
			message = err.Error()
		}
		return map[string]any{
			"contract": "audio-utils",
			"events": []map[string]any{
				{"name": "array_buffer_reject_oversized", "error": err != nil, "error_message": message},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported audio utils action %q", payload.Action)
	}
}
