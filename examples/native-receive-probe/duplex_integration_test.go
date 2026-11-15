//go:build duplexintegration

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hraban/opus"
	"github.com/livekit/protocol/auth"
	lkproto "github.com/livekit/protocol/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type duplexObservation struct {
	At                time.Time
	Samples           int
	A660, A990, A1320 float64
}

func observeDuplex(pcm []int16, rate int) duplexObservation {
	o := duplexObservation{At: time.Now(), Samples: len(pcm)}
	amplitude := func(hz float64) float64 {
		var re, im float64
		for i, v := range pcm {
			a := 2 * math.Pi * hz * float64(i) / float64(rate)
			re += float64(v) * math.Cos(a)
			im += float64(v) * math.Sin(a)
		}
		return 2 * math.Hypot(re, im) / float64(len(pcm))
	}
	o.A660 = amplitude(660)
	o.A990 = amplitude(990)
	o.A1320 = amplitude(1320)
	return o
}
func duplexTone(rate, n int, hz float64) []int16 {
	p := make([]int16, n)
	for i := range p {
		p[i] = int16(10000 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return p
}
func duplexBytes(p []int16) []byte {
	b := make([]byte, 2*len(p))
	for i, v := range p {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

// Parent Go process owns PCM production/consumption; only caller plus the native
// helper join the room. RoomService HTTP queries do not add a participant.
func TestSingleNativeRoomDuplex(t *testing.T) {
	url, binaryPath, out := os.Getenv("DUPLEX_TEST_URL"), os.Getenv("DUPLEX_HELPER"), os.Getenv("DUPLEX_RESULT_DIR")
	if url == "" || binaryPath == "" || out == "" {
		t.Fatal("DUPLEX_TEST_URL, DUPLEX_HELPER and DUPLEX_RESULT_DIR required")
	}
	if err := os.Mkdir(out, 0755); err != nil {
		t.Fatal(err)
	} // preserve previous runs
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var inbound, outbound []duplexObservation
	var packetCount, seqGaps int
	var lastSeq uint16
	var haveSeq bool
	var timestampDeltas []uint32
	var lastTS uint32
	receiverDone := make(chan struct{})
	subscribed := make(chan struct{}, 1)
	cb := lk.NewRoomCallback()
	cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, _ *lk.RemoteTrackPublication, p *lk.RemoteParticipant) {
		if p.Identity() != "native-agent" {
			return
		}
		subscribed <- struct{}{}
		go func() {
			defer close(receiverDone)
			decoder, err := opus.NewDecoder(48000, 1)
			if err != nil {
				return
			}
			pcm := make([]int16, 5760)
			for {
				packet, _, err := track.ReadRTP()
				if err != nil {
					return
				}
				n, err := decoder.Decode(packet.Payload, pcm)
				if err != nil {
					return
				}
				o := observeDuplex(pcm[:n], 48000)
				mu.Lock()
				packetCount++
				if haveSeq {
					if packet.SequenceNumber != lastSeq+1 {
						seqGaps++
					}
					timestampDeltas = append(timestampDeltas, packet.Timestamp-lastTS)
				}
				lastSeq = packet.SequenceNumber
				lastTS = packet.Timestamp
				haveSeq = true
				outbound = append(outbound, o)
				mu.Unlock()
			}
		}()
	}
	roomName := fmt.Sprintf("native-duplex-%d", time.Now().UnixNano())
	caller, err := lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: localKey, APISecret: localSecret, RoomName: roomName, ParticipantIdentity: "caller"}, cb)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Disconnect()
	track, err := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	pub, err := caller.LocalParticipant.PublishTrack(track, &lk.TrackPublicationOptions{Name: "caller-input", Source: lkproto.TrackSource_MICROPHONE})
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := auth.NewAccessToken(localKey, localSecret).SetIdentity("native-agent").SetKind(lkproto.ParticipantInfo_AGENT).SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: roomName}).ToJWT()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binaryPath, "-mode", "duplex-helper")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(out, "helper.stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		cancel()
		_ = stdin.Close()
		_ = command.Process.Kill()
		if !waited {
			_ = command.Wait()
		}
	}()
	encoder := json.NewEncoder(stdin)
	if err := encoder.Encode(map[string]string{"URL": url, "Token": jwt, "Participant": "caller", "TrackSID": pub.SID()}); err != nil {
		t.Fatal(err)
	}
	events := make(chan map[string]json.RawMessage, 128)
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			var ev map[string]json.RawMessage
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				readDone <- err
				return
			}
			var name string
			json.Unmarshal(ev["event"], &name)
			if name == "pcm" {
				var data []byte
				json.Unmarshal(ev["data"], &data)
				pcm := make([]int16, len(data)/2)
				for i := range pcm {
					pcm[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
				}
				o := observeDuplex(pcm, 24000)
				mu.Lock()
				inbound = append(inbound, o)
				mu.Unlock()
			} else {
				select {
				case events <- ev:
				case <-ctx.Done():
					readDone <- ctx.Err()
					return
				}
			}
		}
		readDone <- scanner.Err()
	}()
	// Preserve control events without PCM payloads or credentials.
	var controls []map[string]json.RawMessage
	waitEvent := func(want string) {
		t.Helper()
		for {
			select {
			case ev := <-events:
				controls = append(controls, ev)
				var name string
				json.Unmarshal(ev["event"], &name)
				if name == want {
					return
				}
			case err := <-readDone:
				t.Fatalf("helper ended waiting %s: %v", want, err)
			case <-ctx.Done():
				t.Fatalf("timeout waiting %s", want)
			}
		}
	}
	// Output publication can occur before or after input subscription.
	readyIn, readyOut := false, false
	for !readyIn || !readyOut {
		select {
		case ev := <-events:
			controls = append(controls, ev)
			var name string
			json.Unmarshal(ev["event"], &name)
			readyIn = readyIn || name == "subscribed"
			readyOut = readyOut || name == "output_published"
		case <-ctx.Done():
			t.Fatal("subscription timeout")
		case err := <-readDone:
			t.Fatalf("startup failed: %v", err)
		}
	}
	select {
	case <-subscribed:
	case <-ctx.Done():
		t.Fatal("caller did not subscribe output")
	}
	service := lk.NewRoomServiceClient(url, localKey, localSecret)
	checkParticipants := func() {
		t.Helper()
		r, err := service.ListParticipants(ctx, &lkproto.ListParticipantsRequest{Room: roomName})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Participants) != 2 {
			t.Fatalf("participants=%d, want two", len(r.Participants))
		}
		for _, p := range r.Participants {
			if p.Identity != "caller" && p.Identity != "native-agent" {
				t.Fatalf("unexpected participant %s", p.Identity)
			}
		}
	}
	checkParticipants()
	pubCtx, stopPublisher := context.WithCancel(ctx)
	defer stopPublisher()
	publishDone := make(chan error, 1)
	go func() {
		enc, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
		if err != nil {
			publishDone <- err
			return
		}
		pcm := duplexTone(48000, 960, 660)
		buf := make([]byte, 4000)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var seq uint16
		var ts uint32
		for {
			select {
			case <-pubCtx.Done():
				publishDone <- nil
				return
			case <-ticker.C:
				n, err := enc.Encode(pcm, buf)
				if err == nil {
					err = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: ts}, Payload: append([]byte(nil), buf[:n]...)}, nil)
				}
				if err != nil {
					publishDone <- err
					return
				}
				seq++
				ts += 960
			}
		}
	}()
	var id uint64 = 1000
	send := func(kind string, data []byte) {
		t.Helper()
		id++
		if err := encoder.Encode(duplexCommand{Event: kind, ID: id, Data: data}); err != nil {
			t.Fatal(err)
		}
		if kind == "capture" {
			waitEvent("captured")
		}
		if kind == "clear" {
			waitEvent("cleared")
		}
	}
	began := time.Now()
	for i := 0; i < 60; i++ {
		send("capture", duplexBytes(duplexTone(24000, 1200, 990)))
		time.Sleep(time.Until(began.Add(time.Duration(i+1) * 50 * time.Millisecond)))
	}
	checkParticipants()
	// Fill the native queue, then interrupt it through the supported clear API.
	burstAt := time.Now()
	for i := 0; i < 4; i++ {
		send("capture", duplexBytes(duplexTone(24000, 1200, 990)))
	}
	burstElapsed := time.Since(burstAt)
	if burstElapsed > 150*time.Millisecond {
		t.Errorf("200ms capture burst took %v: queued-clear evidence inconclusive", burstElapsed)
	}
	clearedAt := time.Now()
	send("clear", nil)
	time.Sleep(700 * time.Millisecond)
	secondAt := time.Now()
	for i := 0; i < 40; i++ {
		send("capture", duplexBytes(duplexTone(24000, 1200, 1320)))
		time.Sleep(time.Until(secondAt.Add(time.Duration(i+1) * 50 * time.Millisecond)))
	}
	time.Sleep(300 * time.Millisecond)
	checkParticipants()
	stopPublisher()
	if err := <-publishDone; err != nil {
		t.Fatal(err)
	}
	stoppedAt := time.Now()
	id++
	if err := encoder.Encode(duplexCommand{Event: "stop", ID: id}); err != nil {
		t.Fatal(err)
	}
	waitEvent("shutdown")
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	waitErr := command.Wait()
	waited = true
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	cleanup := time.Since(stoppedAt)
	stdin.Close()
	for _, ev := range controls {
		var name string
		json.Unmarshal(ev["event"], &name)
		if name == "shutdown" {
			var ok bool
			json.Unmarshal(ev["disconnect_callback"], &ok)
			if !ok {
				t.Error("disconnect callback missing")
			}
			json.Unmarshal(ev["retained_handles_released"], &ok)
			if !ok {
				t.Error("handle release failed")
			}
		}
	}
	remaining, err := service.ListParticipants(ctx, &lkproto.ListParticipantsRequest{Room: roomName})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Participants) != 1 || remaining.Participants[0].Identity != "caller" {
		t.Error("native participant remained")
	}
	caller.Disconnect()
	select {
	case <-receiverDone:
	case <-time.After(time.Second):
		t.Error("caller receiver did not settle")
	}
	mu.Lock()
	defer mu.Unlock()
	var inputSamples int
	var inputTone int
	var maxGap time.Duration
	for i, o := range inbound {
		if !o.At.Before(began) && o.At.Before(stoppedAt) {
			inputSamples += o.Samples
			if o.A660 > 1000 {
				inputTone++
			}
			if i > 0 && o.At.Sub(inbound[i-1].At) > maxGap {
				maxGap = o.At.Sub(inbound[i-1].At)
			}
		}
	}
	var first, second time.Time
	var lastOld time.Time
	var tone990, tone1320 int
	for _, o := range outbound {
		if o.A990 > 1000 {
			tone990++
			if first.IsZero() {
				first = o.At
			}
			if o.At.After(clearedAt) && o.At.Before(secondAt) {
				lastOld = o.At
			}
		}
		if o.A1320 > 1000 {
			tone1320++
			if second.IsZero() {
				second = o.At
			}
		}
	}
	pcmSeconds := float64(inputSamples) / 24000
	wall := stoppedAt.Sub(began).Seconds()
	if math.Abs(pcmSeconds-wall) > .25 {
		t.Errorf("input PCM %.3f wall %.3f", pcmSeconds, wall)
	}
	if maxGap > 250*time.Millisecond {
		t.Errorf("input gap %v", maxGap)
	}
	if inputTone < 40 || tone990 < 30 || tone1320 < 30 {
		t.Errorf("missing content input=%d output990=%d output1320=%d", inputTone, tone990, tone1320)
	}
	if first.IsZero() || first.Sub(began) > 500*time.Millisecond || first.Before(began) {
		t.Error("first output onset exceeds 500ms")
	}
	if second.IsZero() || second.Sub(secondAt) > 500*time.Millisecond || second.Before(secondAt) {
		t.Error("second output onset exceeds 500ms")
	}
	if !lastOld.IsZero() && lastOld.Sub(clearedAt) > 400*time.Millisecond {
		t.Error("old content persisted >400ms after clear")
	}
	if cleanup > 5*time.Second {
		t.Error("cleanup >5s")
	}
	summary := map[string]any{"participants_during": 2, "queued_burst_ms": float64(burstElapsed) / 1e6, "participants_after": len(remaining.Participants), "input_pcm_seconds": pcmSeconds, "wall_seconds": wall, "input_tone_frames": inputTone, "input_max_gap_ms": float64(maxGap) / 1e6, "output_packets": packetCount, "sequence_gap_events": seqGaps, "timestamp_deltas": timestampDeltas, "output_990_frames": tone990, "output_1320_frames": tone1320, "first_output_onset_ms": float64(first.Sub(began)) / 1e6, "second_output_onset_ms": float64(second.Sub(secondAt)) / 1e6, "cleanup_ms": float64(cleanup) / 1e6, "controls": controls}
	if !lastOld.IsZero() {
		summary["last_old_content_after_clear_ms"] = float64(lastOld.Sub(clearedAt)) / 1e6
	}
	observations, _ := json.Marshal(map[string]any{"input_24khz": inbound, "output_48khz": outbound, "began": began, "clear_sent": clearedAt, "second_sent": secondAt, "stopped": stoppedAt})
	if err := os.WriteFile(filepath.Join(out, "observations.json"), observations, 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "summary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("two participants; input %.3fs/%.3fs, output packets=%d, onset=%.1f/%.1fms, cleanup=%v", pcmSeconds, wall, packetCount, float64(first.Sub(began))/1e6, float64(second.Sub(secondAt))/1e6, cleanup)
}
