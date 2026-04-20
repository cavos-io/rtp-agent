# RTP Agent Voice Pipeline — Progress Report

> **Last Updated**: 2026-04-08
> **Project**: `c:\Go Project\src\Cavos\rtp-agent`
> **Goal**: Stabilize Go-based RTP agent's full voice pipeline (VAD → STT → LLM → TTS → Audio Output)

---

## Pipeline Status Overview

```
┌─────────┐    ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌──────────────┐
│  VAD    │───▶│  STT    │───▶│  LLM    │───▶│  TTS    │───▶│ Audio Output │
│  ✅     │    │  ✅     │    │  ✅     │    │  ✅     │    │  ✅ WORKING  │
└─────────┘    └─────────┘    └─────────┘    └─────────┘    └──────────────┘
```

| Component | Status | Details |
|-----------|--------|---------|
| **Opus Decode** | ✅ Working | RTP packets → PCM 48kHz 16-bit mono |
| **VAD** | ✅ Working | Debounce (3 start / 50 stop frames), threshold 0.0005 |
| **STT** | ✅ Working | OpenAI Whisper with WAV header, accurate transcription |
| **LLM** | ✅ Working | OpenAI GPT-4o, `parallel_tool_calls` fix applied |
| **TTS** | ✅ Working | ElevenLabs WebSocket, concurrent text push + audio read |
| **Audio Output** | ✅ **FIXED** | Opus stereo + SDPFmtpLine — user can hear responses |
| **`start` mode** | ✅ **FIXED** | Complete worker protocol, agent auto-joins rooms |

---

## Fix Session 2026-04-08 — All Changes & Explanations

---

### BUG 1 FIX: User Cannot Hear TTS Audio

#### Root Cause 1: Codec SDP Negotiation Failure

**File**: `interface/worker/room_io.go` — `Start()` function

**Problem**:
The Opus track was published with `Channels: 1` (mono). In WebRTC, the standard for Opus is `Channels: 2` (stereo descriptor) even if the content is mono. The Playground sends an SDP answer that doesn't match our declaration, resulting in this error:
```
"could not set remote description" "error"="unable to start track, codec is not supported by remote"
```
As a result, the track gets published in the SDK (no error) but RTP is never sent to the Playground because WebRTC negotiation fails at the SDP level.

**Fix**:
```go
// BEFORE
track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
    MimeType:  webrtc.MimeTypeOpus,
    ClockRate: 48000,
    Channels:  1,
})

// AFTER
track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
    MimeType:    webrtc.MimeTypeOpus,
    ClockRate:   48000,
    Channels:    2,                        // WebRTC Opus standard = 2
    SDPFmtpLine: "minptime=10;useinbandfec=1", // required fmtp for Opus WebRTC
})
```

**Why `SDPFmtpLine` matters**: Without this parameter, some WebRTC clients reject the codec because it doesn't match the standard Opus WebRTC profile.

---

#### Root Cause 2: Opus Encoder Input Mismatch (Mono vs Stereo)

**File**: `interface/worker/room_io.go` — `PublishAudio()` and `NewRoomIO()` functions

**Problem**:
After changing the track to `Channels: 2`, the Opus encoder must also accept stereo input (2ch). TTS produces mono PCM (1ch) — if we send mono PCM to an encoder configured for 2ch, Opus will reject it or produce incorrect output.

**Fix — change encoder to 2ch**:
```go
enc, _ := newOpusEncoder(48000, 2) // 2ch to match WebRTC Opus SDP
```

**Fix — convert mono PCM → stereo before encoding**:
```go
// TTS produces mono. Duplicate each sample to L and R channels.
stereo := make([]byte, len(pcmData)*2)
for i := 0; i < len(pcmData)/2; i++ {
    lo := pcmData[i*2]
    hi := pcmData[i*2+1]
    stereo[i*4]   = lo  // L
    stereo[i*4+1] = hi
    stereo[i*4+2] = lo  // R (duplicate)
    stereo[i*4+3] = hi
}
pcmData = stereo
```

**Why this matters**: The Opus encoder library (`hraban/opus`) validates that the input buffer length matches the number of channels. Stereo input = `960 samples × 2ch × 2 bytes = 3840 bytes` per 20ms frame.

---

#### Root Cause 3: Opus Encoder Mode AppVoIP Not Suitable for TTS

**File**: `interface/worker/room_io.go` — `newOpusEncoder()` function

**Problem**:
The Opus encoder for output was created with `opus.AppVoIP`. VoIP mode has built-in Voice Activity Detection (VAD) that can classify TTS audio as "not real human voice" and apply inappropriate compression.

**Fix**:
```go
// BEFORE
enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)

// AFTER
enc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
```

**Difference between AppVoIP and AppAudio**:
- `AppVoIP`: Optimized for voice calls, has built-in VAD, constrained bitrate
- `AppAudio`: Full audio fidelity, no additional VAD, suitable for TTS playback

---

#### Additional: Diagnostic Logging & WAV Debug Output

**File**: `interface/worker/room_io.go`

**Changes**:
1. **Frame logging**: Log the first 10 frames and every 100th frame — shows PCM input size and Opus output size for verification:
   ```
   🔉 [Publish] Frame #1: pcm=3840B → opus=127B dur=20ms
   ```
   If `opus=0B` → encoder issue. If `opus=2-4B` → DTX is active.

2. **WAV debug file**: Automatically saves the first 2 seconds of TTS PCM to `tts_debug.wav` before resampling. This file can be opened in Audacity/VLC to verify TTS audio content.

3. **Guard for sampleRate=0**: Prevents division-by-zero panic if a frame arrives with `SampleRate = 0`.

---

### BUG 2 FIX: SampleBuilder Nil Pointer Crash

**File**: `interface/worker/room_io.go` — `handleAudioTrack()` function

**Problem**:
```
panic: runtime error: invalid memory address or nil pointer dereference
goroutine 291 [running]:
github.com/livekit/server-sdk-go/v2/pkg/samplebuilder.(*SampleBuilder).popRtpPackets(0xc0001f0320, 0x0)
    samplebuilder.go:432 +0xc0
```

`samplebuilder.Pop()` crashes because it receives a nil RTP packet — likely when the Playground user disconnects or sends comfort-noise packets. In Go, **a panic in one goroutine crashes the entire program** — this was also one of the causes of BUG 1 (audio stops abruptly because the program dies while TTS is playing).

**Fix**:
```go
func (rio *RoomIO) handleAudioTrack(track *webrtc.TrackRemote) {
    defer func() {
        if r := recover(); r != nil {
            fmt.Printf("⚠️ [RoomIO] handleAudioTrack panic recovered: %v — track handler stopped\n", r)
        }
    }()
    // ...
}
```

**Why `defer recover()` here**: Placing recover at the start of the goroutine ensures any panic from samplebuilder (or other code in this goroutine) is caught locally. The goroutine stops but the program continues running — audio output from other goroutines is not affected.

---

### BUG 3 FIX: `start` Mode Not Receiving Job Dispatch

#### Root Cause 1: Ping Uses WebSocket Control Frame Instead of Protobuf

**File**: `interface/worker/server.go`

**Problem**:
The LiveKit agent protocol uses **application-level keepalive** via protobuf `WorkerPing` messages sent as binary WebSocket frames. The previous implementation sent `conn.WriteControl(websocket.PingMessage, ...)` — this is a WebSocket-level ping that **is ignored by LiveKit Cloud** as a worker health indicator. The server considers the worker unresponsive and never dispatches jobs to it.

**Fix — switch to protobuf WorkerPing**:
```go
// BEFORE (WebSocket control frame — ignored by server)
conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))

// AFTER (protobuf WorkerPing — recognized by server)
ping := &livekit.WorkerMessage{
    Message: &livekit.WorkerMessage_Ping{
        Ping: &livekit.WorkerPing{
            Timestamp: time.Now().UnixMilli(),
        },
    },
}
pb, _ := proto.Marshal(ping)
conn.WriteMessage(websocket.BinaryMessage, pb)
```

**Fix — set `PingInterval` in RegisterWorkerRequest**:
```go
Register: &livekit.RegisterWorkerRequest{
    Type:         livekit.JobType_JT_ROOM,
    AgentName:    s.Options.AgentName,
    Version:      "1.0.0",
    PingInterval: 5,   // ← tell server: we ping every 5 seconds
    Namespace:    &ns, // ← explicit namespace (empty = default)
    AllowedPermissions: &livekit.ParticipantPermission{...},
},
```

**Why `PingInterval` matters**: Without this field, the server doesn't know our ping interval and may timeout the worker too early.

**Fix — add WorkerPong handler**:
```go
case *livekit.ServerMessage_Pong:
    fmt.Printf("   🏓 WorkerPong received (lastTs=%d serverTs=%d)\n",
        m.Pong.LastTimestamp, m.Pong.Timestamp)
```

Previously, pong messages fell into the `default` handler and only logged a warning — not a functional problem but made debugging difficult.

---

#### Root Cause 2: Worker Does Not Send AVAILABLE Status After Registration

**File**: `interface/worker/server.go`

**Problem**:
After successfully registering (receiving WorkerId from server), the worker must send `UpdateWorkerStatus{Status: WS_AVAILABLE}` to tell the server that the worker is ready to accept jobs. Without this message, the server keeps the worker in an "initializing" state and never sends `AvailabilityRequest` even if a dispatch has been created.

This was proven by: `dispatch.exe` successfully creates a dispatch (gets Dispatch ID), but the worker never receives any message.

**Fix — send AVAILABLE after successful registration**:
```go
func (s *AgentServer) sendAvailable() error {
    status := livekit.WorkerStatus_WS_AVAILABLE
    update := &livekit.WorkerMessage{
        Message: &livekit.WorkerMessage_UpdateWorker{
            UpdateWorker: &livekit.UpdateWorkerStatus{
                Status:   &status,
                Load:     0.0,
                JobCount: 0,
            },
        },
    }
    b, _ := proto.Marshal(update)
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.conn.WriteMessage(websocket.BinaryMessage, b)
}

// Called after receiving RegisterWorkerResponse:
case *livekit.ServerMessage_Register:
    fmt.Printf("   ✅ Worker Registered! ID: %s\n", m.Register.WorkerId)
    s.sendAvailable() // ← CRITICAL: without this, no jobs are dispatched
```

**Fix — re-send AVAILABLE after job completes**:
```go
go func() {
    if err := s.entrypointFnc(jobCtx); err != nil { ... }
    s.sendAvailable() // ← ready to accept next job
}()
```

---

#### Root Cause 3: Ctrl+C Cannot Exit (Blocking ReadMessage)

**File**: `interface/worker/server.go`

**Problem**:
`conn.ReadMessage()` is a blocking call. When Ctrl+C is pressed, `signal.NotifyContext` cancels `ctx`, but the goroutine is already stuck inside `ReadMessage()` and cannot return to `select { case <-ctx.Done() }`. The program hangs forever.

**Fix**:
```go
// When ctx is canceled (Ctrl+C), close WebSocket to unblock ReadMessage
go func() {
    <-ctx.Done()
    fmt.Println("   🛑 Shutting down worker...")
    conn.Close()
}()
```

**How it works**: Ctrl+C → SIGINT → ctx cancel → goroutine closes connection → `ReadMessage()` returns error → loop exits → program exits cleanly.

---

### Additional Fix: Filter Audio from Other Agents

**File**: `interface/worker/room_io.go` — `onTrackSubscribed()` function

**Problem**:
When several agents from previous failed dispatches are still active in the room, the new agent subscribes to audio tracks from all other agents. This causes the pipeline (VAD, STT) to process audio from other agents — wasting resources and potentially causing feedback loops.

**Fix**:
```go
func (rio *RoomIO) onTrackSubscribed(..., rp *lksdk.RemoteParticipant) {
    if track.Kind() != webrtc.RTPCodecTypeAudio {
        return
    }
    // Only process audio from human (Standard) participants
    if rp.Kind() != lksdk.ParticipantStandard {
        fmt.Printf("   ↩️  Skipping audio from non-human participant (kind=%v)\n", rp.Kind())
        return
    }
    go rio.handleAudioTrack(track)
}
```

---

### Additional Fix: ParticipantKind = AGENT When Connecting

**File**: `interface/worker/job.go` — `Connect()` function

**Problem**:
The agent connects to the room without setting participant kind. The LiveKit server assigns `ParticipantKind.STANDARD` as default. The Playground web reads this kind to display the agent in the "agent" section. Without `ParticipantKind.AGENT`, the Playground doesn't recognize the participant as an agent.

**Fix**:
```go
room, err := lksdk.ConnectToRoom(c.url, lksdk.ConnectInfo{
    APIKey:              c.apiKey,
    APISecret:           c.apiSecret,
    RoomName:            c.Job.Room.Name,
    ParticipantIdentity: "agent-" + c.Job.Id[:8],
    ParticipantKind:     lksdk.ParticipantAgent, // ← Playground needs this
}, cb)
```

---

### Additional Fix: Publish `lk.agent.state` to Playground

**File**: `core/agent/agent_session.go` — `UpdateAgentState()` function

**Problem**:
The Playground displays agent status (listening, thinking, speaking) by reading the participant attribute `lk.agent.state`. Without this, the Playground doesn't know the agent's current state.

**Fix**:
```go
func (s *AgentSession) UpdateAgentState(state AgentState) {
    // ... update state ...

    // Publish to Playground via participant attributes
    if room != nil && room.LocalParticipant != nil {
        room.LocalParticipant.SetAttributes(map[string]string{
            "lk.agent.state": string(state),
        })
    }
}
```

Published states: `"initializing"`, `"idle"`, `"listening"`, `"thinking"`, `"speaking"`.

---

## Build & Run

```powershell
# Build
set CGO_ENABLED=1
set PATH=C:\msys64\ucrt64\bin;%PATH%
set PKG_CONFIG_PATH=C:\msys64\ucrt64\lib\pkgconfig
go build -o agent.exe ./cmd/main.go

# Run — start mode (auto-dispatch, RECOMMENDED)
.\agent.exe start

# Run — connect mode (manual room join)
.\agent.exe connect <room_name>

# Manual dispatch to a specific room (while start mode is running)
.\dispatch.exe <room_name>

# Generate token for testing
go run ./cmd/gentoken/main.go <room_name> <identity>
```

## LiveKit Cloud Credentials

```
LIVEKIT_URL=wss://first-test-smn9006t.livekit.cloud
LIVEKIT_API_KEY=APIbNwMFHLB4QtC
LIVEKIT_API_SECRET=ofPQ1UiLQ5Nf87lMX3pXrLyf87sBCz2iTMZ5eACocdoB
```

---

## Note: Playground UI "Waiting for agent audio track…"

This message appears in the LiveKit Playground web because the Playground is designed for **LiveKit Managed Agents** (agents deployed on LiveKit's own cloud infrastructure). For custom local agents connecting via the worker protocol, the Playground doesn't fully display the agent in the "agent section" even though audio works normally.

**This is not a bug in our code** — full functionality is working:
- User can speak and agent responds with voice ✅
- `lk.agent.state` is published (listening/thinking/speaking) ✅
- `ParticipantKind.AGENT` is set ✅

---

## Files Modified (Session 2026-04-08)

| File | Changes |
|------|---------|
| `interface/worker/room_io.go` | Opus stereo fix, AppAudio, recover panic, WAV debug, filter non-human audio |
| `interface/worker/server.go` | WorkerPing protobuf, UpdateWorkerStatus AVAILABLE, Ctrl+C fix, Pong handler |
| `interface/worker/job.go` | ParticipantKind = AGENT |
| `core/agent/agent_session.go` | Publish `lk.agent.state` attribute |
