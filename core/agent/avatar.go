package agent

import "context"

type AvatarState string

const (
	AvatarStateIdle     AvatarState = "idle"
	AvatarStateSpeaking AvatarState = "speaking"
)

type AvatarProvider interface {
	Start(ctx context.Context) error
	UpdateState(state AvatarState) error
}
