package avatartalk

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestNewAvatartalkAvatarUsesReferenceDefaults(t *testing.T) {
	t.Setenv(avatarTalkAPIKeyEnv, "env-key")
	t.Setenv(avatarTalkAvatarEnv, "")
	t.Setenv(avatarTalkEmotionEnv, "")

	avatar := NewAvatartalkAvatar("explicit-key")

	if avatar.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", avatar.apiKey)
	}
	if providerName != "avatartalk" {
		t.Fatalf("providerName = %q, want avatartalk", providerName)
	}
	if avatar.avatar != "japanese_man" {
		t.Fatalf("avatar = %q, want default avatar", avatar.avatar)
	}
	if avatar.emotion != "expressive" {
		t.Fatalf("emotion = %q, want expressive", avatar.emotion)
	}
	if avatar.avatarIdentity != "avatartalk-agent" {
		t.Fatalf("avatarIdentity = %q, want reference identity", avatar.avatarIdentity)
	}
	if avatar.avatarName != "avatartalk-agent" {
		t.Fatalf("avatarName = %q, want reference name", avatar.avatarName)
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewAvatartalkAvatarUsesEnvironmentConfig(t *testing.T) {
	t.Setenv(avatarTalkAPIKeyEnv, "env-key")
	t.Setenv(avatarTalkAvatarEnv, "custom-avatar")
	t.Setenv(avatarTalkEmotionEnv, "calm")

	avatar := NewAvatartalkAvatar("")

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}
	if avatar.avatar != "custom-avatar" {
		t.Fatalf("avatar = %q, want env avatar", avatar.avatar)
	}
	if avatar.emotion != "calm" {
		t.Fatalf("emotion = %q, want env emotion", avatar.emotion)
	}
}

func TestAvatartalkAvatarStartRequiresAPIKey(t *testing.T) {
	t.Setenv(avatarTalkAPIKeyEnv, "")
	avatar := NewAvatartalkAvatar("")

	err := avatar.Start(context.Background())

	if err == nil || !strings.Contains(err.Error(), "AVATARTALK_API_KEY") {
		t.Fatalf("Start error = %v, want missing API key error", err)
	}
	if avatar.started {
		t.Fatal("started = true, want false after failed start")
	}
}

func TestAvatartalkAvatarStartAndUpdateState(t *testing.T) {
	avatar := NewAvatartalkAvatar("explicit-key")

	if err := avatar.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !avatar.started {
		t.Fatal("started = false, want true")
	}

	if err := avatar.UpdateState(agent.AvatarStateSpeaking); err != nil {
		t.Fatalf("UpdateState returned error: %v", err)
	}
	if avatar.state != agent.AvatarStateSpeaking {
		t.Fatalf("state = %q, want speaking", avatar.state)
	}
}
