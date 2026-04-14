package silero

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/model"
	ort "github.com/yalue/onnxruntime_go"
)

// SileroVAD implements vad.VAD using onnxruntime-go directly.
type SileroVAD struct {
	modelPath string
	threshold float64
	mu        sync.Mutex
	initialized bool
}

// Ensure interface is implemented
var _ vad.VAD = (*SileroVAD)(nil)

func NewSileroVAD(opts ...func(*SileroVAD)) *SileroVAD {
	v := &SileroVAD{
		modelPath:  "silero_vad.onnx",
		threshold:  0.5,
	}

	for _, opt := range opts {
		opt(v)
	}

	return v
}

func WithModelPath(path string) func(*SileroVAD) {
	return func(v *SileroVAD) { v.modelPath = path }
}

func (v *SileroVAD) Label() string { return "silero-native" }

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.initialized {
		if !ort.IsInitialized() {
			ort.SetSharedLibraryPath("onnxruntime.dll")
			if err := ort.InitializeEnvironment(); err != nil {
				return nil, fmt.Errorf("onnxruntime init failed: %w", err)
			}
		}
		v.initialized = true
	}

	if _, err := os.Stat(v.modelPath); err != nil {
		return nil, fmt.Errorf("silero model not found at %s", v.modelPath)
	}

	// Tensors for V4 Silero (input, state, sr)
	inputShape := ort.NewShape(1, 512)
	inputTensor, _ := ort.NewEmptyTensor[float32](inputShape)
	
	stateShape := ort.NewShape(2, 1, 64)
	stateTensor, _ := ort.NewEmptyTensor[float32](stateShape)
	
	srShape := ort.NewShape(1)
	srTensor, _ := ort.NewTensor(srShape, []int64{16000})

	outputShape := ort.NewShape(1, 1)
	outputTensor, _ := ort.NewEmptyTensor[float32](outputShape)
	
	outStateShape := ort.NewShape(2, 1, 64)
	outStateTensor, _ := ort.NewEmptyTensor[float32](outStateShape)

	session, err := ort.NewAdvancedSession(v.modelPath,
		[]string{"input", "state", "sr"},
		[]string{"output", "out_state"},
		[]ort.Value{inputTensor, stateTensor, srTensor},
		[]ort.Value{outputTensor, outStateTensor},
		nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX session: %w", err)
	}

	stream := &sileroStream{
		parent:        v,
		session:       session,
		inputTensor:   inputTensor,
		stateTensor:   stateTensor,
		outputTensor:  outputTensor,
		outStateTensor: outStateTensor,
		eventChan:     make(chan *vad.VADEvent, 10),
		ctx:           ctx,
	}

	return stream, nil
}

type sileroStream struct {
	parent         *SileroVAD
	session        *ort.AdvancedSession
	inputTensor    *ort.Tensor[float32]
	stateTensor    *ort.Tensor[float32]
	outputTensor   *ort.Tensor[float32]
	outStateTensor *ort.Tensor[float32]
	
	buffer   []float32
	lastProb float32
	speaking bool
	
	eventChan chan *vad.VADEvent
	ctx       context.Context
	mu        sync.Mutex
}

func (s *sileroStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Convert and append to buffer
	floats := s.processAudio(frame)
	s.buffer = append(s.buffer, floats...)

	// Process 512-sample chunks
	for len(s.buffer) >= 512 {
		chunk := s.buffer[:512]
		s.buffer = s.buffer[512:]

		// Populate input tensor
		copy(s.inputTensor.GetData(), chunk)

		// Run inference
		if err := s.session.Run(); err != nil {
			return fmt.Errorf("inference failed: %w", err)
		}

		// Read output
		prob := s.outputTensor.GetData()[0]

		// Update state for next iteration
		copy(s.stateTensor.GetData(), s.outStateTensor.GetData())

		// Detection logic
		wasSpeaking := s.speaking
		s.speaking = prob >= float32(s.parent.threshold)

		if s.speaking && !wasSpeaking {
			s.emitEvent(vad.VADEventStartOfSpeech, prob)
		} else if !s.speaking && wasSpeaking {
			s.emitEvent(vad.VADEventEndOfSpeech, prob)
		}
	}

	return nil
}

func (s *sileroStream) emitEvent(eventType vad.VADEventType, prob float32) {
	evt := &vad.VADEvent{
		Type:        eventType,
		Speaking:    s.speaking,
		Probability: float64(prob),
		Timestamp:   float64(time.Now().UnixNano()) / 1e9,
	}
	select {
	case s.eventChan <- evt:
	default:
		// Drop event if channel is full to prevent blocking
	}
}

func (s *sileroStream) Next() (*vad.VADEvent, error) {
	select {
	case evt := <-s.eventChan:
		return evt, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *sileroStream) processAudio(frame *model.AudioFrame) []float32 {
	samples := len(frame.Data) / 2
	floats := make([]float32, samples)
	for i := 0; i < samples; i++ {
		val := int16(frame.Data[i*2]) | int16(frame.Data[i*2+1])<<8
		floats[i] = float32(val) / 32768.0
	}

	// Resample 24kHz -> 16kHz
	if frame.SampleRate == 24000 {
		resampled := make([]float32, 0, (len(floats)*2)/3)
		for i := 0; i < len(floats); i += 3 {
			if i+1 < len(floats) {
				resampled = append(resampled, floats[i], floats[i+1])
			}
		}
		return resampled
	}
	return floats
}

func (s *sileroStream) Flush() error { return nil }

func (s *sileroStream) Close() error {
	if s.session != nil {
		s.session.Destroy()
	}
	if s.inputTensor != nil { s.inputTensor.Destroy() }
	if s.stateTensor != nil { s.stateTensor.Destroy() }
	if s.outputTensor != nil { s.outputTensor.Destroy() }
	if s.outStateTensor != nil { s.outStateTensor.Destroy() }
	return nil
}
