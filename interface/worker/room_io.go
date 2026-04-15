package worker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/hraban/opus"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type AudioDecoder interface {
	Decode(data []byte) ([]byte, error)
	Close() error
}

type AudioEncoder interface {
	Encode(pcm []byte) ([]byte, error)
	Close() error
}

type opusDecoder struct {
	decoder   *opus.Decoder
	buf       []int16
	callCount int
}

func newOpusDecoder(sampleRate int, channels int) (*opusDecoder, error) {
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}
	// Max frame size is typically 120ms at 48kHz = 5760 samples per channel
	return &opusDecoder{
		decoder: dec,
		buf:     make([]int16, 5760*channels),
	}, nil
}

func (d *opusDecoder) Decode(data []byte) ([]byte, error) {
	n, err := d.decoder.Decode(data, d.buf)
	if err != nil {
		return nil, err
	}

	// Debug: log first few decoded int16 samples
	d.callCount++
	if d.callCount <= 5 {
		maxShow := 10
		if n < maxShow {
			maxShow = n
		}
		fmt.Printf("🔬 [Opus] Decode #%d: n=%d samples, first values: %v\n", d.callCount, n, d.buf[:maxShow])
	}

	// Convert int16 slice to byte slice (little-endian)
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		out[i*2] = byte(d.buf[i])
		out[i*2+1] = byte(d.buf[i] >> 8)
	}
	return out, nil
}

func (d *opusDecoder) Close() error {
	return nil
}

type opusEncoder struct {
	encoder *opus.Encoder
	buf     []byte
}

func newOpusEncoder(sampleRate int, channels int) (*opusEncoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
	if err != nil {
		return nil, err
	}
	return &opusEncoder{
		encoder: enc,
		buf:     make([]byte, 4000), // Max packet size
	}, nil
}

func (e *opusEncoder) Encode(pcm []byte) ([]byte, error) {
	// Convert byte slice back to int16 slice for Opus encoder
	in := make([]int16, len(pcm)/2)
	for i := 0; i < len(in); i++ {
		in[i] = int16(pcm[i*2]) | (int16(pcm[i*2+1]) << 8)
	}

	n, err := e.encoder.Encode(in, e.buf)
	if err != nil {
		return nil, err
	}

	out := make([]byte, n)
	copy(out, e.buf[:n])
	return out, nil
}

func (e *opusEncoder) Close() error {
	return nil
}

type RoomOptions struct {
}

type RoomIO struct {
	Room         *lksdk.Room
	AgentSession *agent.AgentSession
	Options      RoomOptions
	Recorder     *RecorderIO

	mu     sync.Mutex
	closed bool
	ctx    context.Context // session lifecycle context — cancelled on disconnect

	audioTrack *lksdk.LocalTrack
	decoder    AudioDecoder
	encoder    AudioEncoder

	preConnectAudio *PreConnectAudioHandler

	// Debug: collect PCM from TTS for WAV file verification
	publishCount  int
	pcmDebugBuf   []byte
	pcmDebugSaved bool
	pcmDebugSRate uint32
}

func NewRoomIO(room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) *RoomIO {
	dec, _ := newOpusDecoder(48000, 1)
	enc, _ := newOpusEncoder(48000, 2) // 2ch to match WebRTC Opus SDP

	preConnectAudio := NewPreConnectAudioHandler(room, 5*time.Second)
	preConnectAudio.Register()

	rio := &RoomIO{
		Room:            room,
		AgentSession:    session,
		Options:         opts,
		decoder:         dec,
		encoder:         enc,
		Recorder:        NewRecorderIO(session),
		preConnectAudio: preConnectAudio,
	}

	if session.Assistant == nil {
		session.Assistant = agent.NewPipelineAgent(session.VAD, session.STT, session.LLM, session.TTS, session.ChatCtx)
	}
	session.Assistant.PublishAudio = rio.PublishAudio

	return rio
}

func (rio *RoomIO) GetCallback() *lksdk.RoomCallback {
	cb := lksdk.NewRoomCallback()
	cb.OnTrackSubscribed = rio.onTrackSubscribed
	return cb
}

func (rio *RoomIO) Start(ctx context.Context) error {
	rio.ctx = ctx
	// WebRTC Opus standard requires Channels=2 and specific fmtp even for mono
	// content. Channels=1 causes "codec not supported by remote" SDP rejection.
	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   48000,
		Channels:    2,
		SDPFmtpLine: "minptime=10;useinbandfec=1",
	})
	if err != nil {
		return err
	}

	pub, err := rio.Room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:   "agent-audio",
		Source: livekit.TrackSource_MICROPHONE,
	})
	if err != nil {
		return err
	}

	// Store agent track SID for transcript attribution.
	if pub != nil {
		rio.AgentSession.SetAgentTrackSID(pub.SID())
		fmt.Printf("🎙️ [RoomIO] Agent audio track SID: %s\n", pub.SID())
	}

	rio.audioTrack = track

	// Start recorder: stereo OGG, left=user input, right=agent output
	if rio.Recorder != nil {
		roomName := rio.Room.Name()
		recPath := fmt.Sprintf("recordings/%s_%d.wav", roomName, time.Now().Unix())
		if err := rio.Recorder.Start(recPath, 48000); err != nil {
			fmt.Printf("⚠️ [RoomIO] Recorder start failed: %v\n", err)
		} else {
			fmt.Printf("🔴 [RoomIO] Recording started: %s\n", recPath)
		}
	}

	return nil
}

func (rio *RoomIO) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	fmt.Printf("📡 [RoomIO] onTrackSubscribed called: kind=%s codec=%s participant=%s\n", track.Kind().String(), track.Codec().MimeType, rp.Identity())
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}
	// Only process audio from human (Standard) participants — skip other agents
	if rp.Kind() != lksdk.ParticipantStandard {
		fmt.Printf("   ↩️  Skipping audio from non-human participant (kind=%v)\n", rp.Kind())
		return
	}
	// Store human participant info for transcript attribution.
	rio.AgentSession.SetRemoteUserIdentity(rp.Identity())
	rio.AgentSession.SetRemoteTrackSID(publication.SID())
	fmt.Printf("🎤 [RoomIO] Human participant: identity=%s trackSID=%s\n", rp.Identity(), publication.SID())
	fmt.Println("🎤 [RoomIO] Starting audio track handler...")
	go rio.handleAudioTrack(track)
}

func (rio *RoomIO) handleAudioTrack(track *webrtc.TrackRemote) {
	// BUG 2 fix: recover from SampleBuilder nil-pointer panic so the whole
	// program doesn't crash (which would also kill ongoing audio output).
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ [RoomIO] handleAudioTrack panic recovered: %v — track handler stopped\n", r)
		}
	}()

	fmt.Printf("🎧 [RoomIO] handleAudioTrack started: trackID=%s codec=%s sampleRate=%d\n", track.ID(), track.Codec().MimeType, track.Codec().ClockRate)

	// Create Opus decoder for this track
	decoder, err := newOpusDecoder(int(track.Codec().ClockRate), 1)
	if err != nil {
		fmt.Printf("❌ [RoomIO] Failed to create Opus decoder: %v\n", err)
		return
	}
	defer decoder.Close()
	fmt.Println("✅ [RoomIO] Opus decoder created")

	// Skip pre-connect audio wait — go straight to RTP reading
	fmt.Println("📦 [RoomIO] Starting RTP read loop...")

	sb := samplebuilder.New(20, &codecs.OpusPacket{}, track.Codec().ClockRate)

	var rtpCount int
	var sampleCount int
	for {
		// Check both closed flag and context cancellation
		rio.mu.Lock()
		closed := rio.closed
		rio.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-rio.ctx.Done():
			fmt.Println("🔌 [RoomIO] handleAudioTrack: context cancelled, exiting")
			return
		default:
		}

		pkt, _, err := track.ReadRTP()
		if err != nil {
			fmt.Printf("❌ [RoomIO] ReadRTP error: %v\n", err)
			if !errors.Is(err, io.EOF) {
				// log error
			}
			return
		}

		rtpCount++
		if rtpCount == 1 || rtpCount%500 == 0 {
			fmt.Printf("📦 [RoomIO] RTP packets read: %d (payload: %d bytes)\n", rtpCount, len(pkt.Payload))
		}

		sb.Push(pkt)
		for {
			sample := sb.Pop()
			if sample == nil {
				break
			}

			sampleCount++
			rawSize := len(sample.Data)
			pcm := sample.Data
			if decoded, err := decoder.Decode(sample.Data); err == nil {
				pcm = decoded
			} else if sampleCount <= 3 {
				fmt.Printf("⚠️ [RoomIO] Opus decode error: %v\n", err)
			}

			if sampleCount <= 5 || sampleCount%500 == 0 {
				fmt.Printf("🔊 [RoomIO] Sample #%d: raw=%d bytes → decoded=%d bytes, sampleRate=%d\n", sampleCount, rawSize, len(pcm), track.Codec().ClockRate)
			}

			frame := &model.AudioFrame{
				Data:              pcm,
				SampleRate:        track.Codec().ClockRate,
				NumChannels:       1,
				SamplesPerChannel: uint32(len(pcm) / 2),
			}

			if rio.Recorder != nil {
				rio.Recorder.RecordInput(frame)
			}
			rio.AgentSession.OnAudioFrame(context.Background(), frame)
		}
	}
}

func (rio *RoomIO) PublishAudio(frame *model.AudioFrame) error {
	rio.mu.Lock()
	track := rio.audioTrack
	encoder := rio.encoder

	// Debug: collect raw TTS PCM (before resampling) for WAV verification.
	// Saves first ~2s to tts_debug.wav so we can confirm the audio content.
	if !rio.pcmDebugSaved && len(frame.Data) > 0 {
		if rio.pcmDebugSRate == 0 {
			rio.pcmDebugSRate = frame.SampleRate
			if rio.pcmDebugSRate == 0 {
				rio.pcmDebugSRate = 24000
			}
		}
		rio.pcmDebugBuf = append(rio.pcmDebugBuf, frame.Data...)
		targetBytes := int(rio.pcmDebugSRate) * 2 * 2 // 2 seconds of 16-bit mono
		if len(rio.pcmDebugBuf) >= targetBytes {
			if err := writePCMToWAV("tts_debug.wav", rio.pcmDebugBuf, rio.pcmDebugSRate, 1); err == nil {
				fmt.Printf("💾 [Debug] Saved TTS PCM → tts_debug.wav (%d bytes @ %dHz)\n", len(rio.pcmDebugBuf), rio.pcmDebugSRate)
			} else {
				fmt.Printf("⚠️ [Debug] Failed to save tts_debug.wav: %v\n", err)
			}
			rio.pcmDebugSaved = true
			rio.pcmDebugBuf = nil // free memory
		}
	}

	rio.publishCount++
	count := rio.publishCount
	rio.mu.Unlock()

	if track == nil {
		return nil
	}

	pcmData := frame.Data
	sampleRate := frame.SampleRate
	samplesPerChannel := frame.SamplesPerChannel

	// Guard: if sampleRate is 0 assume 48kHz (already target rate, skip resample)
	if sampleRate == 0 {
		sampleRate = 48000
	}

	// Resample 24kHz → 48kHz if needed (Opus track is 48kHz)
	if sampleRate != 48000 {
		ratio := 48000 / sampleRate // e.g., 48000/24000 = 2
		numSamples := len(pcmData) / 2
		resampled := make([]byte, numSamples*int(ratio)*2)
		for i := 0; i < numSamples; i++ {
			lo := pcmData[i*2]
			hi := pcmData[i*2+1]
			for r := 0; r < int(ratio); r++ {
				idx := (i*int(ratio) + r) * 2
				resampled[idx] = lo
				resampled[idx+1] = hi
			}
		}
		pcmData = resampled
		samplesPerChannel = uint32(len(pcmData) / 2)
		sampleRate = 48000
	}

	// Record output at 48kHz mono (after resampling, before stereo conversion)
	if rio.Recorder != nil {
		rio.Recorder.RecordOutput(&model.AudioFrame{
			Data:              pcmData,
			SampleRate:        sampleRate,
			NumChannels:       1,
			SamplesPerChannel: samplesPerChannel,
		})
	}

	// Convert mono PCM → stereo (interleave L+R) to match Channels:2 in SDP.
	// Opus encoder is created with 2ch so it expects stereo input.
	stereo := make([]byte, len(pcmData)*2)
	for i := 0; i < len(pcmData)/2; i++ {
		lo := pcmData[i*2]
		hi := pcmData[i*2+1]
		stereo[i*4] = lo // L
		stereo[i*4+1] = hi
		stereo[i*4+2] = lo // R (duplicate)
		stereo[i*4+3] = hi
	}
	pcmData = stereo
	// samplesPerChannel stays the same (960); frame size is now 960*2ch*2bytes = 3840

	data := pcmData
	if encoder != nil {
		// At 48kHz stereo, 20ms = 960 samples/ch × 2ch × 2 bytes = 3840 bytes
		opusFrameBytes := 3840
		if len(pcmData) < opusFrameBytes {
			padded := make([]byte, opusFrameBytes)
			copy(padded, pcmData)
			pcmData = padded
			samplesPerChannel = 960
		}
		if encoded, err := encoder.Encode(pcmData); err == nil {
			data = encoded
		} else {
			fmt.Printf("❌ [RoomIO] Opus encode error: %v (pcmLen=%d samples=%d)\n", err, len(pcmData), samplesPerChannel)
			return nil // Skip this frame
		}
	}

	// Duration based on 48kHz output
	duration := time.Duration(samplesPerChannel) * time.Second / time.Duration(sampleRate)

	// Log first 10 frames and every 100 after, to verify non-zero Opus output
	if count <= 10 || count%100 == 0 {
		fmt.Printf("🔉 [Publish] Frame #%d: pcm=%dB → opus=%dB dur=%v\n", count, len(pcmData), len(data), duration)
	}

	if err := track.WriteSample(media.Sample{
		Data:     data,
		Duration: duration,
	}, nil); err != nil {
		fmt.Printf("❌ [RoomIO] WriteSample error: %v\n", err)
		return err
	}

	// Real-time pacing: wait one frame duration between writes
	time.Sleep(duration)
	return nil
}

func (rio *RoomIO) Close() error {
	rio.mu.Lock()
	rio.closed = true
	decoder := rio.decoder
	encoder := rio.encoder
	rio.decoder = nil
	rio.encoder = nil
	rio.audioTrack = nil
	rio.pcmDebugBuf = nil
	rio.mu.Unlock()

	if decoder != nil {
		decoder.Close()
	}
	if encoder != nil {
		encoder.Close()
	}
	if rio.Recorder != nil {
		if err := rio.Recorder.Stop(); err != nil {
			fmt.Printf("⚠️ [RoomIO] Recorder stop error: %v\n", err)
		} else {
			fmt.Printf("💾 [RoomIO] Recording saved: %s\n", rio.Recorder.OutPath)
		}
	}
	fmt.Println("🧹 [RoomIO] Resources cleaned up")
	return nil
}

// writePCMToWAV saves raw 16-bit PCM bytes to a WAV file for offline debugging.
func writePCMToWAV(filename string, pcm []byte, sampleRate uint32, channels uint32) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	dataSize := uint32(len(pcm))
	byteRate := sampleRate * channels * 2
	blockAlign := uint16(channels * 2)

	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataSize)) //nolint
	f.Write([]byte("WAVE"))
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))       //nolint
	binary.Write(f, binary.LittleEndian, uint16(1))        // PCM //nolint
	binary.Write(f, binary.LittleEndian, uint16(channels)) //nolint
	binary.Write(f, binary.LittleEndian, sampleRate)       //nolint
	binary.Write(f, binary.LittleEndian, byteRate)         //nolint
	binary.Write(f, binary.LittleEndian, blockAlign)       //nolint
	binary.Write(f, binary.LittleEndian, uint16(16))       // bits per sample //nolint
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, dataSize) //nolint
	f.Write(pcm)
	return nil
}
