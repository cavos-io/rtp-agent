// Isolated feasibility harness. No production receive code is imported.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/hraban/opus"
	"github.com/livekit/protocol/auth"
	lkproto "github.com/livekit/protocol/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type recorder struct {
	mu       sync.Mutex
	log, pcm *os.File
	enc      *json.Encoder
	offset   int64
	err      error
}

func newRecorder(dir, name string) (*recorder, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(dir, name+".jsonl"))
	if err != nil {
		return nil, err
	}
	p, err := os.Create(filepath.Join(dir, name+".pcm"))
	if err != nil {
		f.Close()
		return nil, err
	}
	return &recorder{log: f, pcm: p, enc: json.NewEncoder(f)}, nil
}
func (r *recorder) event(v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.enc.Encode(v); err != nil {
		r.err = err
	}
}
func (r *recorder) frame(at int64, f *model.AudioFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f.SampleRate != 24000 || f.NumChannels != 1 || len(f.Data) != int(f.SamplesPerChannel)*2 {
		return errors.New("invalid frame format")
	}
	if _, err := r.pcm.Write(f.Data); err != nil {
		return err
	}
	if err := r.enc.Encode(map[string]any{"event": "frame", "at": at, "delivered_at": nowNS(), "samples": f.SamplesPerChannel, "rate": f.SampleRate, "offset": r.offset}); err != nil {
		return err
	}
	r.offset += int64(f.SamplesPerChannel)
	return nil
}
func (r *recorder) close() error { return errors.Join(r.log.Close(), r.pcm.Close(), r.err) }

const localKey = "devkey"
const localSecret = "local-native-receive-experiment-secret"

func token(room, id string) (string, error) {
	return auth.NewAccessToken(localKey, localSecret).SetIdentity(id).SetValidFor(time.Hour).SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: room}).ToJWT()
}

func main() {
	mode := flag.String("mode", "receive", "receive, publish, or token")
	url := flag.String("url", "ws://127.0.0.1:18991", "local test server")
	room := flag.String("room", "native-receive-probe", "test room")
	scenario := flag.String("scenario", "continuous", "continuous, dtx, loss")
	out := flag.String("out", "results/smoke", "result directory")
	fixture := flag.String("fixture", ".cache/speech.pcm", "48 kHz mono PCM16 speech fixture")
	flag.Parse()
	if *mode == "token" {
		s, e := token(*room, "python")
		if e != nil {
			panic(e)
		}
		fmt.Println(s)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	name := "go"
	if *mode == "publish" {
		name = "publisher"
	}
	sink, err := newRecorder(*out, name)
	if err != nil {
		panic(err)
	}
	if os.Getenv("PROBE_STATS_EPOCH_NS") != "" {
		clockAnchor(sink)
	}
	var finishDiagnostics func() error
	if os.Getenv("PROBE_DIAGNOSTICS") == "1" {
		finishDiagnostics = startHeartbeat(*out, name)
	}
	switch *mode {
	case "receive":
		var jwt string
		jwt, err = token(*room, "go-native")
		if err == nil {
			err = nativeReceive(ctx, *url, jwt, sink)
		}
	case "publish":
		err = publish(ctx, *url, *room, *scenario, *fixture, sink)
	default:
		err = errors.New("unknown mode")
	}
	if err != nil {
		sink.event(map[string]any{"event": "error", "error": err.Error(), "at": nowNS()})
	}
	if finishDiagnostics != nil {
		err = errors.Join(err, finishDiagnostics())
	}
	if os.Getenv("PROBE_STATS_EPOCH_NS") != "" {
		clockAnchor(sink)
	}
	err = errors.Join(err, flushPacketTrace())
	err = errors.Join(err, sink.close())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func publish(ctx context.Context, url, roomName, scenario, fixture string, sink *recorder) error {
	if scenario != "continuous" && scenario != "dtx" && scenario != "loss" {
		return errors.New("unknown scenario")
	}
	speech, err := os.ReadFile(fixture)
	if err != nil {
		return err
	}
	if len(speech) < 48000*2*20 {
		return errors.New("need at least 20 seconds speech fixture")
	}
	observerLog, err := os.Create(filepath.Join(filepath.Dir(sink.log.Name()), "observer.jsonl"))
	if err != nil {
		return err
	}
	defer observerLog.Close()
	var wg sync.WaitGroup
	var observerMu sync.Mutex
	oc := lk.NewRoomCallback()
	oc.OnTrackSubscribed = func(tr *webrtc.TrackRemote, _ *lk.RemoteTrackPublication, _ *lk.RemoteParticipant) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enc := json.NewEncoder(observerLog)
			for {
				p, _, e := tr.ReadRTP()
				if e != nil {
					return
				}
				observerMu.Lock()
				e = enc.Encode(map[string]any{"at": nowNS(), "seq": p.SequenceNumber, "ts": p.Timestamp, "bytes": len(p.Payload)})
				observerMu.Unlock()
				if e != nil {
					return
				}
			}
		}()
	}
	observer, err := lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: localKey, APISecret: localSecret, RoomName: roomName, ParticipantIdentity: "packet-observer"}, oc)
	if err != nil {
		return err
	}
	defer func() { observer.Disconnect(); wg.Wait() }()
	room, err := lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: localKey, APISecret: localSecret, RoomName: roomName, ParticipantIdentity: "publisher"}, nil)
	if err != nil {
		return err
	}
	defer room.Disconnect()
	track, err := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
	if err != nil {
		return err
	}
	if _, err = room.LocalParticipant.PublishTrack(track, &lk.TrackPublicationOptions{Name: "controlled-audio", Source: lkproto.TrackSource_MICROPHONE}); err != nil {
		return err
	}
	enc, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
	if err != nil {
		return err
	}
	if err = enc.SetDTX(scenario == "dtx"); err != nil {
		return err
	}
	if err = enc.SetBitrate(32000); err != nil {
		return err
	}
	if barrier := os.Getenv("PROBE_START_BARRIER"); barrier != "" {
		if err := waitPublisherBarrier(ctx, barrier); err != nil {
			return fmt.Errorf("publisher subscription barrier: %w", err)
		}
		sink.event(map[string]any{"event": "subscription_barrier_released", "at": nowNS()})
	} else {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	start := nowNS()
	sink.event(map[string]any{"event": "start", "at": start, "scenario": scenario, "clock": "CLOCK_MONOTONIC", "duration_s": 30})
	pcm := make([]int16, 960)
	buf := make([]byte, 4000)
	seq := uint16(1000)
	diagnostic := os.Getenv("PROBE_DIAGNOSTICS") == "1"
	stallIndex := -1
	if v := os.Getenv("PROBE_STALL_PACKET"); v != "" {
		stallIndex, err = strconv.Atoi(v)
		if err != nil {
			return err
		}
	}
	var source []byte
	if diagnostic {
		source = make([]byte, 0, 1500*960*2)
	}
	for i := 0; i < 1500; i++ {
		deadline := start + int64(i)*20_000_000
		if delay := deadline - nowNS(); delay > 0 {
			timer := time.NewTimer(time.Duration(delay))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		awake := nowNS()
		if i == stallIndex {
			injectedAt := nowNS()
			time.Sleep(450 * time.Millisecond) // Explicit publisher fault injection; never receiver policy.
			sink.event(map[string]any{"event": "injected_stall", "index": i, "at": injectedAt, "end": nowNS()})
		}
		sec := float64(i) * .02
		for j := range pcm {
			t := sec + float64(j)/48000
			pcm[j] = 0
			if t < 10 || t >= 20 {
				idx := i*960 + j
				if t >= 20 {
					idx -= 10 * 48000
				}
				pcm[j] = int16(binary.LittleEndian.Uint16(speech[idx*2:]))
				// Distinct 250ms marker at each content second; remaining 750ms is speech.
				marker := int(t)
				id := marker
				if marker >= 20 {
					id -= 10
				}
				if t-float64(marker) < .25 {
					pcm[j] = int16(10000 * math.Sin(2*math.Pi*float64(600+id*60)*t))
				}
			}
		}
		encodeStart := nowNS()
		n, e := enc.Encode(pcm, buf)
		if e != nil {
			return e
		}
		encodeEnd := nowNS()
		inDTX, e := enc.InDTX()
		if e != nil {
			return e
		}
		skip := scenario == "dtx" && inDTX && n <= 2
		lost := scenario == "loss" && i >= 1225 && i < 1250
		timestamp := uint32(100000 + i*960)
		sent := !skip && !lost
		at := nowNS()
		if sent {
			if e = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: timestamp}, Payload: append([]byte(nil), buf[:n]...)}, nil); e != nil {
				return e
			}
		}
		writeEnd := nowNS()
		if diagnostic {
			for _, sample := range pcm {
				source = binary.LittleEndian.AppendUint16(source, uint16(sample))
			}
			sink.event(map[string]any{"event": "packet_stages", "index": i, "awake": awake, "encode_start": encodeStart, "encode_end": encodeEnd, "write_end": writeEnd})
		}
		sink.event(map[string]any{"event": "packet", "at": at, "scheduled": deadline, "index": i, "seq": seq, "ts": timestamp, "bytes": n, "in_dtx": inDTX, "sent": sent, "loss": lost, "dtx_skip": skip})
		if !skip {
			seq++
		}
	}
	if diagnostic {
		if _, err = sink.pcm.Write(source); err != nil {
			return err
		}
	}
	time.Sleep(520 * time.Millisecond)
	sink.event(map[string]any{"event": "end", "at": nowNS()})
	return nil
}
