package cavos

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cavos-io/rtp-agent/adapter/cavos/spec"
	"github.com/cavos-io/rtp-agent/adapter/pipecat"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

// targetSampleRate is the rate the smart-turn model (Whisper feature extractor)
// expects. Inbound RTP audio (e.g. 48kHz, possibly multi-channel) is resampled and
// downmixed to 16kHz mono before feature extraction.
const targetSampleRate = 16000

// defaultSmartTurnGRPCAddr is the manual address of the grpc-llm smart-turn service.
// Adjust to the deployment DNS/port as needed.
const defaultSmartTurnGRPCAddr = "localhost:9001"

// Mel spectrogram shape produced by the Whisper feature extractor and expected by
// the SmartTurnServiceV1 proto (mel-major flattened: melBins * timeSteps float32).
const (
	smartTurnMelBins   = 80
	smartTurnTimeSteps = 800
)

// SmartTurn is an audio end-of-turn detector backed by the grpc-llm SmartTurnServiceV1
// service. Per the proto (Plan A), the client extracts mel features locally and ships
// the mel spectrogram; the server runs ONNX inference only.
// Implements agent.AudioTurnDetector (PredictEndOfTurnAudio).
type SmartTurn struct {
	conn      *grpc.ClientConn
	client    spec.SmartTurnServiceV1Client
	addr      string
	extractor *pipecat.WhisperFeatureExtractor
	mu        sync.Mutex // guards extractor (shared FFT state is not concurrency-safe)
}

type SmartTurnOption func(*SmartTurn)

// WithSmartTurnAddr overrides the default gRPC address.
func WithSmartTurnAddr(addr string) SmartTurnOption {
	return func(s *SmartTurn) {
		if addr != "" {
			s.addr = addr
		}
	}
}

// NewSmartTurn dials the smart-turn gRPC service (insecure, intra-cluster) and returns
// a ready detector.
func NewSmartTurn(opts ...SmartTurnOption) (*SmartTurn, error) {
	s := &SmartTurn{
		addr:      defaultSmartTurnGRPCAddr,
		extractor: pipecat.NewWhisperFeatureExtractor(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	conn, err := grpc.NewClient(s.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cavos smart turn: dial %s: %w", s.addr, err)
	}
	s.conn = conn
	s.client = spec.NewSmartTurnServiceV1Client(conn)
	return s, nil
}

// PredictEndOfTurnAudio extracts the mel spectrogram from the user audio frames
// locally, ships it to the smart-turn gRPC service, and returns the turn-completeness
// probability [0,1]. Empty audio returns 0 (not complete) without an RPC.
func (s *SmartTurn) PredictEndOfTurnAudio(ctx context.Context, frames []*model.AudioFrame) (float64, error) {
	// Normalize to 16kHz mono float32 (resample + downmix) so the mel features match
	// what the server's model expects, regardless of inbound RTP rate/channels.
	samples, err := framesToMono16k(frames)
	if err != nil {
		return 0, fmt.Errorf("cavos smart turn decode: %w", err)
	}
	if len(samples) == 0 {
		return 0, nil
	}

	// WhisperFeatureExtractor reuses a shared FFT; serialize access.
	s.mu.Lock()
	features, err := s.extractor.Extract(ctx, samples)
	s.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("cavos smart turn extract: %w", err)
	}

	resp, err := s.client.Predict(ctx, &spec.SmartTurnRequest{
		Features:  encodeFloat32LE(features),
		MelBins:   smartTurnMelBins,
		TimeSteps: smartTurnTimeSteps,
	})
	if err != nil {
		return 0, fmt.Errorf("cavos smart turn predict: %w", err)
	}
	return float64(resp.GetProbability()), nil
}

// Close releases the gRPC connection.
func (s *SmartTurn) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// framesToMono16k resamples each PCM16 frame to 16kHz and downmixes to mono,
// returning normalized float32 samples [-1,1]. Invalid/empty frames are skipped.
func framesToMono16k(frames []*model.AudioFrame) ([]float32, error) {
	var samples []float32
	for _, frame := range frames {
		if frame == nil || len(frame.Data) == 0 || frame.SampleRate == 0 || frame.NumChannels == 0 {
			continue
		}
		f, err := audio.ResampleAudioFrame(frame, targetSampleRate)
		if err != nil {
			return nil, err
		}
		if len(f.Data)%2 != 0 {
			return nil, fmt.Errorf("odd PCM16 byte length %d", len(f.Data))
		}
		ch := int(f.NumChannels)
		if ch < 1 {
			ch = 1
		}
		groups := (len(f.Data) / 2) / ch
		for i := 0; i < groups; i++ {
			acc := 0
			for c := 0; c < ch; c++ {
				off := (i*ch + c) * 2
				acc += int(int16(binary.LittleEndian.Uint16(f.Data[off:])))
			}
			samples = append(samples, float32(acc)/float32(ch)/32768.0)
		}
	}
	return samples, nil
}

// encodeFloat32LE serializes float32 samples to little-endian bytes.
func encodeFloat32LE(samples []float32) []byte {
	out := make([]byte, len(samples)*4)
	for i, v := range samples {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}
	return out
}
