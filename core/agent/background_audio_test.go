package agent

import (
	"testing"

	"github.com/cavos-io/rtp-agent/model"
)

func TestBackgroundAudioPlayer_Normalize(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)
	
	// Test BuiltinAudioClip
	src, vol := player.normalizeSoundSource(CityAmbience)
	if vol != 1.0 {
		t.Errorf("Expected volume 1.0, got %v", vol)
	}
	if src == nil {
		t.Error("Expected source path, got nil")
	}

	// Test AudioConfig
	src, vol = player.normalizeSoundSource(AudioConfig{Source: "test.ogg", Volume: 0.5})
	if vol != 0.5 {
		t.Errorf("Expected volume 0.5, got %v", vol)
	}
	if src != "test.ogg" {
		t.Errorf("Expected source test.ogg, got %v", src)
	}

	// Test channel
	ch := make(chan *model.AudioFrame)
	src, vol = player.normalizeSoundSource(ch)
	if vol != 1.0 {
		t.Errorf("Expected volume 1.0, got %v", vol)
	}
	if src != ch {
		t.Error("Expected channel source, got different")
	}
}

func TestBackgroundAudioPlayer_SelectFromList(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)
	
	sounds := []AudioConfig{
		{Source: "s1", Probability: 1.0},
		{Source: "s2", Probability: 0.0},
	}
	
	selected := player.selectSoundFromList(sounds)
	if selected == nil || selected.Source != "s1" {
		t.Errorf("Expected s1, got %v", selected)
	}
	
	sounds = []AudioConfig{
		{Source: "s1", Probability: 0.0},
	}
	selected = player.selectSoundFromList(sounds)
	if selected != nil {
		t.Errorf("Expected nil, got %v", selected)
	}
}

func TestPlayHandle(t *testing.T) {
	h := newPlayHandle()
	if h.Done() {
		t.Error("Handle should not be done initially")
	}
	
	h.Stop()
	if !h.Done() {
		t.Error("Handle should be done after Stop()")
	}
}
