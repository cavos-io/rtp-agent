//go:build agora_sdk

package agora

import (
	"context"
	"fmt"
	"sync"

	agorartm "github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2/go_sdk/rtm"
)

type sdkDataPublisher struct {
	channel string
	client  *agorartm.IRtmClient
	mu      sync.Mutex
	closed  bool
}

func NewSDKDataPublisher(opts Options) (DataPublisher, error) {
	resolved, err := ResolveDataOptions(opts)
	if err != nil {
		return nil, err
	}
	if resolved.UID == "" {
		return nil, fmt.Errorf("agora UID is required for data publishing")
	}
	cfg := agorartm.NewRtmConfig()
	cfg.AppId = resolved.AppID
	cfg.UserId = resolved.UID
	cfg.UseStringUserId = true
	client := agorartm.NewRtmClient(cfg)
	if client == nil {
		return nil, fmt.Errorf("agora RTM client creation failed")
	}
	if ret, _ := client.Login(resolved.Token); ret != 0 {
		client.Release()
		return nil, fmt.Errorf("agora RTM login failed: %d", ret)
	}
	return &sdkDataPublisher{
		channel: resolved.Channel,
		client:  client,
	}, nil
}

func (p *sdkDataPublisher) PublishData(ctx context.Context, payload []byte) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("agora data publisher is not configured")
	}
	ctx = normalizeContext(ctx)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("agora data publisher is closed")
	}
	opts := agorartm.NewPublishOptions()
	opts.ChannelType = agorartm.RtmChannelTypeMESSAGE
	opts.MessageType = agorartm.RtmMessageTypeSTRING
	if ret, _ := p.client.Publish(p.channel, payload, opts); ret != 0 {
		return fmt.Errorf("agora RTM publish failed: %d", ret)
	}
	return nil
}

func (p *sdkDataPublisher) Close(context.Context) error {
	if p == nil || p.client == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if ret, _ := p.client.Logout(); ret != 0 {
		p.client.Release()
		return fmt.Errorf("agora RTM logout failed: %d", ret)
	}
	p.client.Release()
	return nil
}
