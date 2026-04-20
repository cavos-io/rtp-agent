package agent

import (
	"context"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type BuiltinAudioClip string

const (
	CityAmbience    BuiltinAudioClip = "city-ambience.ogg"
	ForestAmbience  BuiltinAudioClip = "forest-ambience.ogg"
	OfficeAmbience  BuiltinAudioClip = "office-ambience.ogg"
	CrowdedRoom     BuiltinAudioClip = "crowded-room.ogg"
	KeyboardTyping  BuiltinAudioClip = "keyboard-typing.ogg"
	KeyboardTyping2 BuiltinAudioClip = "keyboard-typing2.ogg"
	HoldMusic       BuiltinAudioClip = "hold_music.ogg"
)

func (b BuiltinAudioClip) Path() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, "resources", string(b))
}

type AudioSource interface{} // Can be string, BuiltinAudioClip, or <-chan *model.AudioFrame

type AudioConfig struct {
	Source      AudioSource
	Volume      float64
	Probability float64
}

type BackgroundAudioPlayer struct {
	ambientSound  interface{} // AudioSource | AudioConfig | []AudioConfig
	thinkingSound interface{} // AudioSource | AudioConfig | []AudioConfig

	room         *lksdk.Room
	agentSession *AgentSession
	publication  *lksdk.LocalTrackPublication

	mu              sync.Mutex
	mixerTaskCtx    context.Context
	mixerTaskCancel context.CancelFunc
	playTasks       sync.WaitGroup
	activeStreams   map[*PlayHandle]<-chan *model.AudioFrame

	ambientHandle  *PlayHandle
	thinkingHandle *PlayHandle

	targetVolume  float64
	currentVolume float64
}

func NewBackgroundAudioPlayer(ambientSound, thinkingSound interface{}) *BackgroundAudioPlayer {
	return &BackgroundAudioPlayer{
		ambientSound:  ambientSound,
		thinkingSound: thinkingSound,
		activeStreams: make(map[*PlayHandle]<-chan *model.AudioFrame),
		targetVolume:  1.0,
		currentVolume: 1.0,
	}
}

func (p *BackgroundAudioPlayer) selectSoundFromList(sounds []AudioConfig) *AudioConfig {
	var totalProbability float64
	for _, s := range sounds {
		totalProbability += s.Probability
	}
	if totalProbability <= 0 {
		return nil
	}

	if totalProbability < 1.0 && rand.Float64() > totalProbability {
		return nil
	}

	normalizeFactor := 1.0
	if totalProbability > 1.0 {
		normalizeFactor = totalProbability
	}

	r := rand.Float64() * math.Min(totalProbability, 1.0)
	var cumulative float64

	for _, s := range sounds {
		if s.Probability <= 0 {
			continue
		}
		normProb := s.Probability / normalizeFactor
		cumulative += normProb

		if r <= cumulative {
			return &s
		}
	}
	return &sounds[len(sounds)-1]
}

func (p *BackgroundAudioPlayer) normalizeSoundSource(source interface{}) (AudioSource, float64) {
	if source == nil {
		return nil, 0
	}

	switch s := source.(type) {
	case BuiltinAudioClip:
		return s.Path(), 1.0
	case []AudioConfig:
		selected := p.selectSoundFromList(s)
		if selected == nil {
			return nil, 0
		}
		return p.normalizeSoundSource(selected.Source)
	case AudioConfig:
		src, _ := p.normalizeSoundSource(s.Source)
		return src, s.Volume
	case string, <-chan *model.AudioFrame:
		return s, 1.0
	}
	return nil, 0
}

type PlayHandle struct {
	doneCh chan struct{}
	stopCh chan struct{}
	once   sync.Once
}

func newPlayHandle() *PlayHandle {
	return &PlayHandle{
		doneCh: make(chan struct{}),
		stopCh: make(chan struct{}),
	}
}

func (h *PlayHandle) Done() bool {
	select {
	case <-h.doneCh:
		return true
	default:
		return false
	}
}

func (h *PlayHandle) Stop() {
	if h.Done() {
		return
	}
	h.once.Do(func() {
		close(h.stopCh)
		close(h.doneCh)
	})
}

func (h *PlayHandle) WaitForPlayout() {
	<-h.doneCh
}

func (h *PlayHandle) markPlayoutDone() {
	h.once.Do(func() {
		close(h.doneCh)
	})
}

func (p *BackgroundAudioPlayer) Play(audio interface{}, loop bool) *PlayHandle {
	if p.mixerTaskCancel == nil {
		logger.Logger.Warnw("BackgroundAudio is not started", nil)
		handle := newPlayHandle()
		handle.markPlayoutDone()
		return handle
	}

	soundSource, volume := p.normalizeSoundSource(audio)
	if soundSource == nil {
		handle := newPlayHandle()
		handle.markPlayoutDone()
		return handle
	}

	if loop {
		if _, ok := soundSource.(<-chan *model.AudioFrame); ok {
			logger.Logger.Warnw("Looping sound via chan is not supported", nil)
			handle := newPlayHandle()
			handle.markPlayoutDone()
			return handle
		}
	}

	handle := newPlayHandle()
	p.playTasks.Add(1)
	go p.playTask(handle, soundSource, volume, loop)
	return handle
}

func (p *BackgroundAudioPlayer) playTask(handle *PlayHandle, sound AudioSource, volume float64, loop bool) {
	defer p.playTasks.Done()
	defer handle.markPlayoutDone()

	var stream <-chan *model.AudioFrame
	switch s := sound.(type) {
	case string:
		stream = readAudioFramesFromFile(s, loop, handle.stopCh)
	case <-chan *model.AudioFrame:
		stream = s
	}

	if stream == nil {
		return
	}

	processedStream := make(chan *model.AudioFrame)
	p.mu.Lock()
	p.activeStreams[handle] = processedStream
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.activeStreams, handle)
		p.mu.Unlock()
		close(processedStream)
	}()

	for {
		select {
		case <-handle.stopCh:
			return
		case <-p.mixerTaskCtx.Done():
			return
		case frame, ok := <-stream:
			if !ok {
				return
			}
			if volume != 1.0 {
				data := make([]byte, len(frame.Data))
				volFactor := math.Pow(10, math.Log10(volume))
				for i := 0; i < len(frame.Data); i += 2 {
					sample := int16(frame.Data[i]) | int16(frame.Data[i+1])<<8
					val := float64(sample) * volFactor
					if val > 32767 {
						val = 32767
					} else if val < -32768 {
						val = -32768
					}
					data[i] = byte(int16(val))
					data[i+1] = byte(int16(val) >> 8)
				}
				frame.Data = data
			}
			select {
			case <-handle.stopCh:
				return
			case processedStream <- frame:
			}
		}
	}
}

func (p *BackgroundAudioPlayer) Start(room *lksdk.Room, agentSession *AgentSession) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.room = room
	p.agentSession = agentSession

	ctx, cancel := context.WithCancel(context.Background())
	p.mixerTaskCtx = ctx
	p.mixerTaskCancel = cancel

	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  1,
	})
	if err != nil {
		return err
	}

	pub, err := room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name: "background_audio",
	})
	if err != nil {
		return err
	}
	p.publication = pub

	go p.runMixerTask(track)

	if p.ambientSound != nil {
		source, vol := p.normalizeSoundSource(p.ambientSound)
		if source != nil {
			switch source.(type) {
			case string:
				p.ambientHandle = p.Play(AudioConfig{Source: source, Volume: vol}, true)
			default:
				p.ambientHandle = p.Play(AudioConfig{Source: source, Volume: vol}, false)
			}
		}
	}

	return nil
}

func (p *BackgroundAudioPlayer) AgentStateChanged(newState AgentState) {
	p.mu.Lock()
	if newState == AgentStateSpeaking {
		p.targetVolume = 0.2 // Duck volume
	} else {
		p.targetVolume = 1.0 // Restore volume
	}
	p.mu.Unlock()

	if p.thinkingSound == nil {
		return
	}

	if newState == AgentStateThinking {
		if p.thinkingHandle != nil && !p.thinkingHandle.Done() {
			return
		}
		p.thinkingHandle = p.Play(p.thinkingSound, false)
	} else if p.thinkingHandle != nil {
		p.thinkingHandle.Stop()
	}
}

func (p *BackgroundAudioPlayer) Close() error {
	p.mu.Lock()
	if p.mixerTaskCancel != nil {
		p.mixerTaskCancel()
		p.mixerTaskCancel = nil
	}
	if p.publication != nil && p.room != nil {
		_ = p.room.LocalParticipant.UnpublishTrack(p.publication.SID())
	}
	p.mu.Unlock()

	p.playTasks.Wait()
	return nil
}

func (p *BackgroundAudioPlayer) runMixerTask(track *lksdk.LocalSampleTrack) {
	ticker := time.NewTicker(20 * time.Millisecond) // 20ms block
	defer ticker.Stop()

	for {
		select {
		case <-p.mixerTaskCtx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			// Smoothly interpolate volume
			if p.currentVolume < p.targetVolume {
				p.currentVolume += 0.05
				if p.currentVolume > p.targetVolume {
					p.currentVolume = p.targetVolume
				}
			} else if p.currentVolume > p.targetVolume {
				p.currentVolume -= 0.05
				if p.currentVolume < p.targetVolume {
					p.currentVolume = p.targetVolume
				}
			}
			duckFactor := p.currentVolume

			streams := make([]<-chan *model.AudioFrame, 0, len(p.activeStreams))
			for _, s := range p.activeStreams {
				streams = append(streams, s)
			}
			p.mu.Unlock()

			if len(streams) == 0 {
				silence := make([]byte, 48000*2*20/1000)
				track.WriteSample(media.Sample{Data: silence, Duration: 20 * time.Millisecond}, &lksdk.SampleWriteOptions{})
				continue
			}

			mixedData := make([]int32, 48000*20/1000)
			for _, s := range streams {
				select {
				case frame, ok := <-s:
					if ok && frame != nil {
						for i := 0; i < len(frame.Data)/2 && i < len(mixedData); i++ {
							sample := int16(frame.Data[i*2]) | int16(frame.Data[i*2+1])<<8
							// Apply ducking to the raw sample before mixing
							duckedSample := float64(sample) * duckFactor
							mixedData[i] += int32(duckedSample)
						}
					}
				default:
				}
			}

			outData := make([]byte, len(mixedData)*2)
			for i, val := range mixedData {
				if val > 32767 {
					val = 32767
				} else if val < -32768 {
					val = -32768
				}
				outData[i*2] = byte(int16(val))
				outData[i*2+1] = byte(int16(val) >> 8)
			}
			track.WriteSample(media.Sample{Data: outData, Duration: 20 * time.Millisecond}, &lksdk.SampleWriteOptions{})
		}
	}
}

func readAudioFramesFromFile(path string, loop bool, stopCh <-chan struct{}) <-chan *model.AudioFrame {
	out := make(chan *model.AudioFrame)

	go func() {
		defer close(out)
		for {
			select {
			case <-stopCh:
				return
			default:
				file, err := os.Open(path)
				if err != nil {
					logger.Logger.Errorw("failed to open audio file", err, "path", path)
					return
				}
				defer file.Close()

				var reader io.Reader = file
				var sampleRate uint32

				switch filepath.Ext(path) {
				case ".ogg":
					vorbis, err := oggvorbis.NewReader(file)
					if err != nil {
						logger.Logger.Errorw("failed to create ogg reader", err)
						return
					}
					sampleRate = uint32(vorbis.SampleRate())
					
					// Convert float32 to int16 PCM
					floatBuf := make([]float32, 4096)
					pcmBuf := make([]int16, 4096)
					
					for {
						n, err := vorbis.Read(floatBuf)
						if err == io.EOF {
							break
						} else if err != nil {
							logger.Logger.Errorw("error reading ogg file", err)
							return
						}

						for i := 0; i < n; i++ {
							val := floatBuf[i] * 32767
							if val > 32767 { val = 32767 }
							if val < -32768 { val = -32768 }
							pcmBuf[i] = int16(val)
						}

						out <- &model.AudioFrame{
							Data:              pcm16leToBytes(pcmBuf[:n]),
							SampleRate:        sampleRate,
							NumChannels:       uint32(vorbis.Channels()),
							SamplesPerChannel: uint32(n),
						}
					}

				case ".mp3":
					decoder, err := mp3.NewDecoder(file)
					if err != nil {
						logger.Logger.Errorw("failed to create mp3 decoder", err)
						return
					}
					sampleRate = uint32(decoder.SampleRate())
					reader = decoder
				
					buf := make([]byte, 4096)
					for {
						n, err := reader.Read(buf)
						if err == io.EOF {
							break
						} else if err != nil {
							logger.Logger.Errorw("error reading mp3 file", err)
							return
						}
						out <- &model.AudioFrame{
							Data:              buf[:n],
							SampleRate:        sampleRate,
							NumChannels:       2,
							SamplesPerChannel: uint32(n / 4), // 2 channels, 16-bit
						}
					}
				default:
					logger.Logger.Warnw("unsupported audio format for background audio", nil, "path", path)
					return
				}

				if !loop {
					return
				}
			}
		}
	}()
	return out
}

func pcm16leToBytes(in []int16) []byte {
	out := make([]byte, len(in)*2)
	for i, v := range in {
		out[i*2] = byte(v)
		out[i*2+1] = byte(v >> 8)
	}
	return out
}

