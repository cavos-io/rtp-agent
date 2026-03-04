package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/library/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type AvatarState string

const (
	AvatarStateIdle     AvatarState = "idle"
	AvatarStateSpeaking AvatarState = "speaking"
)

type Avatar struct {
	State AvatarState
}

func NewAvatar() *Avatar {
	return &Avatar{
		State: AvatarStateIdle,
	}
}

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

// AvatarRunner coordinates Avatar IO and LipSync events.
type AvatarRunner struct {
	io     AvatarIO
	ctx    context.Context
	cancel context.CancelFunc
}

func NewAvatarRunner(io AvatarIO) *AvatarRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &AvatarRunner{
		io:     io,
		ctx:    ctx,
		cancel: cancel,
	}
}

type blendShapeData struct {
	Type   string             `json:"type"`
	Shapes map[string]float64 `json:"shapes"`
}

func (r *AvatarRunner) Start(ctx context.Context) error {
	return nil
}

// SimulateLipSync takes text (from TranscriptSynchronizer) and simulates basic lip movements
func (r *AvatarRunner) SimulateLipSync(text string) {
	go func() {
		if text == "" {
			return
		}

		words := strings.Fields(strings.ToLower(text))
		wordDuration := 250 * time.Millisecond // Rough approximation per word

		for _, word := range words {
			jawOpen := 0.1 // Default closed/idle

			// Basic viseme mapping: vowels cause jaw to open wider
			if strings.ContainsAny(word, "a") {
				jawOpen = 0.8
			} else if strings.ContainsAny(word, "o") || strings.ContainsAny(word, "e") {
				jawOpen = 0.6
			} else if strings.ContainsAny(word, "i") || strings.ContainsAny(word, "u") {
				jawOpen = 0.4
			}

			data := blendShapeData{
				Type: "blendshapes",
				Shapes: map[string]float64{
					"jawOpen": jawOpen,
				},
			}

			payload, err := json.Marshal(data)
			if err == nil {
				_ = r.io.SendAvatarData(r.ctx, payload)
			}

			time.Sleep(wordDuration)
		}

		// Close jaw at the end of the text chunk
		data := blendShapeData{
			Type: "blendshapes",
			Shapes: map[string]float64{
				"jawOpen": 0.0,
			},
		}
		payload, err := json.Marshal(data)
		if err == nil {
			_ = r.io.SendAvatarData(r.ctx, payload)
		}
	}()
}

func (r *AvatarRunner) Stop() {
	r.cancel()
}

func (a *Avatar) Start(ctx context.Context) error {
	logger.Logger.Infow("Avatar started")
	return nil
}
