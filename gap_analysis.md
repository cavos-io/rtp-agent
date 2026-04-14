# 🔍 Gap Analysis: RTP Agent (Go) vs LiveKit Agents (Python)

**Date**: April 7, 2026 — **Updated**: April 9, 2026 (Post Bug-Fix Sessions 1 & 2)  
**Go Reference**: `cavos-io/rtp-agent` (181 files, Go 1.25+)  
**Python Reference**: `livekit/agents` (Python 3.9+, production-ready)

---

## How to Read This Document

Each gap is grouped by **priority** and includes:  
- **Python**: What already exists in Python  
- **Go**: What already exists in Go  
- **Gap**: What's missing  
- **Effort**: Estimated difficulty (🟢 Easy / 🟡 Medium / 🔴 Hard)
- **Status**: Current state after bug-fix sessions (✅ Closed / 🔄 Partial / ❌ Open)
- **Implementation Guide**: Step-by-step Go implementation details

---

## 🔧 Bug Fixes Applied (Sessions 1 & 2)

These were critical **runtime bugs** — not feature gaps — that prevented the agent from functioning at all. They were fixed before any gap work could be started.

| ID | Bug | Root Cause | Files Changed | Status |
|---|---|---|---|---|
| BUG-01 | No audio output to remote participants | WebRTC SDP rejected `Channels:1` as "codec not supported by remote" — `WriteSample()` silently dropped packets | `room_io.go` | ✅ Fixed |
| BUG-02 | SampleBuilder nil-pointer panic crashes entire program | `defer recover()` missing from audio goroutine — one panic killed the whole process | `room_io.go` | ✅ Fixed |
| BUG-03 | Agent never received job dispatch in `start` mode | Worker sent WebSocket ping frames instead of protobuf `WorkerPing` — server marked worker dead. Also never sent `UpdateWorkerStatus(WS_AVAILABLE)` | `server.go` | ✅ Fixed |
| BUG-04 | `Ctrl+C` hang — agent could not exit | `conn.ReadMessage()` blocks indefinitely; context cancel could not interrupt it | `server.go` | ✅ Fixed |
| BUG-05 | Agent not recognized by LiveKit Playground | `ParticipantKind` not set to `lksdk.ParticipantAgent` at connect time | `job.go` | ✅ Fixed |
| BUG-06 | Agent subscribed to other agents' audio tracks | No `rp.Kind()` filter in `onTrackSubscribed` — processed audio from itself when rejoining | `room_io.go` | ✅ Fixed |
| BUG-07 | Agent state not visible to Playground UI | `lk.agent.state` participant attribute never published | `agent_session.go` | ✅ Fixed |
| BUG-08 | ElevenLabs `input_timeout_exceeded` error | Text push and audio read ran sequentially — TTS provider timed out waiting for more input | `generation.go` | ✅ Fixed |
| BUG-09 | VAD rapid-toggling on background noise | No debounce — single frame above threshold triggered speech start | `simple_vad.go` | ✅ Fixed |

**Result of bug fixes**: The agent is now fully functional end-to-end. It connects to LiveKit, receives job assignments, decodes incoming audio, produces LLM + TTS responses, and publishes audible Opus audio back to the room.

---

## 📊 Gap Status After Bug-Fix Sessions

| Gap | Description | Priority | Status |
|---|---|---|---|
| GAP-01 | Pipeline Nodes (Override Points) | 🔴 Critical | ❌ Open |
| GAP-02 | Event System (EventEmitter Pattern) | 🔴 Critical | 🔄 Partial |
| GAP-03 | Error Recovery & Connection Resilience | 🔴 Critical | 🔄 Partial |
| GAP-04 | Comprehensive Test Suite | 🔴 Critical | ❌ Open |
| GAP-05 | Granular Recording Options | 🔴 Critical | ❌ Open |
| GAP-06 | Agent Handoff (Multi-Agent) | 🟠 High | ❌ Open |
| GAP-07 | False Interruption Handling | 🟠 High | ❌ Open |
| GAP-08 | Model String Inference | 🟠 High | ❌ Open |
| GAP-09 | Configurable I/O Layer | 🟠 High | ❌ Open |
| GAP-10 | TTS Text Transforms | 🟠 High | 🔄 Partial |
| GAP-11 | AEC Warmup | 🟠 High | ❌ Open |
| GAP-12 | AMD (Answering Machine Detection) | 🟡 Medium | ❌ Open |
| GAP-13 | Adaptive Interruption Detection | 🟡 Medium | 🔄 Partial |
| GAP-14 | Video Sampler (Adaptive FPS) | 🟡 Medium | ❌ Open |
| GAP-15 | Session Userdata (Type-Safe) | 🟡 Medium | ❌ Open |
| GAP-16 | HTTP MCP Client | 🟡 Medium | ❌ Open |
| GAP-17 | Remote Session Transport | 🟡 Medium | ❌ Open |
| GAP-18 | User Away Timeout | 🟡 Medium | ❌ Open |
| GAP-19 | Function Tool Decorator Pattern | 🟢 Low | ❌ Open |
| GAP-20 | Max Tool Steps Enforcement | 🟢 Low | ❌ Open |
| GAP-21 | Preemptive Generation | 🟢 Low | ❌ Open |
| GAP-22 | Language Code Support | 🟢 Low | ❌ Open |
| GAP-23 | Metrics & Usage Tracking (Granular) | 🟢 Low | ❌ Open |
| GAP-24 | Overlapping Speech Detection | 🟢 Low | ❌ Open |
| GAP-25 | ReadOnly ChatContext | ⚪ Nice-to-Have | ❌ Open |
| GAP-26 | Pydantic-style Event Validation | ⚪ Nice-to-Have | N/A |
| GAP-27 | Deprecation Helpers | ⚪ Nice-to-Have | ❌ Open |
| GAP-28 | CLI Dev Mode (Auto-Reload) Enhancement | ⚪ Nice-to-Have | ❌ Open |

**Totals**: ✅ 0 Closed · 🔄 4 Partially Addressed · ❌ 23 Open · N/A 1

---

## 🔴 CRITICAL — Required for Production

### GAP-01: Pipeline Nodes (Override Points)

| | Detail |
|---|---|
| **Python** | Agent has **5 override-able pipeline nodes**: `stt_node()`, `llm_node()`, `tts_node()`, `transcription_node()`, `realtime_audio_output_node()`. Developers can override any node for custom behavior without rewriting the entire pipeline. |
| **Go** | Pipeline is hardcoded in `PipelineAgent.run()` and `PipelineAgent.generateReply()`. No per-node override mechanism. |
| **Gap** | Cannot customize individual pipeline stages. For example, custom STT preprocessing or TTS chunking requires forking the entire pipeline. |
| **Effort** | 🟡 Medium (~150 LOC) |
| **Status** | ❌ **OPEN** — Not touched in bug-fix sessions. Pipeline in `pipeline_agent.go` remains hardcoded. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [pipeline_agent.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go), new `core/agent/pipeline_nodes.go`

1. Define node interfaces:
```go
type STTNode interface {
    Process(ctx context.Context, frame *model.AudioFrame) (*stt.SpeechEvent, error)
}
type LLMNode interface {
    Process(ctx context.Context, chatCtx *llm.ChatContext, tools []llm.Tool) (*LLMGenerationData, error)
}
type TTSNode interface {
    Process(ctx context.Context, textCh <-chan string) (*TTSGenerationData, error)
}
```
2. Create default implementations wrapping [PerformLLMInference](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/generation.go#21-63), [PerformTTSInference](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/generation.go#69-107)
3. Add node fields to [Agent](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent.go#33-54) struct: `STTNode`, `LLMNode`, `TTSNode` (nil = use default)
4. In `PipelineAgent.generateReply()`, check for custom nodes before calling defaults
</details>

---

### GAP-02: Event System (EventEmitter Pattern)

| | Detail |
|---|---|
| **Python** | [AgentSession](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#50-76) extends `EventEmitter` with **12 event types**: `user_state_changed`, `agent_state_changed`, `user_input_transcribed`, `conversation_item_added`, `agent_false_interruption`, `overlapping_speech`, `function_tools_executed`, `metrics_collected`, `session_usage_updated`, `speech_created`, `error`, `close`. |
| **Go** | Only 2 channel-based events (`AgentStateChangedCh`, `UserStateChangedCh`) and [ClientEventsDispatcher](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/events.go#100-104). Missing `error`, `close`, `function_tools_executed`, `agent_false_interruption`, `overlapping_speech` events. |
| **Gap** | Developers cannot subscribe to many important events. No error propagation or tool execution lifecycle events. |
| **Effort** | 🟡 Medium (~200 LOC) |
| **Status** | 🔄 **PARTIALLY ADDRESSED** — `UpdateAgentState()` now publishes `lk.agent.state` attribute via `room.LocalParticipant.SetAttributes()`, making agent state visible to LiveKit Playground. However, the Go event system itself is unchanged: still only 2 channel-based events; the 10+ missing Python event types (`error`, `close`, `function_tools_executed`, etc.) are not implemented. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [events.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/events.go), [agent_session.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go), new `core/agent/event_emitter.go`

1. Create `EventEmitter`:
```go
type EventHandler func(event Event)
type EventEmitter struct {
    handlers map[string][]EventHandler
    mu       sync.RWMutex
}
func (e *EventEmitter) On(eventType string, handler EventHandler)
func (e *EventEmitter) Emit(event Event)
```
2. Add missing event types: `AgentFalseInterruptionEvent`, `OverlappingSpeechEvent`, `FunctionToolsExecutedEvent`, `SessionUsageUpdatedEvent`, `ErrorEvent`
3. Embed `EventEmitter` in [AgentSession](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#50-76)
4. Call `s.Emit(event)` at every state change, error, and tool execution
</details>

---

### GAP-03: Error Recovery & Connection Resilience

| | Detail |
|---|---|
| **Python** | `SessionConnectOptions` with `APIConnectOptions` per provider (STT, LLM, TTS). `max_unrecoverable_errors` counter. Error count resets after successful agent speech. Provider-level retry with timeout. |
| **Go** | No retry logic, no per-provider connection options, no error counter. One error stops the pipeline. No WebSocket reconnection. |
| **Gap** | A single API error can crash the entire session. No graceful degradation. |
| **Effort** | 🟡 Medium (~200 LOC) |
| **Status** | 🔄 **PARTIALLY ADDRESSED** — Two improvements from bug-fix sessions: (1) `server.go` now closes the WebSocket on `ctx.Done()`, allowing clean shutdown; (2) `sendAvailable()` is called after job completion, so the worker can accept a new job after one ends. However, no retry logic, no per-provider error counters, and no WebSocket reconnect-with-backoff were added. A single LLM/STT/TTS API error still stops the pipeline permanently. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [pipeline_agent.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go), [server.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/worker/server.go), new `core/agent/connect_options.go`

1. Define options:
```go
type APIConnectOptions struct {
    MaxRetries int
    Timeout    time.Duration
    Backoff    time.Duration
}
type SessionConnectOptions struct {
    STT, LLM, TTS          APIConnectOptions
    MaxUnrecoverableErrors  int
}
```
2. Add `ConnectOptions` to [AgentSessionOptions](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#33-49)
3. Wrap LLM/STT/TTS calls with retry middleware:
```go
func withRetry(opts APIConnectOptions, fn func() error) error {
    for i := 0; i <= opts.MaxRetries; i++ {
        if err := fn(); err == nil { return nil }
        time.Sleep(opts.Backoff * time.Duration(i+1))
    }
    return lastErr
}
```
4. Add error counter to [AgentSession](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#50-76), reset on successful playout
5. In `server.go Run()`: wrap the entire connection loop in a reconnect loop with exponential backoff:
```go
for {
    if err := s.runOnce(ctx); err != nil && ctx.Err() == nil {
        time.Sleep(backoff)
        backoff = min(backoff*2, maxBackoff)
        continue
    }
    return
}
```
</details>

---

### GAP-04: Comprehensive Test Suite

| | Detail |
|---|---|
| **Python** | Unit tests, integration tests, mocking for all major components. |
| **Go** | Only **1 test file** found ([pre_connect_audio_test.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/worker/pre_connect_audio_test.go)). |
| **Gap** | Almost no test coverage. Dangerous for production. |
| **Effort** | 🔴 Hard (~500+ LOC) |
| **Status** | ❌ **OPEN** — No tests were added in either session. The bug-fix sessions focused on runtime correctness, not test coverage. The pipeline is now verified to work end-to-end manually, but automated test coverage remains ~0%. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: new `core/agent/*_test.go`, `interface/worker/*_test.go`, mock files per interface

1. Create mock implementations: `mock_stt.go`, `mock_tts.go`, `mock_llm.go`, `mock_vad.go`
2. Priority test files:
   - `pipeline_agent_test.go` — audio → STT → LLM → TTS flow
   - `agent_session_test.go` — lifecycle, state changes
   - `agent_activity_test.go` — speech scheduling, EOU detection
   - `room_io_test.go` — audio encode/decode, Opus stereo conversion
3. Use table-driven tests with `testing.T`
4. For Opus codec tests, verify stereo output: `len(pcm_in)*2 == len(stereo_out)`
</details>

---

### GAP-05: Granular Recording Options

| | Detail |
|---|---|
| **Python** | `RecordingOptions` with granular control: `audio`, `traces`, `logs`, `transcript` booleans. |
| **Go** | `RecorderIO` only records input/output audio. No granular control. |
| **Gap** | Cannot selectively enable/disable recording components. |
| **Effort** | 🟢 Easy (~30 LOC) |
| **Status** | ❌ **OPEN** — `RecorderIO` and `RoomOptions` were not changed. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [room_io.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/worker/room_io.go)

```go
type RecordingOptions struct {
    Audio, Traces, Logs, Transcript bool
}
```
Add to [RoomOptions](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/worker/room_io.go#104-106). Guard calls: `if rio.options.Recording.Audio { ... }`
</details>

---

## 🟠 HIGH — Critical for Feature Parity

### GAP-06: Agent Handoff (Multi-Agent)

| | Detail |
|---|---|
| **Python** | Full [AgentHandoff](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/llm/llm.go#82-88): agent can `handoff()` to another, ChatContext is transferred, tools updated. `AgentTask[T]` for sub-agents with typed results. |
| **Go** | [AgentHandoff](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/llm/llm.go#82-88) struct exists but **has no implementation**. `AgentTask[T]` generics exist but aren't integrated. |
| **Gap** | Multi-agent workflows are not possible. |
| **Effort** | 🟡 Medium (~100 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [agent.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent.go), [agent_session.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go), new `core/agent/handoff.go`

```go
func (s *AgentSession) Handoff(ctx context.Context, target AgentInterface) error {
    s.activity.Stop()
    s.Agent = target
    s.activity = NewAgentActivity(target, s)
    s.activity.Start()
    return nil
}
```
Transfer [ChatCtx](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/llm/remote_chat_context.go#23-32), fire [ConversationItemAddedEvent](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/events.go#29-33), wire `AgentTask[T]` completion.
</details>

---

### GAP-07: False Interruption Handling

| | Detail |
|---|---|
| **Python** | `AgentFalseInterruptionEvent` with `resumed` flag. `false_interruption_timeout` config. Automatic resume on false positive. `AdaptiveInterruptionDetector`. |
| **Go** | `FalseInterruptionTimeout` and `ResumeFalseInterruption` exist in [AgentSessionOptions](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#33-49) but are **not implemented**. |
| **Gap** | Agent cannot distinguish real interruptions from noise/false positives. Will frequently stop speaking incorrectly. |
| **Effort** | 🟡 Medium (~60 LOC) |
| **Status** | ❌ **OPEN** — `FalseInterruptionTimeout` field in `AgentSessionOptions` is still not wired to any logic. The VAD debounce from BUG-09 reduces the frequency of spurious speech-start events, but there is no actual false-interruption delay timer or automatic resume in `vadLoop()`. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [agent_activity.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_activity.go)

In [OnStartOfSpeech()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_activity.go#134-148), delay interruption:
```go
if session.Options.FalseInterruptionTimeout > 0 {
    go func() {
        timer := time.NewTimer(duration)
        select {
        case <-timer.C: a.cancelCurrentSpeech() // real interruption
        case <-speechEndCh: // false interruption → resume
        }
    }()
}
```
Emit `AgentFalseInterruptionEvent`. If `ResumeFalseInterruption=true`, re-schedule speech.
</details>

---

### GAP-08: Model String Inference (Auto-Resolution)

| | Detail |
|---|---|
| **Python** | `inference.STT.from_model_string("deepgram")`. Auto-resolve string to provider instance. |
| **Go** | Must manually instantiate each provider. |
| **Gap** | Developer experience is more verbose. |
| **Effort** | 🟡 Medium (~80 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: new `core/inference/registry.go`

```go
var sttRegistry = map[string]func(string) stt.STT{}
func RegisterSTT(name string, factory func(string) stt.STT)
func STTFromString(name, apiKey string) (stt.STT, error)
```
Register in each adapter's `init()`.
</details>

---

### GAP-09: Configurable I/O Layer

| | Detail |
|---|---|
| **Python** | `AgentInput`/`AgentOutput` as configurable I/O layer. Can swap audio/video/text independently. |
| **Go** | Audio I/O hardcoded in [RoomIO](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/worker/room_io.go#106-121). No abstraction for swapping. |
| **Gap** | Cannot easily add custom audio sources or output targets. |
| **Effort** | 🟡 Medium (~120 LOC) |
| **Status** | ❌ **OPEN** — `RoomIO` was significantly modified in Session 2 (Channels:2 fix, mono→stereo, Opus encoder fixes), but the I/O abstraction layer was not added. `PublishAudio` is still a concrete function pointer on `PipelineAgent`, not an interface. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [room_io.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/worker/room_io.go), new `core/agent/io.go`

```go
type AgentInput interface { AudioCh() <-chan *model.AudioFrame }
type AgentOutput interface { PublishAudio(frame *model.AudioFrame) error }
```
Make [RoomIO](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/worker/room_io.go#106-121) implement both. Change [PipelineAgent](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go#16-33) to accept interfaces.
</details>

---

### GAP-10: TTS Text Transforms

| | Detail |
|---|---|
| **Python** | `tts_text_transforms` config: `["filter_markdown", "filter_emoji"]` default. Extensible. |
| **Go** | `tts.ApplyTextTransforms()` exists but is not extensible. |
| **Gap** | Developers cannot easily customize text preprocessing before TTS. |
| **Effort** | 🟢 Easy (~30 LOC) |
| **Status** | 🔄 **PARTIALLY ADDRESSED** — `filters.go` now has complete implementations of `FilterMarkdown()` and `FilterEmoji()`. `generation.go` calls `tts.ApplyTextTransforms(text)` before pushing each chunk to the TTS provider. This means markdown and emoji are always stripped from TTS input. However, the transforms are **hardcoded** — there is no `TTSTextTransforms []TextTransform` field in `AgentSessionOptions`, so developers cannot add, remove, or reorder transforms at runtime. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [filters.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/tts/filters.go)

```go
type TextTransform func(string) string
var DefaultTransforms = []TextTransform{FilterMarkdown, FilterEmoji}
```
Add `TTSTextTransforms []tts.TextTransform` to [AgentSessionOptions](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#33-49). In `PerformTTSInference`, replace `tts.ApplyTextTransforms(text)` with:
```go
for _, fn := range transforms {
    text = fn(text)
}
```
</details>

---

### GAP-11: AEC Warmup

| | Detail |
|---|---|
| **Python** | `aec_warmup_duration` (default 3.0s). Interruptions disabled during warmup. |
| **Go** | `AECWarmupDuration` field exists but is **not implemented**. |
| **Gap** | Agent may detect echo as user speech at session start. |
| **Effort** | 🟢 Easy (~20 LOC) |
| **Status** | ❌ **OPEN** — `AECWarmupDuration` in `AgentSessionOptions` is still a no-op. Not wired to any logic. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [agent_session.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go)

In [Start()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go#57-65): set `s.aecWarmup = true`, start timer goroutine, clear after duration. In [OnAudioFrame()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go#237-243): skip VAD during warmup.
</details>

---

## 🟡 MEDIUM — Important for Quality

### GAP-12: AMD (Answering Machine Detection)

| | Detail |
|---|---|
| **Python** | `AMD` class detects voicemail vs human on outbound SIP calls. |
| **Go** | No AMD implementation. |
| **Gap** | For SIP outbound, agent cannot differentiate human vs voicemail. |
| **Effort** | 🟡 Medium (~100 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

New `core/agent/amd.go`. Analyze silence/speech patterns: voicemail greetings are typically >3s continuous speech. Integrate with `AgentSession.Start()`.
</details>

---

### GAP-13: Adaptive Interruption Detection

| | Detail |
|---|---|
| **Python** | `AdaptiveInterruptionDetector` — considers speech duration, overlap analysis, context. |
| **Go** | Simple VAD-based interruption (any speech → interrupt). |
| **Gap** | High false positive rate (background noise, coughing can trigger interruption). |
| **Effort** | 🟡 Medium (~80 LOC) |
| **Status** | 🔄 **PARTIALLY ADDRESSED** — `simple_vad.go` now has meaningful debounce: requires **3 consecutive frames above threshold** to declare speech start (~60ms at 20ms/frame), and **50 consecutive frames below threshold** to declare speech end (~1s silence). This significantly reduces false positives from brief noise bursts. However, there is no `AdaptiveInterruptionDetector` — no overlap analysis, no speech duration weighting, no context consideration. The interruption logic in `vadLoop()` still immediately cancels agent speech on first real speech-start event. |

<details>
<summary>📋 Implementation Guide</summary>

New `core/agent/adaptive_interruption.go`. Track overlap duration, ignore overlaps <300ms. Replace direct interruption in [vadLoop()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go#95-124):
```go
type AdaptiveInterruptionDetector struct {
    MinDuration time.Duration  // e.g., 300ms
    overlapStart time.Time
}
func (d *AdaptiveInterruptionDetector) OnSpeechStart() bool {
    d.overlapStart = time.Now()
    return false // don't interrupt yet
}
func (d *AdaptiveInterruptionDetector) OnSpeechProgress() bool {
    return time.Since(d.overlapStart) >= d.MinDuration
}
```
</details>

---

### GAP-14: Video Sampler

| | Detail |
|---|---|
| **Python** | `VoiceActivityVideoSampler` — adaptive FPS based on user state (`speaking_fps=1.0`, `silent_fps=0.3`). |
| **Go** | [video_sampler.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/video_sampler.go) exists (65 lines) but is basic. |
| **Gap** | Video processing is not adaptive based on conversation state. |
| **Effort** | 🟢 Easy (~30 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [video_sampler.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/video_sampler.go)

Add `SpeakingFPS`, `SilentFPS` config. Subscribe to `UserStateChangedCh`, adjust interval dynamically.
</details>

---

### GAP-15: Session Userdata (Type-Safe)

| | Detail |
|---|---|
| **Python** | `AgentSession[Userdata_T]` — generic typed userdata attached to session. |
| **Go** | No typed userdata in [AgentSession](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#50-76). |
| **Gap** | Developers must use `map[string]any` or custom fields. |
| **Effort** | 🟢 Easy (~5 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [agent_session.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go)

Add `Userdata any` field. Full generics `AgentSession[T]` would require refactoring all references — `any` is pragmatic.
</details>

---

### GAP-16: HTTP MCP Client (Full)

| | Detail |
|---|---|
| **Python** | Full SSE/HTTP MCP client implementation. |
| **Go** | [MCPServerHTTP](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/llm/mcp.go#20-26) is a stub — returns error "not fully supported". |
| **Gap** | Cannot connect to MCP servers using HTTP/SSE transport. |
| **Effort** | 🟡 Medium (~150 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [mcp.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/llm/mcp.go)

Implement SSE client with `bufio.NewReader`, JSON-RPC over HTTP POST, map MCP tool definitions to `llm.Tool`.
</details>

---

### GAP-17: Remote Session Transport

| | Detail |
|---|---|
| **Python** | `RoomSessionTransport` and `SessionHost` — support remote session management across different servers. |
| **Go** | No remote session concept. |
| **Gap** | Sessions must run in the same process/machine. |
| **Effort** | 🔴 Hard (~300 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

New `core/agent/transport.go`. Define `SessionTransport` interface with `SendAudio`, `RecvAudio`, `SendMessage`, [Close](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/tts/tts.go#39-40). Implement `WebSocketTransport` and `GRPCTransport`.
</details>

---

### GAP-18: User Away Timeout

| | Detail |
|---|---|
| **Python** | `user_away_timeout=15.0` — state changes to `"away"` after silence. |
| **Go** | `UserAwayTimeout` field exists but is **not implemented**. |
| **Gap** | Agent cannot detect user idle/away state. |
| **Effort** | 🟢 Easy (~20 LOC) |
| **Status** | ❌ **OPEN** — `UserAwayTimeout` in `AgentSessionOptions` is still a no-op. Not wired to any logic. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [agent_session.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go)

Track `lastSpeechAt` in [UpdateUserState()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/agent_session.go#184-202). Run goroutine: if `time.Since(lastSpeechAt) > timeout`, set `UserStateAway`.
</details>

---

## 🟢 LOW — Nice Polish

### GAP-19: Function Tool Decorator Pattern

| | Detail |
|---|---|
| **Python** | `@function_tool` decorator + `find_function_tools(self)` auto-discovers tools. |
| **Go** | Manual `llm.Tool` interface implementation. |
| **Gap** | More verbose to define tools. |
| **Effort** | 🟡 Medium (~60 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

New `core/llm/tool_helpers.go`. Reflection-based discovery using method prefix `Tool_` or `DiscoverTools(agent)`.
</details>

---

### GAP-20: Max Tool Steps Enforcement

| | Detail |
|---|---|
| **Python** | `max_tool_steps=3` — limits tool call chains to prevent infinite loops. |
| **Go** | `MaxToolSteps` field exists but is **not implemented** in the tool call loop. |
| **Gap** | Potential infinite loop if LLM keeps calling tools. |
| **Effort** | 🟢 Easy (~5 LOC) |
| **Status** | ❌ **OPEN** — The `for {}` loop at `pipeline_agent.go:188` still has no step counter. `MaxToolSteps` in `AgentSessionOptions` is a no-op. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [pipeline_agent.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go) line 188

Change:
```go
for {
```
To:
```go
maxSteps := session.Options.MaxToolSteps
if maxSteps <= 0 {
    maxSteps = 3
}
for step := 0; step < maxSteps; step++ {
```
</details>

---

### GAP-21: Preemptive Generation

| | Detail |
|---|---|
| **Python** | `preemptive_generation=True` — starts generating response while user is still speaking. Discards if turn isn't complete. |
| **Go** | `PreemptiveGeneration` field exists but is **not implemented**. |
| **Gap** | Missed latency optimization opportunity. |
| **Effort** | 🟡 Medium (~60 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [pipeline_agent.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go)

In [sttLoop()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go#140-166), handle interim transcripts: `go va.speculativeGenerate(text)`. Cancel if final differs; use pre-generated if matches.
</details>

---

### GAP-22: Language Code Support

| | Detail |
|---|---|
| **Python** | `LanguageCode` enum in events. Standardized BCP-47 codes. |
| **Go** | Language as plain `string`. |
| **Gap** | No standardized language handling. |
| **Effort** | 🟢 Easy (~30 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

New `core/stt/language.go`: `type LanguageCode string` + constants (`"en"`, `"id"`, `"ja"`, etc.). Add to [UserInputTranscribedEvent](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/events.go#19-26).
</details>

---

### GAP-23: Metrics & Usage Tracking (Granular)

| | Detail |
|---|---|
| **Python** | `AgentMetrics`, `ModelUsageCollector`, per-model token tracking, cost estimation. |
| **Go** | Basic `UsageCollector` and `UsageSummary`. Not as granular as Python. |
| **Gap** | Lacks per-model usage tracking and cost estimation. |
| **Effort** | 🟡 Medium (~80 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: `library/telemetry/usage_collector.go`

Add per-provider `ProviderUsage` struct. Record usage in [generateReply()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go#151-219). Emit `SessionUsageUpdatedEvent`.
</details>

---

### GAP-24: Overlapping Speech Detection

| | Detail |
|---|---|
| **Python** | `OverlappingSpeechEvent` — detects when user and agent speak simultaneously. |
| **Go** | No overlapping speech detection. |
| **Gap** | Cannot analyze conversation quality metrics. |
| **Effort** | 🟡 Medium (~20 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [pipeline_agent.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go), [events.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/events.go)

In [vadLoop()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/agent/pipeline_agent.go#95-124): if agent state is Speaking when user starts speaking → emit `OverlappingSpeechEvent`.
</details>

---

## ⚪ NICE-TO-HAVE — Enhancements

### GAP-25: ReadOnly ChatContext

| | Detail |
|---|---|
| **Python** | `_ReadOnlyChatContext` — immutable view passed to callbacks. |
| **Go** | `ChatContext.Copy()` exists but no read-only enforcement. |
| **Gap** | Developers can accidentally mutate ChatContext in callbacks. |
| **Effort** | 🟢 Easy (~15 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [chat_context.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/llm/chat_context.go)

Define `ReadOnlyChatContext` interface with only [Messages()](file:///c:/Go%20Project/src/Cavos/rtp-agent/core/llm/chat_context.go#13-22) and `Len()`. Pass to callbacks.
</details>

---

### GAP-26: Pydantic-style Event Validation

| | Detail |
|---|---|
| **Python** | All events use Pydantic `BaseModel` — auto-validation, serialization. |
| **Go** | Events use plain structs without validation. |
| **Gap** | No runtime validation for event data. |
| **Effort** | 🟢 Easy (~0 LOC) |
| **Status** | N/A |

> **No action needed** — Go structs are type-safe by default. Optionally add `validate` struct tags.

---

### GAP-27: Deprecation Helpers

| | Detail |
|---|---|
| **Python** | `@deprecate_params` decorator with auto-warnings. |
| **Go** | No deprecation mechanism. |
| **Gap** | Less important now, useful as API evolves. |
| **Effort** | 🟢 Easy (~15 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

New `library/deprecate/deprecate.go` — log warning at struct init when deprecated fields are set.
</details>

---

### GAP-28: CLI Dev Mode (Auto-Reload) Enhancement

| | Detail |
|---|---|
| **Python** | Full auto-reload with file watching and graceful restart. |
| **Go** | `RunWithDevMode` + [watcher.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/cli/watcher.go) exists but is basic. |
| **Gap** | Dev experience could be smoother. |
| **Effort** | 🟢 Easy (~30 LOC) |
| **Status** | ❌ **OPEN** — Not touched in either session. |

<details>
<summary>📋 Implementation Guide</summary>

**Files**: [interface/cli/watcher.go](file:///c:/Go%20Project/src/Cavos/rtp-agent/interface/cli/watcher.go)

Use `fsnotify` for file watching. Add graceful shutdown (SIGTERM). Debounce restarts (500ms).
</details>

---

## Summary

### Gap Status After Bug-Fix Sessions

| Priority | Total | ✅ Closed | 🔄 Partial | ❌ Open |
|---|---|---|---|---|
| 🔴 **Critical** | 5 | 0 | 2 (GAP-02, GAP-03) | 3 |
| 🟠 **High** | 6 | 0 | 2 (GAP-10, GAP-13) | 4 |
| 🟡 **Medium** | 7 | 0 | 0 | 7 |
| 🟢 **Low** | 6 | 0 | 0 | 6 |
| ⚪ **Nice-to-Have** | 4 | 0 | 0 | 3 (+1 N/A) |
| **Total** | **28** | **0** | **4** | **23** |

> **Key finding**: The two bug-fix sessions resolved 9 critical runtime bugs that made the agent non-functional. However, none of the 28 feature-parity gaps from the original analysis were closed — they remain open. The partially-addressed gaps (GAP-02, GAP-03, GAP-10, GAP-13) received incidental improvements as side effects of the bug fixes.

---

### Effort Breakdown (Remaining Work)

| Effort | Gaps | Est. LOC |
|---|---|---|
| 🟢 Easy | GAP-05, GAP-11, GAP-14, GAP-15, GAP-18, GAP-20, GAP-22, GAP-25, GAP-27, GAP-28 | ~175 |
| 🟡 Medium | GAP-01, GAP-02, GAP-03, GAP-06, GAP-07, GAP-08, GAP-09, GAP-13, GAP-16, GAP-19, GAP-21, GAP-23, GAP-24, GAP-10 | ~1,230 |
| 🔴 Hard | GAP-04, GAP-17 | ~800+ |

---

## 🗺️ Implementation Roadmap

Recommended implementation order based on impact vs. effort, now that the agent is functionally working:

### Phase 1 — Quick Wins (Easy gaps, high impact) `~175 LOC`

These are individually small and directly improve reliability or developer experience:

1. **GAP-20: Max Tool Steps** (`pipeline_agent.go:188`) — 5 LOC, prevents infinite LLM loops  
2. **GAP-11: AEC Warmup** (`agent_session.go`) — 20 LOC, prevents echo-triggered false starts on session open  
3. **GAP-18: User Away Timeout** (`agent_session.go`) — 20 LOC, clean up idle sessions  
4. **GAP-05: Granular Recording** (`room_io.go`) — 30 LOC, `RecordingOptions` struct  
5. **GAP-15: Session Userdata** (`agent_session.go`) — 5 LOC, `Userdata any` field  

### Phase 2 — Quality & Stability (Medium gaps) `~600 LOC`

6. **GAP-07: False Interruption** (`agent_activity.go`) — 60 LOC, `FalseInterruptionTimeout` timer in vadLoop  
7. **GAP-13: Adaptive Interruption** (new `adaptive_interruption.go`) — 80 LOC, overlap duration gating  
8. **GAP-03: Error Recovery** (`pipeline_agent.go`, `server.go`) — 200 LOC, retry middleware + WS reconnect loop  
9. **GAP-02: Event System** (new `event_emitter.go`) — 200 LOC, `EventEmitter` + missing event types  
10. **GAP-10: TTS Transforms** (`filters.go`, `agent_session.go`) — 30 LOC, make transforms configurable  

### Phase 3 — Architecture (Medium-Hard gaps) `~830 LOC`

11. **GAP-01: Pipeline Nodes** (new `pipeline_nodes.go`) — 150 LOC, per-node override mechanism  
12. **GAP-09: Configurable I/O** (new `core/agent/io.go`) — 120 LOC, `AgentInput`/`AgentOutput` interfaces  
13. **GAP-06: Agent Handoff** (new `handoff.go`) — 100 LOC, session-to-session transfer  
14. **GAP-16: HTTP MCP Client** (`mcp.go`) — 150 LOC, SSE + JSON-RPC transport  
15. **GAP-04: Test Suite** (new `*_test.go`) — 500+ LOC, mock implementations + table-driven tests  

### Phase 4 — Nice-to-Have `~430 LOC`

16. GAP-08: Model String Inference  
17. GAP-12: AMD  
18. GAP-14: Video Sampler  
19. GAP-21: Preemptive Generation  
20. GAP-23: Granular Metrics  
21. GAP-24: Overlapping Speech  
22. GAP-17: Remote Session Transport (hardest, lowest priority)
