package agent

import (
	"context"

	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type AvatarState string

const (
	AvatarStateIdle     AvatarState = "idle"
	AvatarStateSpeaking AvatarState = "speaking"
)

type AvatarProvider interface {
	Start(ctx context.Context) error
	UpdateState(state AvatarState) error
}

type AvatarMetricsHandler func(*telemetry.AvatarMetrics)

type AvatarMetricsSource interface {
	OnMetricsCollected(handler AvatarMetricsHandler) func()
}

type AvatarStartInfo struct {
	LiveKitURL   string
	LiveKitToken string
}

type avatarStartInfoContextKey struct{}

func ContextWithAvatarStartInfo(ctx context.Context, info AvatarStartInfo) context.Context {
	return context.WithValue(ctx, avatarStartInfoContextKey{}, info)
}

func AvatarStartInfoFromContext(ctx context.Context) (AvatarStartInfo, bool) {
	info, ok := ctx.Value(avatarStartInfoContextKey{}).(AvatarStartInfo)
	return info, ok
}
