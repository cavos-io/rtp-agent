package app

import (
	"context"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/interface/worker"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/livekit/protocol/livekit"
)

func TestRunSessionJoinsPreparedWorkerRoomBeforeStartingPipeline(t *testing.T) {
	base := agent.NewAgent("test")
	base.VAD = &fakeAppVAD{}
	base.STT = &fakeAppSTT{}
	base.LLM = &fakeAppLLM{}
	base.TTS = &fakeAppTTS{}
	session := agent.NewAgentSession(base, nil, agent.AgentSessionOptions{})
	application := &App{Session: session, Server: worker.NewAgentServer(worker.WorkerOptions{}), RoomOptions: workerlivekit.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true}}
	// Real worker entrypoints receive a prepared, non-nil room. It must still be
	// joined. Missing credentials fail locally without contacting a provider.
	job := worker.NewJobContext(&livekit.Job{Id: "prepared-test", Room: &livekit.Room{Name: "room-test"}}, "", "", "")
	job.PrepareRoom()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := application.runSessionWithContext(job, ctx)
	if err == nil || !strings.Contains(err.Error(), "connect prepared room") {
		t.Fatalf("prepared room join bypassed; err=%v", err)
	}
}

func TestRecordedSessionReportContainsAudioAndTranscript(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	roomIO := workerlivekit.NewRoomIO(nil, session, workerlivekit.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true})
	defer roomIO.Close()
	application := &App{Session: session, RoomIO: roomIO}
	job := worker.NewJobContext(&livekit.Job{Id: "recorded-test", Room: &livekit.Room{Sid: "RM_test", Name: "room-test"}}, "", "", "")
	job.SetSessionDirectory(t.TempDir())
	job.InitRecording(agent.RecordingOptions{Audio: true, Transcript: true, Traces: true, Logs: true})
	job.SetPrimarySession(session)
	job.SetPrimaryRoomIO(roomIO)
	if err := application.startAudioRecorder(job, roomIO); err != nil {
		t.Fatal(err)
	}
	pcm := make([]byte, 4800*2)
	for i := 0; i < 4800; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16((i%100)*200)))
	}
	frame := &model.AudioFrame{Data: pcm, SampleRate: 48000, NumChannels: 1, SamplesPerChannel: 4800}
	roomIO.Recorder.RecordInput(frame)
	roomIO.Recorder.RecordOutput(frame)
	session.EmitConversationItemAdded(&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}})
	session.EmitConversationItemAdded(&llm.ChatMessage{ID: "agent-1", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi"}}})
	if err := roomIO.StopRecorder(); err != nil {
		t.Fatal(err)
	}
	report, err := job.MakeSessionReport(session)
	if err != nil {
		t.Fatal(err)
	}
	if report.AudioRecordingPath == nil || report.AudioRecordingStartedAt == nil {
		t.Fatal("missing recording metadata")
	}
	data, err := os.ReadFile(*report.AudioRecordingPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 100 || string(data[:4]) != "OggS" {
		t.Fatalf("missing finalized Ogg audio, bytes=%d", len(data))
	}
	if report.ChatHistory == nil || len(report.ChatHistory.Items) != 2 {
		t.Fatalf("missing conversation history: %+v", report.ChatHistory)
	}
	if report.ChatHistory.Items[0].(*llm.ChatMessage).TextContent() != "hello" || report.ChatHistory.Items[1].(*llm.ChatMessage).TextContent() != "hi" {
		t.Fatal("transcript contents lost")
	}
}
