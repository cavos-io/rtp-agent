package cavos

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/cavos/spec"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const bargeSampleRate = 16000
const bargeWindowSamples = 32000
const bargeHistorySamples = 48000
const bargeTimeout = 700 * time.Millisecond

// AdaptiveInterruptionDetector calls Anura V2 once, at onset + 1 s. Anura
// owns preprocessing and the decision threshold. Each stream owns its connection.
type AdaptiveInterruptionDetector struct{ addr, dumpDir string }
type AdaptiveInterruptionOption func(*AdaptiveInterruptionDetector)

func WithAdaptiveInterruptionAddr(addr string) AdaptiveInterruptionOption {
	return func(d *AdaptiveInterruptionDetector) {
		if addr != "" {
			d.addr = addr
		}
	}
}
func WithAdaptiveInterruptionDumpDir(dir string) AdaptiveInterruptionOption {
	return func(d *AdaptiveInterruptionDetector) { d.dumpDir = dir }
}
func NewAdaptiveInterruptionDetector(opts ...AdaptiveInterruptionOption) *AdaptiveInterruptionDetector {
	d := &AdaptiveInterruptionDetector{addr: "anura.dev.cavos.internal:9090"}
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}
	return d
}
func (*AdaptiveInterruptionDetector) Model() string    { return "bargein-v50" }
func (*AdaptiveInterruptionDetector) Provider() string { return "cavos" }

var _ agent.InterruptionDetector = (*AdaptiveInterruptionDetector)(nil)
var _ agent.InterruptionStream = (*cavosInterruptionStream)(nil)

func (d *AdaptiveInterruptionDetector) Stream(ctx context.Context) (agent.InterruptionStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(d.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cavos barge-in client: %w", err)
	}
	return newCavosInterruptionStream(ctx, spec.NewBargeInServiceV1Client(conn), conn, d.dumpDir), nil
}

type cavosOverlap struct {
	startedAt                           time.Time
	trigger                             int64
	queued, sent, speechEnded, terminal bool
	ctx                                 context.Context
	cancel                              context.CancelFunc
	pcm                                 []byte
	queuedAt                            time.Time
	maxFrameGap                         time.Duration
}
type cavosInterruptionStream struct {
	mu                       sync.Mutex
	ctx                      context.Context
	cancel                   context.CancelFunc
	client                   spec.BargeInServiceV1Client
	conn                     *grpc.ClientConn
	dumpDir                  string
	now                      func() time.Time
	ring                     [bargeHistorySamples]int16
	total                    int64
	resampler                *audio.StreamingResampler
	inputRate, inputChannels uint32
	speaking, closed         bool
	interval                 uint64
	overlap                  *cavosOverlap
	pending                  *cavosOverlap
	ready, wake              chan struct{}
	done                     chan struct{}
	events                   []agent.OverlappingSpeechEvent // bounded to eight; overflow fails to VAD
	failure                  error
	lastFrameAt              time.Time
}

func newCavosInterruptionStream(ctx context.Context, client spec.BargeInServiceV1Client, conn *grpc.ClientConn, dumpDir string) *cavosInterruptionStream {
	ctx, cancel := context.WithCancel(ctx)
	s := &cavosInterruptionStream{ctx: ctx, cancel: cancel, client: client, conn: conn, dumpDir: dumpDir, now: time.Now, ready: make(chan struct{}, 1), wake: make(chan struct{}, 1), done: make(chan struct{})}
	go s.run()
	return s
}
func (s *cavosInterruptionStream) usableLocked() error {
	if s.failure != nil {
		return s.failure
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.ctx.Err()
}
func (s *cavosInterruptionStream) PushFrame(f *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	if f == nil {
		return nil
	}
	if f.SampleRate == 0 || f.NumChannels == 0 || f.SamplesPerChannel == 0 || uint64(len(f.Data)) != uint64(f.SamplesPerChannel)*uint64(f.NumChannels)*2 {
		return errors.New("cavos barge-in: invalid PCM16 frame format or sample count")
	}
	now := s.now()
	if o := s.overlap; o != nil && !o.queued && !o.terminal && !s.lastFrameAt.IsZero() {
		o.maxFrameGap = max(o.maxFrameGap, now.Sub(s.lastFrameAt))
	}
	s.lastFrameAt = now
	if s.inputRate != 0 && (s.inputRate != f.SampleRate || s.inputChannels != f.NumChannels) {
		return errors.New("cavos barge-in: audio format changed within stream")
	}
	s.inputRate, s.inputChannels = f.SampleRate, f.NumChannels
	mono := make([]byte, int(f.SamplesPerChannel)*2)
	for i := 0; i < int(f.SamplesPerChannel); i++ {
		var sum int64
		for c := 0; c < int(f.NumChannels); c++ {
			sum += int64(int16(binary.LittleEndian.Uint16(f.Data[(i*int(f.NumChannels)+c)*2:])))
		}
		binary.LittleEndian.PutUint16(mono[i*2:], uint16(int16(sum/int64(f.NumChannels))))
	}
	converted := &model.AudioFrame{Data: mono, SampleRate: f.SampleRate, NumChannels: 1, SamplesPerChannel: f.SamplesPerChannel}
	if f.SampleRate != bargeSampleRate {
		if s.resampler == nil {
			var err error
			s.resampler, err = audio.NewStreamingResampler(f.SampleRate, bargeSampleRate, 1)
			if err != nil {
				return err
			}
		}
		var err error
		converted, err = s.resampler.Push(converted)
		if err != nil {
			return err
		}
	}
	if converted == nil {
		return nil
	}
	for i := 0; i < len(converted.Data); i += 2 {
		s.ring[s.total%bargeHistorySamples] = int16(binary.LittleEndian.Uint16(converted.Data[i:]))
		s.total++
		if o := s.overlap; o != nil && !o.terminal && !o.queued && s.total >= o.trigger {
			s.queueLocked(o)
		}
	}
	return s.failure
}
func (s *cavosInterruptionStream) StartAgentSpeech() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	s.skipLocked("superseded", false, s.now())
	s.overlap = nil
	s.speaking = true
	s.interval++
	return nil
}
func (s *cavosInterruptionStream) StartOverlap(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	if !s.speaking {
		return nil
	}
	if t.IsZero() {
		return errors.New("cavos barge-in: overlap onset is required")
	}
	if s.overlap != nil && s.overlap.startedAt.UnixNano() == t.UnixNano() {
		return nil
	}
	s.skipLocked("superseded", false, s.now())
	elapsed := max(s.now().Sub(t), 0)
	// Compute once: output sample count is the continuous audio timeline.
	trigger := s.total - int64(elapsed.Seconds()*bargeSampleRate) + bargeSampleRate
	ctx, cancel := context.WithCancel(s.ctx)
	o := &cavosOverlap{startedAt: t, trigger: trigger, ctx: ctx, cancel: cancel}
	s.overlap = o
	logger.Logger.Infow("bargein.overlap_start", "overlap_started_at", t.UnixNano(), "elapsed_ms", float64(elapsed)/float64(time.Millisecond), "trigger_sample", trigger, "total_samples", s.total, "agent_interval", s.interval)
	if s.total >= trigger {
		s.queueLocked(o)
	}
	return s.failure
}
func (s *cavosInterruptionStream) EndOverlap(t time.Time, agentEnded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	if agentEnded {
		s.skipLocked("agent_ended", true, t)
	} else if s.overlap != nil {
		s.overlap.speechEnded = true
	}
	return s.failure
}
func (s *cavosInterruptionStream) EndAgentSpeech(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	s.speaking = false
	s.skipLocked("agent_ended", true, t)
	return s.failure
}
func (s *cavosInterruptionStream) signalLocked() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *cavosInterruptionStream) publishLocked(ev agent.OverlappingSpeechEvent) error {
	if len(s.events) >= 8 {
		return errors.New("cavos barge-in: verdict queue overflow")
	}
	s.events = append(s.events, ev)
	s.signalLocked()
	return nil
}
func (s *cavosInterruptionStream) errorLocked(o *cavosOverlap, err error) {
	if o.terminal {
		return
	}
	o.terminal = true
	o.cancel()
	logger.Logger.Errorw("bargein.error", err, "overlap_started_at", o.startedAt.UnixNano(), "grpc_code", status.Code(err).String())
	s.failure = err
	s.cancel()
	s.signalLocked()
}
func (s *cavosInterruptionStream) skipLocked(reason string, emit bool, at time.Time) {
	o := s.overlap
	if o == nil || o.terminal {
		return
	}
	if emit {
		now := s.now()
		ev := agent.OverlappingSpeechEvent{CreatedAt: now, DetectedAt: at, AgentEnded: true, OverlapStartedAt: &o.startedAt, DetectionDelay: max(now.Sub(o.startedAt), 0)}
		if o.sent {
			ev.NumRequests = 1
		}
		if err := s.publishLocked(ev); err != nil {
			s.errorLocked(o, err)
			return
		}
	}
	o.terminal = true
	o.cancel()
	logger.Logger.Infow("bargein.skip", "overlap_started_at", o.startedAt.UnixNano(), "reason", reason)
}
func (s *cavosInterruptionStream) queueLocked(o *cavosOverlap) {
	start := max(o.trigger-bargeWindowSamples, s.total-bargeHistorySamples, 0)
	if o.trigger <= start || o.trigger > s.total {
		s.errorLocked(o, errors.New("cavos barge-in: trigger window is outside retained audio"))
		return
	}
	o.pcm = make([]byte, int(o.trigger-start)*2)
	for i := start; i < o.trigger; i++ {
		binary.LittleEndian.PutUint16(o.pcm[(i-start)*2:], uint16(s.ring[i%bargeHistorySamples]))
	}
	o.queued = true
	o.queuedAt = s.now()
	logger.Logger.Debugw("bargein.window_ready", "overlap_started_at", o.startedAt.UnixNano(),
		"window_ready_ms", float64(o.queuedAt.Sub(o.startedAt))/float64(time.Millisecond),
		"trigger_sample", o.trigger, "total_samples", s.total, "window_samples", len(o.pcm)/2)
	s.pending = o
	select {
	case s.ready <- struct{}{}:
	default:
	}
}
func (s *cavosInterruptionStream) run() {
	defer close(s.done)
	defer func() {
		if s.conn != nil {
			_ = s.conn.Close()
		}
	}()
	defer func() { s.mu.Lock(); defer s.mu.Unlock(); s.skipLocked("cancelled", false, s.now()) }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.ready:
		}
		s.mu.Lock()
		o := s.pending
		s.pending = nil
		if o == nil || o.terminal || o != s.overlap || s.ctx.Err() != nil {
			s.mu.Unlock()
			continue
		}
		o.sent = true
		pcm := o.pcm
		o.pcm = nil
		logger.Logger.Infow("bargein.request", "overlap_started_at", o.startedAt.UnixNano(), "window_samples", len(pcm)/2, "padded_ms", float64(bargeWindowSamples-len(pcm)/2)/16, "trigger_wall_ms", float64(s.now().Sub(o.startedAt))/float64(time.Millisecond), "speech_ended", o.speechEnded,
			"window_ready_ms", float64(o.queuedAt.Sub(o.startedAt))/float64(time.Millisecond),
			"dispatch_delay_ms", float64(s.now().Sub(o.queuedAt))/float64(time.Millisecond),
			"max_input_frame_gap_ms", float64(o.maxFrameGap)/float64(time.Millisecond),
			"input_rate", s.inputRate, "input_channels", s.inputChannels)
		ctx, cancel := context.WithTimeout(o.ctx, bargeTimeout)
		s.mu.Unlock()
		s.infer(ctx, cancel, o, pcm)
	}
}

func (s *cavosInterruptionStream) infer(ctx context.Context, cancel context.CancelFunc, o *cavosOverlap, pcm []byte) {
	defer cancel()
	// Every sent request is inspectable, including errors and cancelled replies.
	// Disk I/O stays on the inference worker, outside the audio/state lock.
	defer func() {
		if s.dumpDir != "" {
			if err := writeBargeWAV(s.dumpDir, o.startedAt, pcm); err != nil {
				logger.Logger.Warnw("bargein.dump_failed", err, "overlap_started_at", o.startedAt.UnixNano())
			}
		}
	}()
	started := time.Now()
	resp, err := s.client.Predict(ctx, &spec.BargeInRequest{Pcm: pcm})
	rtt := time.Since(started)
	s.mu.Lock()
	defer s.mu.Unlock()
	// This lock also owns supersession: owned cancellation never reaches Next.
	if o.terminal || o != s.overlap || s.ctx.Err() != nil {
		return
	}
	if err != nil {
		s.errorLocked(o, fmt.Errorf("cavos barge-in predict: %w", err))
		return
	}
	if err = validateBargeResponse(resp); err != nil {
		s.errorLocked(o, err)
		return
	}
	now := s.now()
	ev := agent.OverlappingSpeechEvent{
		CreatedAt: now, DetectedAt: now, OverlapStartedAt: &o.startedAt,
		IsInterruption: resp.IsBargeIn, Probability: float64(resp.Probability),
		Probabilities:      []float32{resp.Probability},
		PredictionDuration: time.Duration((float64(resp.PreprocessMs) + float64(resp.InferenceMs)) * float64(time.Millisecond)),
		TotalDuration:      rtt, DetectionDelay: max(now.Sub(o.startedAt), 0), NumRequests: 1,
	}
	if err = s.publishLocked(ev); err != nil {
		s.errorLocked(o, err)
		return
	}
	o.terminal = true
	o.cancel()
	logger.Logger.Infow("bargein.verdict", "overlap_started_at", o.startedAt.UnixNano(), "model", "bargein-v50", "probability", resp.Probability, "threshold", resp.Threshold, "is_barge_in", resp.IsBargeIn, "preprocess_ms", resp.PreprocessMs, "inference_ms", resp.InferenceMs, "rtt_ms", float64(rtt)/float64(time.Millisecond))
}

func validateBargeResponse(r *spec.BargeInResponse) error {
	if r == nil {
		return errors.New("cavos barge-in: nil response")
	}
	for _, v := range []float32{r.Probability, r.Threshold, r.PreprocessMs, r.InferenceMs} {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < 0 {
			return errors.New("cavos barge-in: malformed response")
		}
	}
	if r.Probability > 1 || r.Threshold > 1 || float64(r.PreprocessMs)+float64(r.InferenceMs) > float64(math.MaxInt64)/float64(time.Millisecond) {
		return errors.New("cavos barge-in: invalid response range")
	}
	return nil
}
func (s *cavosInterruptionStream) Next() (agent.OverlappingSpeechEvent, error) {
	for {
		s.mu.Lock()
		if s.failure != nil {
			err := s.failure
			s.mu.Unlock()
			return agent.OverlappingSpeechEvent{}, err
		}
		if s.closed {
			s.mu.Unlock()
			return agent.OverlappingSpeechEvent{}, io.EOF
		}
		if len(s.events) > 0 {
			ev := s.events[0]
			s.events = s.events[1:]
			s.mu.Unlock()
			return ev, nil
		}
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			s.mu.Lock()
			err := s.failure
			s.mu.Unlock()
			if err == nil {
				err = s.ctx.Err()
			}
			return agent.OverlappingSpeechEvent{}, err
		case <-s.wake:
		}
	}
}
func (s *cavosInterruptionStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.skipLocked("cancelled", false, s.now())
	s.cancel()
	s.signalLocked()
	s.mu.Unlock()
	<-s.done
	return nil
}

// Dump exactly the sent PCM, without front-padding or normalization. Anura
// pads short windows; filenames use the exact overlap timestamp for correlation.
func writeBargeWAV(dir string, t time.Time, pcm []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data := make([]byte, 44+len(pcm))
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+len(pcm)))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], bargeSampleRate)
	binary.LittleEndian.PutUint32(data[28:], bargeSampleRate*2)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(pcm)))
	copy(data[44:], pcm)
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.wav", t.UnixNano())), data, 0600)
}
