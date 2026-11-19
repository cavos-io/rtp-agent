//go:build nativeinprocess && !ffmpeg

package nativeroom

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/hraban/opus"
	lk "github.com/livekit/protocol/livekit"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

type recordOnlyAssistant struct{ frames int }

func (*recordOnlyAssistant) Start(context.Context, *agent.AgentSession) error               { return nil }
func (a *recordOnlyAssistant) OnAudioFrame(context.Context, *model.AudioFrame)              { a.frames++ }
func (*recordOnlyAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {}

func TestNativeRecordOnlyRetention(t *testing.T) {
	for _, kind := range []string{"primary", "auxiliary", "helper-primary"} {
		t.Run(kind, func(t *testing.T) {
			job := worker.NewJobContext(&lk.Job{Id: "record-only", Room: &lk.Room{Name: "test"}}, "", "", "")
			defer os.RemoveAll(job.SessionDirectory())
			assistant := &recordOnlyAssistant{}
			session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
			session.Assistant = assistant
			session.SetInputAudioMuted(true)
			cfg := Config{Token: "fixture-token", Participant: "caller", TrackSID: "track"}
			r := NewInProcess(job, session, cfg, "", "")
			if kind == "helper-primary" {
				r = New(job, session, cfg, "", "")
			}
			defer r.Close()
			if err := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, RecordRemoteParticipants: true}); err != nil {
				t.Fatal(err)
			}
			var primary func(context.Context, *model.AudioFrame)
			var aux func(string, *model.AudioFrame)
			sentinel := errors.New("capture configured callbacks without connecting")
			r.connect = func(_ context.Context, cfg Config, consume func(context.Context, *model.AudioFrame)) (*client, error) {
				primary = consume
				aux = cfg.recordAux
				return nil, sentinel
			}
			if err := r.Start(context.Background()); !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if primary == nil || kind == "auxiliary" && aux == nil {
				t.Fatal("missing callback")
			}
			if kind == "helper-primary" && aux != nil {
				t.Fatal("helper acquired auxiliary recording")
			}
			path := filepath.Join(t.TempDir(), "record-only.ogg")
			if err := r.StartRecorder(path, 24000); err != nil {
				t.Fatal(err)
			}
			frame := &model.AudioFrame{SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 480, Data: make([]byte, 960)}
			for i := 0; i < 480; i++ {
				binary.LittleEndian.PutUint16(frame.Data[2*i:], uint16(int16(10000*math.Sin(2*math.Pi*1000*float64(i)/24000))))
			}
			for range 25 {
				if kind == "auxiliary" {
					aux("other-track", frame)
				} else {
					primary(context.Background(), frame)
				}
			}
			if assistant.frames != 0 {
				t.Fatal("muted audio reached agent", assistant.frames)
			}
			if err := r.StopRecorder(); err != nil {
				t.Fatal(err)
			}
			energy := recordOnlyEnergy(t, path)
			if kind == "helper-primary" {
				if energy != 0 {
					t.Fatal("helper mute behavior changed", energy)
				}
			} else if energy < 1000000 {
				t.Fatalf("muted %s recording lost: energy=%f", kind, energy)
			}
			session.SetInputAudioMuted(false)
			primary(context.Background(), frame)
			if assistant.frames != 1 {
				t.Fatal("unmuted input did not resume")
			}
			if aux != nil {
				aux("other-track", frame)
				if assistant.frames != 1 {
					t.Fatal("auxiliary input reached agent")
				}
			}
		})
	}
}

func recordOnlyEnergy(t *testing.T, path string) float64 {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, _, err := oggreader.NewWith(file)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := opus.NewDecoder(24000, 2)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 5760*2)
	var energy float64
	var samples int
	for {
		packet, _, err := reader.ParseNextPage()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.HasPrefix(packet, []byte("OpusTags")) {
			continue
		}
		n, err := decoder.Decode(packet, pcm)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range pcm[:n*2] {
			energy += float64(v) * float64(v)
			samples++
		}
	}
	if samples == 0 {
		return 0
	}
	return energy / float64(samples)
}
