//go:build agora_sdk

package agora

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"

	agoraservice "github.com/AgoraIO-Extensions/Agora-Golang-Server-SDK/v2/go_sdk/rtc"
)

type sdkChannelClient struct {
	mu         sync.Mutex
	joining    bool
	connection *agoraservice.RtcConnection
}

var (
	sdkServiceMu    sync.Mutex
	sdkServiceRefs  int
	sdkServiceAppID string
)

const defaultSDKJoinTimeout = 10 * time.Second

const (
	remoteAudioStateStopped        = 0
	remoteAudioReasonRemoteOffline = 7
)

func NewSDKChannelClient() (ChannelClient, error) {
	return &sdkChannelClient{}, nil
}

func sdkRuntimeDir() string {
	if dir := strings.TrimSpace(os.Getenv("AGORA_SDK_DATA_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "rtp-agent-agora")
}

func sdkJoinTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv("AGORA_JOIN_TIMEOUT"))
	if value == "" {
		return defaultSDKJoinTimeout
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return defaultSDKJoinTimeout
}

func acquireSDKService(cfg *agoraservice.AgoraServiceConfig) error {
	sdkServiceMu.Lock()
	defer sdkServiceMu.Unlock()
	if sdkServiceRefs > 0 {
		if sdkServiceAppID != "" && cfg != nil && cfg.AppId != sdkServiceAppID {
			return fmt.Errorf("agora SDK service already initialized for app ID %q", sdkServiceAppID)
		}
		sdkServiceRefs++
		return nil
	}
	if ret := agoraservice.Initialize(cfg); ret != 0 {
		return fmt.Errorf("agora SDK initialize failed: %d", ret)
	}
	if cfg != nil {
		sdkServiceAppID = cfg.AppId
	}
	sdkServiceRefs = 1
	return nil
}

func releaseSDKService() error {
	sdkServiceMu.Lock()
	defer sdkServiceMu.Unlock()
	if sdkServiceRefs == 0 {
		return nil
	}
	sdkServiceRefs--
	if sdkServiceRefs > 0 {
		return nil
	}
	sdkServiceAppID = ""
	if ret := agoraservice.Release(); ret != 0 {
		return fmt.Errorf("agora SDK release failed: %d", ret)
	}
	return nil
}

func (c *sdkChannelClient) releaseActiveConnection(connection *agoraservice.RtcConnection) bool {
	c.mu.Lock()
	if c.connection != connection {
		c.mu.Unlock()
		return false
	}
	c.connection = nil
	c.joining = false
	c.mu.Unlock()

	_ = connection.Disconnect()
	connection.Release()
	_ = releaseSDKService()
	return true
}

func (c *sdkChannelClient) publishActiveAudio(connection *agoraservice.RtcConnection) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection != connection {
		return 0, false
	}
	return connection.PublishAudio(), true
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
	if frame.Type != agoraservice.AudioFrameTypePCM16 || frame.BytesPerSample != 2 {
		return nil
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel <= 0 {
		samplesPerChannel = len(frame.Buffer) / frame.Channels / 2
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
	ctx = normalizeContext(ctx)
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
	c.mu.Lock()
	if c.connection != nil || c.joining {
		c.mu.Unlock()
		return fmt.Errorf("agora SDK channel is already joined")
	}
	c.joining = true
	c.mu.Unlock()
	joined := false
	defer func() {
		if joined {
			return
		}
		c.mu.Lock()
		c.joining = false
		c.mu.Unlock()
	}()

	cfg := agoraservice.NewAgoraServiceConfig()
	cfg.AppId = opts.AppID
	cfg.EnableVideo = false
	cfg.EnableAudioDevice = false
	cfg.EnableAudioProcessor = true
	cfg.ChannelProfile = agoraservice.ChannelProfileLiveBroadcasting
	cfg.AudioScenario = agoraservice.AudioScenarioChorus
	cfg.UseStringUid = true
	runtimeDir := sdkRuntimeDir()
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("agora SDK runtime directory setup failed: %w", err)
	}
	cfg.LogPath = filepath.Join(runtimeDir, "agorasdk.log")
	cfg.ConfigDir = runtimeDir
	cfg.DataDir = runtimeDir
	if err := acquireSDKService(cfg); err != nil {
		return err
	}

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
		_ = releaseSDKService()
		return fmt.Errorf("agora SDK connection creation failed")
	}
	connectedCh := make(chan Event, 1)
	joinErrCh := make(chan error, 1)
	if ret := connection.RegisterObserver(&agoraservice.RtcConnectionObserver{
		OnConnected: func(_ *agoraservice.RtcConnection, info *agoraservice.RtcConnectionInfo, reason int) {
			event := Event{Kind: EventConnected, Channel: opts.Channel, Reason: reason}
			if info != nil && info.ChannelId != "" {
				event.Channel = info.ChannelId
			}
			select {
			case connectedCh <- event:
			default:
			}
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
			err := fmt.Errorf("agora SDK error %d: %s", errCode, msg)
			emitSDKEvent(handler, Event{Kind: EventError, Channel: opts.Channel, Reason: errCode, Err: err})
			select {
			case joinErrCh <- err:
			default:
			}
		},
		OnConnectionFailure: func(_ *agoraservice.RtcConnection, _ *agoraservice.RtcConnectionInfo, errCode int) {
			err := fmt.Errorf("agora SDK connection failure: %d", errCode)
			emitSDKEvent(handler, Event{Kind: EventError, Channel: opts.Channel, Reason: errCode, Err: err})
			select {
			case joinErrCh <- err:
			default:
			}
		},
		OnAIQoSCapabilityMissing: func(_ *agoraservice.RtcConnection, fallback int) int {
			return fallback
		},
	}); ret != 0 {
		connection.Release()
		_ = releaseSDKService()
		return fmt.Errorf("agora SDK register connection observer failed: %d", ret)
	}
	if audioHandler != nil {
		localUser := connection.GetLocalUser()
		if localUser == nil {
			connection.Release()
			_ = releaseSDKService()
			return fmt.Errorf("agora SDK local user creation failed")
		}
		if ret := connection.RegisterLocalUserObserver(&agoraservice.LocalUserObserver{
			OnUserAudioTrackSubscribed: func(_ *agoraservice.LocalUser, uid string, _ *agoraservice.RemoteAudioTrack) {
				emitSDKEvent(handler, Event{Kind: EventUserJoined, Channel: opts.Channel, UserID: uid})
			},
			OnUserAudioTrackStateChanged: func(_ *agoraservice.LocalUser, uid string, _ *agoraservice.RemoteAudioTrack, state int, reason int, _ int) {
				if state == remoteAudioStateStopped && reason == remoteAudioReasonRemoteOffline {
					emitSDKEvent(handler, Event{Kind: EventUserLeft, Channel: opts.Channel, UserID: uid, Reason: reason})
				}
			},
		}); ret != 0 {
			connection.Release()
			_ = releaseSDKService()
			return fmt.Errorf("agora SDK register local user observer failed: %d", ret)
		}
		if ret := localUser.SetPlaybackAudioFrameBeforeMixingParameters(1, 16000); ret != 0 {
			connection.Release()
			_ = releaseSDKService()
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
			_ = releaseSDKService()
			return fmt.Errorf("agora SDK register audio frame observer failed: %d", ret)
		}
	}

	select {
	case <-ctx.Done():
		connection.Release()
		_ = releaseSDKService()
		return ctx.Err()
	default:
	}
	if ret := connection.Connect(opts.Token, opts.Channel, uid, ""); ret != 0 {
		connection.Release()
		_ = releaseSDKService()
		return fmt.Errorf("agora SDK connect failed: %d", ret)
	}

	c.mu.Lock()
	c.connection = connection
	c.joining = false
	joined = true
	c.mu.Unlock()
	connectedEvent, err := c.waitConnected(ctx, connection, connectedCh, joinErrCh)
	if err != nil {
		return err
	}
	ret, ok := c.publishActiveAudio(connection)
	if !ok {
		return fmt.Errorf("agora SDK channel left before publish audio")
	}
	if ret != 0 {
		c.releaseActiveConnection(connection)
		return fmt.Errorf("agora SDK publish audio failed: %d", ret)
	}
	if err, ok := pendingJoinError(joinErrCh); ok {
		c.releaseActiveConnection(connection)
		return err
	}
	emitSDKEvent(handler, connectedEvent)
	return nil
}

func (c *sdkChannelClient) waitConnected(ctx context.Context, connection *agoraservice.RtcConnection, connectedCh <-chan Event, joinErrCh <-chan error) (Event, error) {
	timer := time.NewTimer(sdkJoinTimeout())
	defer timer.Stop()
	if err, ok := pendingJoinError(joinErrCh); ok {
		c.releaseActiveConnection(connection)
		return Event{}, err
	}
	select {
	case event := <-connectedCh:
		if err, ok := pendingJoinError(joinErrCh); ok {
			c.releaseActiveConnection(connection)
			return Event{}, err
		}
		return event, nil
	case err := <-joinErrCh:
		c.releaseActiveConnection(connection)
		return Event{}, err
	case <-timer.C:
		c.releaseActiveConnection(connection)
		return Event{}, fmt.Errorf("agora SDK connect timed out after %s", sdkJoinTimeout())
	case <-ctx.Done():
		c.releaseActiveConnection(connection)
		return Event{}, ctx.Err()
	}
}

func pendingJoinError(joinErrCh <-chan error) (error, bool) {
	select {
	case err := <-joinErrCh:
		return err, true
	default:
		return nil, false
	}
}

func (c *sdkChannelClient) Leave(ctx context.Context) error {
	ctx = normalizeContext(ctx)
	c.mu.Lock()
	connection := c.connection
	c.connection = nil
	c.mu.Unlock()
	if connection == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return nil
	}
	var err error
	if ret := connection.Disconnect(); ret != 0 {
		err = fmt.Errorf("agora SDK disconnect failed: %d", ret)
	}
	connection.Release()
	if releaseErr := releaseSDKService(); releaseErr != nil && err == nil {
		err = releaseErr
	}
	select {
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	default:
	}
	return err
}

func (c *sdkChannelClient) PublishPCM(ctx context.Context, frame PCMFrame) error {
	ctx = normalizeContext(ctx)
	if err := frame.Validate(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection == nil {
		return fmt.Errorf("agora SDK channel is not joined")
	}
	if ret := c.connection.PushAudioPcmData(frame.Data, frame.SampleRate, frame.Channels, frame.StartPTSMS); ret != 0 {
		return fmt.Errorf("agora SDK push PCM failed: %d", ret)
	}
	return nil
}
