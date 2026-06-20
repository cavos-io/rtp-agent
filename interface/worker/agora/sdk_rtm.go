//go:build agora_sdk

package agora

import (
	"context"
	"fmt"
	"sync"

	agorartm "github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2/go_sdk/rtm"
)

type sdkDataPublisher struct {
	channel   string
	client    *agorartm.IRtmClient
	mu        sync.Mutex
	callbacks sync.WaitGroup
	closed    bool
	handler   DataMessageHandler
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
	publisher := &sdkDataPublisher{
		channel: resolved.Channel,
	}
	cfg.EventHandler = &agorartm.RtmEventHandler{
		OnMessageEvent: func(event *agorartm.MessageEvent) {
			publisher.handleMessageEvent(event)
		},
	}
	client := agorartm.NewRtmClient(cfg)
	if client == nil {
		return nil, fmt.Errorf("agora RTM client creation failed")
	}
	if ret, _ := client.Login(resolved.Token); ret != 0 {
		client.Release()
		return nil, fmt.Errorf("agora RTM login failed: %d", ret)
	}
	if ret, _ := subscribeRTMMessages(client, resolved.Channel); ret != 0 {
		client.Logout()
		client.Release()
		return nil, fmt.Errorf("agora RTM subscribe failed: %d", ret)
	}
	publisher.client = client
	return publisher, nil
}

func subscribeRTMMessages(client *agorartm.IRtmClient, channel string) (int, uint64) {
	opts := agorartm.NewSubscribeOptions()
	opts.WithMessage = true
	return client.Subscribe(channel, opts)
}

func (p *sdkDataPublisher) SetDataMessageHandler(handler DataMessageHandler) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.handler = handler
	p.mu.Unlock()
}

func (p *sdkDataPublisher) handleMessageEvent(event *agorartm.MessageEvent) {
	if p == nil || event == nil {
		return
	}
	if event.ChannelName != p.channel {
		return
	}
	p.mu.Lock()
	handler := p.handler
	closed := p.closed
	if handler != nil && !closed {
		p.callbacks.Add(1)
	}
	p.mu.Unlock()
	if handler == nil || closed {
		return
	}
	defer p.callbacks.Done()
	_ = handler(context.Background(), DataMessage{
		Channel:   event.ChannelName,
		Publisher: event.Publisher,
		Payload:   append([]byte(nil), event.Message...),
	})
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
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
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

type sdkRTMLifecycleClient struct {
	client *agorartm.IRtmClient
}

func (c sdkRTMLifecycleClient) Unsubscribe(channel string) error {
	if c.client == nil {
		return nil
	}
	if ret, _ := c.client.Unsubscribe(channel); ret != 0 {
		return fmt.Errorf("agora RTM unsubscribe failed: %d", ret)
	}
	return nil
}

func (c sdkRTMLifecycleClient) Logout() error {
	if c.client == nil {
		return nil
	}
	if ret, _ := c.client.Logout(); ret != 0 {
		return fmt.Errorf("agora RTM logout failed: %d", ret)
	}
	return nil
}

func (c sdkRTMLifecycleClient) Release() {
	if c.client != nil {
		c.client.Release()
	}
}

func (p *sdkDataPublisher) Close(context.Context) error {
	if p == nil || p.client == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.handler = nil
	client := p.client
	channel := p.channel
	p.mu.Unlock()
	p.callbacks.Wait()
	return closeRTMClient(sdkRTMLifecycleClient{client: client}, channel)
}
