//go:build agora_sdk

package agora

import (
	"testing"

	agoraservice "github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2/go_sdk/rtc"
)

func TestSDKAudioFrameToModelRejectsNegativeSampleMetadata(t *testing.T) {
	frame := &agoraservice.AudioFrame{
		Type:              agoraservice.AudioFrameTypePCM16,
		SamplesPerChannel: -1,
		BytesPerSample:    2,
		Channels:          1,
		SamplesPerSec:     16000,
		Buffer:            []byte{0x01, 0x02},
	}

	if got := sdkAudioFrameToModel("caller-7", frame); got != nil {
		t.Fatalf("sdkAudioFrameToModel() = %#v, want nil for negative SamplesPerChannel", got)
	}
}

func TestSDKAudioFrameToModelCarriesParticipantID(t *testing.T) {
	frame := &agoraservice.AudioFrame{
		Type:              agoraservice.AudioFrameTypePCM16,
		SamplesPerChannel: 1,
		BytesPerSample:    2,
		Channels:          1,
		SamplesPerSec:     16000,
		Buffer:            []byte{0x01, 0x02},
	}

	got := sdkAudioFrameToModel(" caller-7 ", frame)
	if got == nil {
		t.Fatal("sdkAudioFrameToModel() = nil, want audio frame")
	}
	if got.ParticipantID != "caller-7" {
		t.Fatalf("ParticipantID = %q, want trimmed Agora user id", got.ParticipantID)
	}
}
