package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)





// AvatarIO defines how Avatar commands/data are sent.
type AvatarIO interface {
	SendAvatarData(ctx context.Context, data []byte) error
}

type DataStreamIO struct {
	room *lksdk.Room
}

func NewDataStreamIO(room *lksdk.Room) *DataStreamIO {
	return &DataStreamIO{
		room: room,
	}
}

func (io *DataStreamIO) SendAvatarData(ctx context.Context, data []byte) error {
	if io.room == nil || io.room.LocalParticipant == nil {
		return fmt.Errorf("room or local participant is nil")
	}

	topic := "avatar_data"
	// Send via LiveKit Data Channel
	err := io.room.LocalParticipant.PublishData(data, lksdk.WithDataPublishReliable(true), lksdk.WithDataPublishTopic(topic))
	if err != nil {
		return fmt.Errorf("failed to publish avatar data: %w", err)
	}

	return nil
}

type QueueIO struct {
	queue chan []byte
	mu    sync.Mutex
}

func NewQueueIO() *QueueIO {
	return &QueueIO{
		queue: make(chan []byte, 100),
	}
}

func (io *QueueIO) SendAvatarData(ctx context.Context, data []byte) error {
	io.mu.Lock()
	defer io.mu.Unlock()
	
	select {
	case io.queue <- data:
		return nil
	default:
		return fmt.Errorf("queue is full")
	}
}

func (io *QueueIO) ReadQueue() <-chan []byte {
	return io.queue
}

type AvatarOptions struct {
	VideoWidth      int
	VideoHeight     int
	VideoFPS        float64
	AudioSampleRate int
	AudioChannels   int
}

type AudioReceiver interface {
	Start(ctx context.Context) error
	Stream() <-chan *model.AudioFrame
	NotifyPlaybackFinished(playbackPosition time.Duration, interrupted bool) error
	Close() error
}

type VideoGenerator interface {
	PushAudio(frame *model.AudioFrame) error
	Stream() <-chan interface{} // Yields *model.AudioFrame, *model.VideoFrame, or *model.AudioSegmentEnd
	ClearBuffer() error
	Close() error
}

type AVSynchronizer interface {
	Push(frame interface{}) error
	Close() error
}

// AvatarRunner coordinates Avatar IO and LipSync events.
type AvatarRunner struct {
	room        *lksdk.Room
	audioRecv   AudioReceiver
	videoGen    VideoGenerator
	options     AvatarOptions
	
	avSync      AVSynchronizer
	lazyPublish bool

	playbackPosition time.Duration
	audioPlaying     bool

	audioPublication *lksdk.LocalTrackPublication
	videoPublication *lksdk.LocalTrackPublication

	mu sync.Mutex
	
	ctx    context.Context
	cancel context.CancelFunc

	roomConnectedCh chan struct{}
}

func NewAvatarRunner(room *lksdk.Room, audioRecv AudioReceiver, videoGen VideoGenerator, opts AvatarOptions, avSync AVSynchronizer, lazyPublish bool) *AvatarRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &AvatarRunner{
		room:            room,
		audioRecv:       audioRecv,
		videoGen:        videoGen,
		options:         opts,
		avSync:          avSync,
		lazyPublish:     lazyPublish,
		ctx:             ctx,
		cancel:          cancel,
		roomConnectedCh: make(chan struct{}),
	}
}

func (r *AvatarRunner) Start(ctx context.Context) error {
	if err := r.audioRecv.Start(ctx); err != nil {
		return err
	}

	if r.room != nil {
		if r.room.LocalParticipant != nil {
			close(r.roomConnectedCh)
		} else {
			// In a real impl, we'd listen for room events
		}
	}

	if !r.lazyPublish {
		if err := r.publishTracks(ctx); err != nil {
			return err
		}
	}

	go r.readAudioLoop()
	go r.forwardVideoLoop()

	return nil
}

func (r *AvatarRunner) publishTracks(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.audioPublication != nil && r.videoPublication != nil {
		return nil
	}

	if r.room == nil {
		return nil
	}

	select {
	case <-r.roomConnectedCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for room connection")
	}

	// Create and publish audio track
	audioTrack, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: uint32(r.options.AudioSampleRate),
		Channels:  uint16(r.options.AudioChannels),
	})
	if err != nil {
		return fmt.Errorf("failed to create audio track: %w", err)
	}

	r.audioPublication, err = r.room.LocalParticipant.PublishTrack(audioTrack, &lksdk.TrackPublicationOptions{
		Name: "avatar_audio",
	})
	if err != nil {
		return fmt.Errorf("failed to publish audio track: %w", err)
	}

	// Create and publish video track
	videoTrack, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeVP8,
		ClockRate: 90000,
	})
	if err != nil {
		return fmt.Errorf("failed to create video track: %w", err)
	}

	r.videoPublication, err = r.room.LocalParticipant.PublishTrack(videoTrack, &lksdk.TrackPublicationOptions{
		Name: "avatar_video",
	})
	if err != nil {
		return fmt.Errorf("failed to publish video track: %w", err)
	}

	logger.Logger.Infow("Published Avatar AV tracks", "videoWidth", r.options.VideoWidth, "videoHeight", r.options.VideoHeight)
	return nil
}

func (r *AvatarRunner) SendLipSyncEvent(ctx context.Context, data []byte) error {
	if r.room == nil || r.room.LocalParticipant == nil {
		return fmt.Errorf("room or local participant is nil")
	}

	topic := "lk-agent-lipsync"
	err := r.room.LocalParticipant.PublishData(data, lksdk.WithDataPublishReliable(true), lksdk.WithDataPublishTopic(topic))
	return err
}

func (r *AvatarRunner) readAudioLoop() {
	stream := r.audioRecv.Stream()
	for {
		select {
		case <-r.ctx.Done():
			return
		case frame, ok := <-stream:
			if !ok {
				return
			}
			if !r.audioPlaying && frame != nil {
				r.audioPlaying = true
			}
			_ = r.videoGen.PushAudio(frame)
		}
	}
}

func (r *AvatarRunner) forwardVideoLoop() {
	stream := r.videoGen.Stream()
	for {
		select {
		case <-r.ctx.Done():
			return
		case frame, ok := <-stream:
			if !ok {
				return
			}

			switch v := frame.(type) {
			case *model.AudioSegmentEnd:
				if r.audioPlaying {
					_ = r.audioRecv.NotifyPlaybackFinished(r.playbackPosition, false)
					r.audioPlaying = false
					r.playbackPosition = 0
				}
			case *model.AudioFrame:
				if r.lazyPublish {
					_ = r.publishTracks(r.ctx)
				}
				if r.avSync != nil {
					_ = r.avSync.Push(v)
				}
				frameDuration := time.Duration(float64(v.SamplesPerChannel)/float64(v.SampleRate)*1e9) * time.Nanosecond
				r.playbackPosition += frameDuration

				r.mu.Lock()
				pub := r.audioPublication
				r.mu.Unlock()
				if pub != nil {
					if track, ok := pub.Track().(*lksdk.LocalSampleTrack); ok {
						_ = track.WriteSample(media.Sample{
							Data:     v.Data,
							Duration: frameDuration,
						}, nil)
					}
				}
			case *model.VideoFrame:
				if r.lazyPublish {
					_ = r.publishTracks(r.ctx)
				}
				if r.avSync != nil {
					_ = r.avSync.Push(v)
				}

				r.mu.Lock()
				pub := r.videoPublication
				r.mu.Unlock()
				if pub != nil {
					if track, ok := pub.Track().(*lksdk.LocalSampleTrack); ok {
						// Video frames often don't have a fixed duration, using 1/FPS
						dur := time.Second / 30
						if r.options.VideoFPS > 0 {
							dur = time.Duration(float64(time.Second) / r.options.VideoFPS)
						}
						_ = track.WriteSample(media.Sample{
							Data:     v.Data,
							Duration: dur,
						}, nil)
					}
				}
			}
		}
	}
}

func (r *AvatarRunner) Stop() {
	r.cancel()
	_ = r.audioRecv.Close()
	_ = r.videoGen.Close()
	if r.avSync != nil {
		_ = r.avSync.Close()
	}
}

