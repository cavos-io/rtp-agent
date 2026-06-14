//go:build agora_sdk

package agora

import (
	"context"
	"fmt"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"

	agoraservice "github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2/go_sdk/rtc"
)

type sdkChannelClient struct {
	mu         sync.Mutex
	connection *agoraservice.RtcConnection
}

var sdkServiceMu sync.Mutex

func NewSDKChannelClient() (ChannelClient, error) {
	return &sdkChannelClient{}, nil
}

func emitSDKEvent(handler EventHandler, event Event) {
	if handler != nil {
		handler(event)
	}
}

func sdkAudioFrameToModel(frame *agoraservice.AudioFrame) *model.AudioFrame {
	if frame == nil || len(frame.Buffer) == 0 || frame.SamplesPerSec <= 0 || frame.Channels <= 0 {
		return nil
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel <= 0 {
		bytesPerSample := frame.BytesPerSample
		if bytesPerSample <= 0 {
			bytesPerSample = 2
		}
		samplesPerChannel = len(frame.Buffer) / frame.Channels / bytesPerSample
	}
	if samplesPerChannel <= 0 {
		return nil
	}
	return &model.AudioFrame{
		Data:              append([]byte(nil), frame.Buffer...),
		SampleRate:        uint32(frame.SamplesPerSec),
		NumChannels:       uint32(frame.Channels),
		SamplesPerChannel: uint32(samplesPerChannel),
	}
}

func (c *sdkChannelClient) Join(ctx context.Context, opts worker.AgoraOptions, handler EventHandler, audioHandler AudioHandler) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	uid := opts.UID
	if uid == "" {
		uid = "0"
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	sdkServiceMu.Lock()
	cfg := agoraservice.NewAgoraServiceConfig()
	cfg.AppId = opts.AppID
	cfg.EnableVideo = false
	cfg.EnableAudioDevice = false
	cfg.EnableAudioProcessor = true
	cfg.ChannelProfile = agoraservice.ChannelProfileLiveBroadcasting
	cfg.AudioScenario = agoraservice.AudioScenarioChorus
	cfg.UseStringUid = true
	if ret := agoraservice.Initialize(cfg); ret != 0 {
		sdkServiceMu.Unlock()
		return fmt.Errorf("agora SDK initialize failed: %d", ret)
	}
	sdkServiceMu.Unlock()

	conCfg := &agoraservice.RtcConnectionConfig{
		AutoSubscribeAudio:            true,
		AutoSubscribeVideo:            false,
		EnableAudioRecordingOrPlayout: false,
		ClientRole:                    agoraservice.ClientRoleBroadcaster,
		ChannelProfile:                agoraservice.ChannelProfileLiveBroadcasting,
	}
	publishCfg := agoraservice.NewRtcConPublishConfig()
	publishCfg.IsPublishAudio = true
	publishCfg.IsPublishVideo = false
	publishCfg.AudioPublishType = agoraservice.AudioPublishTypePcm
	publishCfg.AudioScenario = agoraservice.AudioScenarioChorus
	publishCfg.AudioProfile = agoraservice.AudioProfileDefault

	connection := agoraservice.NewRtcConnection(conCfg, publishCfg)
	if connection == nil {
		return fmt.Errorf("agora SDK connection creation failed")
	}
	connection.RegisterObserver(&agoraservice.RtcConnectionObserver{
		OnConnected: func(_ *agoraservice.RtcConnection, info *agoraservice.RtcConnectionInfo, reason int) {
			event := Event{Kind: EventConnected, Channel: opts.Channel, Reason: reason}
			if info != nil && info.ChannelId != "" {
				event.Channel = info.ChannelId
			}
			emitSDKEvent(handler, event)
		},
		OnDisconnected: func(_ *agoraservice.RtcConnection, info *agoraservice.RtcConnectionInfo, reason int) {
			event := Event{Kind: EventDisconnected, Channel: opts.Channel, Reason: reason}
			if info != nil && info.ChannelId != "" {
				event.Channel = info.ChannelId
			}
			emitSDKEvent(handler, event)
		},
		OnUserJoined: func(_ *agoraservice.RtcConnection, uid string) {
			emitSDKEvent(handler, Event{Kind: EventUserJoined, Channel: opts.Channel, UserID: uid})
		},
		OnUserLeft: func(_ *agoraservice.RtcConnection, uid string, reason int) {
			emitSDKEvent(handler, Event{Kind: EventUserLeft, Channel: opts.Channel, UserID: uid, Reason: reason})
		},
		OnError: func(_ *agoraservice.RtcConnection, errCode int, msg string) {
			emitSDKEvent(handler, Event{Kind: EventError, Channel: opts.Channel, Reason: errCode, Err: fmt.Errorf("agora SDK error %d: %s", errCode, msg)})
		},
		OnConnectionFailure: func(_ *agoraservice.RtcConnection, _ *agoraservice.RtcConnectionInfo, errCode int) {
			emitSDKEvent(handler, Event{Kind: EventError, Channel: opts.Channel, Reason: errCode, Err: fmt.Errorf("agora SDK connection failure: %d", errCode)})
		},
		OnAIQoSCapabilityMissing: func(_ *agoraservice.RtcConnection, fallback int) int {
			return fallback
		},
	})
	if audioHandler != nil {
		localUser := connection.GetLocalUser()
		if localUser == nil {
			connection.Release()
			return fmt.Errorf("agora SDK local user creation failed")
		}
		if ret := localUser.SetPlaybackAudioFrameBeforeMixingParameters(1, 16000); ret != 0 {
			connection.Release()
			return fmt.Errorf("agora SDK set playback audio frame parameters failed: %d", ret)
		}
		if ret := connection.RegisterAudioFrameObserver(&agoraservice.AudioFrameObserver{
			OnPlaybackAudioFrameBeforeMixing: func(_ *agoraservice.LocalUser, channelID string, userID string, frame *agoraservice.AudioFrame, _ agoraservice.VadState, _ *agoraservice.AudioFrame) bool {
				if audioFrame := sdkAudioFrameToModel(frame); audioFrame != nil {
					audioHandler(audioFrame)
				}
				return true
			},
		}, 0, nil); ret != 0 {
			connection.Release()
			return fmt.Errorf("agora SDK register audio frame observer failed: %d", ret)
		}
	}

	if ret := connection.Connect(opts.Token, opts.Channel, uid, ""); ret != 0 {
		connection.Release()
		return fmt.Errorf("agora SDK connect failed: %d", ret)
	}
	if ret := connection.PublishAudio(); ret != 0 {
		connection.Disconnect()
		connection.Release()
		return fmt.Errorf("agora SDK publish audio failed: %d", ret)
	}

	c.mu.Lock()
	c.connection = connection
	c.mu.Unlock()
	return nil
}

func (c *sdkChannelClient) Leave(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	connection := c.connection
	c.connection = nil
	c.mu.Unlock()
	if connection == nil {
		return nil
	}
	if ret := connection.Disconnect(); ret != 0 {
		connection.Release()
		return fmt.Errorf("agora SDK disconnect failed: %d", ret)
	}
	connection.Release()
	return nil
}

func (c *sdkChannelClient) PublishPCM(ctx context.Context, frame PCMFrame) error {
	if err := frame.Validate(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	connection := c.connection
	c.mu.Unlock()
	if connection == nil {
		return fmt.Errorf("agora SDK channel is not joined")
	}
	if ret := connection.PushAudioPcmData(frame.Data, frame.SampleRate, frame.Channels, frame.StartPTSMS); ret != 0 {
		return fmt.Errorf("agora SDK push PCM failed: %d", ret)
	}
	return nil
}
