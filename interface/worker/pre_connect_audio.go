package worker

import (
	"context"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const PreConnectAudioBufferStream = "lk.agent.pre-connect-audio-buffer"

type PreConnectAudioBuffer struct {
	Timestamp time.Time
	Frames    []*model.AudioFrame
}

type PreConnectAudioHandler struct {
	room         *lksdk.Room
	timeout      time.Duration
	maxDelta     time.Duration
	
	buffers      map[string]chan *PreConnectAudioBuffer
	mu           sync.Mutex
	
	registered   bool
	afterConnect bool
}

func NewPreConnectAudioHandler(room *lksdk.Room, timeout time.Duration) *PreConnectAudioHandler {
	return &PreConnectAudioHandler{
		room:     room,
		timeout:  timeout,
		maxDelta: 1 * time.Second,
		buffers:  make(map[string]chan *PreConnectAudioBuffer),
	}
}

func (h *PreConnectAudioHandler) Register() {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if h.registered {
		return
	}
	
	h.afterConnect = h.room.ConnectionState() == lksdk.ConnectionStateConnected
	
	err := h.room.RegisterByteStreamHandler(PreConnectAudioBufferStream, h.handler)
	if err != nil {
		logger.Logger.Warnw("failed to register pre-connect audio handler", err)
	} else {
		h.registered = true
	}
}

func (h *PreConnectAudioHandler) handler(reader *lksdk.ByteStreamReader, participantIdentity string) {
	go h.readAudioTask(reader, participantIdentity)
}

func (h *PreConnectAudioHandler) readAudioTask(reader *lksdk.ByteStreamReader, participantIdentity string) {
	attrs := reader.Info.Attributes
	if attrs == nil {
		logger.Logger.Warnw("pre-connect audio received but no attributes", nil, "participant", participantIdentity)
		return
	}

	trackID := attrs["trackId"]
	if trackID == "" {
		logger.Logger.Warnw("pre-connect audio received but no trackId", nil, "participant", participantIdentity)
		return
	}

	sampleRateStr := attrs["sampleRate"]
	channelsStr := attrs["channels"]
	if sampleRateStr == "" || channelsStr == "" {
		logger.Logger.Warnw("sampleRate or channels not found in pre-connect byte stream", nil)
		return
	}

	sampleRate, _ := strconv.Atoi(sampleRateStr)
	channels, _ := strconv.Atoi(channelsStr)

	h.mu.Lock()
	if ch, ok := h.buffers[trackID]; ok {
		close(ch)
	}
	bufCh := make(chan *PreConnectAudioBuffer, 1)
	h.buffers[trackID] = bufCh
	h.mu.Unlock()

	buf := &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames:    make([]*model.AudioFrame, 0),
	}

	mimeType := reader.Info.MimeType
	isOpus := mimeType == "audio/opus" || strings.Contains(mimeType, "codecs=opus")

	if isOpus {
		opusDec, err := newOpusDecoder(sampleRate, channels)
		if err == nil {
			defer opusDec.Close()
			for {
				chunk := make([]byte, 4096)
				n, err := reader.Read(chunk)
				if n > 0 {
					decoded, decErr := opusDec.Decode(chunk[:n])
					if decErr == nil {
						buf.Frames = append(buf.Frames, &model.AudioFrame{
							Data:              decoded,
							SampleRate:        uint32(sampleRate),
							NumChannels:       uint32(channels),
							SamplesPerChannel: uint32(len(decoded) / (channels * 2)),
						})
					}
				}
				if err == io.EOF {
					break
				} else if err != nil {
					logger.Logger.Warnw("error reading pre-connect opus stream", err)
					break
				}
			}
		} else {
			logger.Logger.Errorw("failed to create opus decoder for pre-connect audio", err)
		}
	} else {
		// Raw PCM
		for {
			chunk := make([]byte, 4096)
			n, err := reader.Read(chunk)
			if n > 0 {
				data := make([]byte, n)
				copy(data, chunk[:n])
				buf.Frames = append(buf.Frames, &model.AudioFrame{
					Data:              data,
					SampleRate:        uint32(sampleRate),
					NumChannels:       uint32(channels),
					SamplesPerChannel: uint32(n / (channels * 2)),
				})
			}
			if err == io.EOF {
				break
			} else if err != nil {
				logger.Logger.Warnw("error reading pre-connect pcm stream", err)
				break
			}
		}
	}

	bufCh <- buf
	close(bufCh)
}

func (h *PreConnectAudioHandler) WaitForData(ctx context.Context, trackID string) []*model.AudioFrame {
	if h.afterConnect {
		logger.Logger.Warnw("pre-connect audio handler registered after room connection", nil, "track_id", trackID)
	}

	h.mu.Lock()
	ch, ok := h.buffers[trackID]
	if !ok {
		ch = make(chan *PreConnectAudioBuffer, 1)
		h.buffers[trackID] = ch
	}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.buffers, trackID)
		h.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(h.timeout):
		return nil
	case buf := <-ch:
		if buf == nil {
			return nil
		}
		if time.Since(buf.Timestamp) > h.maxDelta {
			logger.Logger.Warnw("pre-connect audio buffer is too old", nil, "track_id", trackID)
			return nil
		}
		return buf.Frames
	}
}
