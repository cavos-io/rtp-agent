# NVIDIA Riva Streaming STT Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace NVIDIA streaming STT's placeholder failure with an ordered,
cancellable Riva bidirectional gRPC transport for hosted NVCF and local Riva.

**Architecture:** Generate typed bindings from pinned official NVIDIA protos
inside `adapter/nvidia/internal/rivapb`. Keep gRPC ownership in a focused
`stt_transport.go`; `stt.go` retains public options, audio normalization, and
event conversion. One stream owns one RPC, ordered audio queue, ordered event
queue, and terminal lifecycle.

**Tech Stack:** Go 1.25, gRPC-Go, protobuf-go, NVIDIA Riva ASR protobufs,
Go `testing`, in-process gRPC server, existing TSV parity gate.

## Global Constraints

- Work only in `adapter/nvidia/**`, except the required TSV row and these
  Superpowers documents.
- Generate from `nvidia-riva/common` commit
  `71df98266725320a6b6b3a9f32a6da832dc93691`.
- Preserve NVIDIA MIT and embedded Google Apache-2.0 notices.
- Keep Riva protobuf types out of `core/**`.
- Support hosted TLS plus `authorization`/`function-id`, and local insecure
  transport plus `function-id` without authorization.
- Keep offline `Recognize` unsupported.
- Do not add JSON scenario files, one-off runners, retries, reconnection, TTS,
  or PersonaPlex changes.
- Use TDD: observe each focused test fail before its production change.
- Commit without `--no-verify`; rebase latest `origin/main` after each commit.

---

## File Map

- Create `adapter/nvidia/internal/rivapb/riva_audio.pb.go`: generated audio enum.
- Create `adapter/nvidia/internal/rivapb/riva_common.pb.go`: generated request ID.
- Create `adapter/nvidia/internal/rivapb/riva_asr.pb.go`: generated ASR messages.
- Create `adapter/nvidia/internal/rivapb/riva_asr_grpc.pb.go`: generated ASR client/server.
- Create `adapter/nvidia/internal/rivapb/UPSTREAM.md`: pinned source and regeneration command.
- Create `adapter/nvidia/internal/rivapb/LICENSE.nvidia-riva-common`: upstream MIT text.
- Create `adapter/nvidia/stt_transport.go`: config, credentials, client factory,
  sender, receiver, event queue, and terminal lifecycle.
- Modify `adapter/nvidia/stt.go`: stream transport fields and public method wiring.
- Modify `adapter/nvidia/nvidia_test.go`: config, local transport, metadata,
  response ordering, flush, error, cancellation, and close tests.
- Modify `scripts/parity-fixtures/test-cases.tsv`: one native streaming transport row.

---

### Task 1: Pin Riva Protocol and Build Reference Configuration

**Files:**
- Create: `adapter/nvidia/internal/rivapb/riva_audio.pb.go`
- Create: `adapter/nvidia/internal/rivapb/riva_common.pb.go`
- Create: `adapter/nvidia/internal/rivapb/riva_asr.pb.go`
- Create: `adapter/nvidia/internal/rivapb/riva_asr_grpc.pb.go`
- Create: `adapter/nvidia/internal/rivapb/UPSTREAM.md`
- Create: `adapter/nvidia/internal/rivapb/LICENSE.nvidia-riva-common`
- Create: `adapter/nvidia/stt_transport.go`
- Modify: `adapter/nvidia/nvidia_test.go`

**Interfaces:**
- Produces: `func nvidiaSTTStreamingConfig(*NvidiaSTT, string) *rivapb.StreamingRecognitionConfig`
- Produces: package `github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb`

- [ ] **Step 1: Write failing configuration test**

Add this focused test before generated bindings or helper code:

```go
func TestNvidiaSTTStreamingConfigMatchesReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "parakeet-rnnt-1.1b",
		WithNvidiaSTTSampleRate(24000),
		WithNvidiaSTTPunctuate(false),
		WithNvidiaSTTDiarization(true),
		WithNvidiaSTTMaxSpeakerCount(4),
	)
	if err != nil {
		t.Fatal(err)
	}

	got := nvidiaSTTStreamingConfig(provider, "id-ID")
	cfg := got.GetConfig()
	if cfg.GetEncoding() != rivapb.AudioEncoding_LINEAR_PCM ||
		cfg.GetSampleRateHertz() != 24000 ||
		cfg.GetLanguageCode() != "id-ID" ||
		cfg.GetModel() != "parakeet-rnnt-1.1b" ||
		cfg.GetMaxAlternatives() != 1 ||
		cfg.GetAudioChannelCount() != 1 ||
		!cfg.GetEnableWordTimeOffsets() ||
		cfg.GetEnableAutomaticPunctuation() || !got.GetInterimResults() {
		t.Fatalf("streaming config = %+v, want reference Riva config", got)
	}
	if d := cfg.GetDiarizationConfig(); d == nil ||
		!d.GetEnableSpeakerDiarization() || d.GetMaxSpeakerCount() != 4 {
		t.Fatalf("diarization config = %+v, want enabled with max 4", d)
	}
}
```

Import the not-yet-created package as:

```go
rivapb "github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb"
```

- [ ] **Step 2: Run test and verify RED**

Run:

```sh
go test ./adapter/nvidia -run '^TestNvidiaSTTStreamingConfigMatchesReference$' -count=1
```

Expected: build failure because `internal/rivapb` and
`nvidiaSTTStreamingConfig` do not exist.

- [ ] **Step 3: Generate pinned typed protocol bindings**

Use a fresh temporary checkout:

```sh
git clone https://github.com/nvidia-riva/common.git /tmp/nvidia-riva-common-71df982
git -C /tmp/nvidia-riva-common-71df982 checkout 71df98266725320a6b6b3a9f32a6da832dc93691
protoc -I /tmp/nvidia-riva-common-71df982 \
  --go_out=. \
  --go_opt=module=github.com/cavos-io/rtp-agent \
  --go_opt=Mriva/proto/riva_audio.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go_opt=Mriva/proto/riva_common.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go_opt=Mriva/proto/riva_asr.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go-grpc_out=. \
  --go-grpc_opt=module=github.com/cavos-io/rtp-agent \
  --go-grpc_opt=Mriva/proto/riva_audio.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go-grpc_opt=Mriva/proto/riva_common.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  --go-grpc_opt=Mriva/proto/riva_asr.proto=github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb \
  /tmp/nvidia-riva-common-71df982/riva/proto/riva_audio.proto \
  /tmp/nvidia-riva-common-71df982/riva/proto/riva_common.proto \
  /tmp/nvidia-riva-common-71df982/riva/proto/riva_asr.proto
```

The expected outputs are exactly the four `.pb.go` files listed above. Add
`UPSTREAM.md` containing the repository, commit, three input paths, exact
command, `protoc --version`, `protoc-gen-go --version`, and
`protoc-gen-go-grpc --version`. Copy the upstream MIT license text into
`LICENSE.nvidia-riva-common` using `apply_patch`.

- [ ] **Step 4: Add minimal config builder**

Create `adapter/nvidia/stt_transport.go`:

```go
package nvidia

import rivapb "github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb"

func nvidiaSTTStreamingConfig(s *NvidiaSTT, language string) *rivapb.StreamingRecognitionConfig {
	cfg := &rivapb.RecognitionConfig{
		Encoding:                   rivapb.AudioEncoding_LINEAR_PCM,
		SampleRateHertz:            int32(s.sampleRate),
		LanguageCode:               language,
		MaxAlternatives:            1,
		AudioChannelCount:          1,
		EnableWordTimeOffsets:      true,
		EnableAutomaticPunctuation: s.punctuate,
		Model:                      s.model,
	}
	if s.diarization {
		cfg.DiarizationConfig = &rivapb.SpeakerDiarizationConfig{
			EnableSpeakerDiarization: true,
			MaxSpeakerCount:          int32(s.maxSpeakerCount),
		}
	}
	return &rivapb.StreamingRecognitionConfig{
		Config:         cfg,
		InterimResults: true,
	}
}
```

- [ ] **Step 5: Run test and verify GREEN**

Run:

```sh
gofmt -w adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
go test ./adapter/nvidia -run '^TestNvidiaSTTStreamingConfigMatchesReference$' -count=1
go test ./adapter/nvidia/internal/rivapb -count=1
```

Expected: both commands pass.

- [ ] **Step 6: Commit**

```sh
git add adapter/nvidia/internal/rivapb adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
git commit -m "feat(nvidia): build riva stt config"
git fetch origin main
git rebase origin/main
```

---

### Task 2: Stream Ordered Audio Through Local Riva

**Files:**
- Modify: `adapter/nvidia/stt.go`
- Modify: `adapter/nvidia/stt_transport.go`
- Modify: `adapter/nvidia/nvidia_test.go`

**Interfaces:**
- Consumes: `nvidiaSTTStreamingConfig`
- Produces: `type nvidiaSTTClientFactory func(context.Context, *NvidiaSTT) (rivapb.RivaSpeechRecognitionClient, io.Closer, error)`
- Produces: `func (s *nvidiaSTTStream) runTransport()`
- Produces: `func (s *nvidiaSTTStream) enqueueTransportAudioLocked([]byte)`

- [ ] **Step 1: Write failing local transport test**

Add a test gRPC server using generated service types:

```go
type nvidiaRivaTestServer struct {
	rivapb.UnimplementedRivaSpeechRecognitionServer
	requests chan *rivapb.StreamingRecognizeRequest
	metadata chan metadata.MD
}

func (s *nvidiaRivaTestServer) StreamingRecognize(stream grpc.BidiStreamingServer[
	rivapb.StreamingRecognizeRequest,
	rivapb.StreamingRecognizeResponse,
]) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	s.metadata <- md
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		s.requests <- req
	}
}
```

Start `grpc.NewServer` on `net.Listen("tcp", "127.0.0.1:0")`, register the
server, and configure `NewNvidiaSTT` with that address and `use_ssl=false`.
Then assert the first request has config and the next two requests contain
exact copies of two pushed mono PCM frames:

```go
func TestNvidiaSTTNativeStreamingTransportSendsConfigAndAudioInOrder(t *testing.T) {
	server, address := startNvidiaRivaTestServer(t)
	provider, err := NewNvidiaSTT("", "model",
		WithNvidiaSTTServer(address),
		WithNvidiaSTTUseSSL(false),
		WithNvidiaSTTFunctionID("local-function"),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.Stream(ctx, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = stream.Close()
	}()

	first := &model.AudioFrame{Data: []byte{1, 0, 2, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 2}
	second := &model.AudioFrame{Data: []byte{3, 0, 4, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 2}
	if err := stream.PushFrame(first); err != nil { t.Fatal(err) }
	if err := stream.PushFrame(second); err != nil { t.Fatal(err) }

	configReq := receiveNvidiaRivaRequest(t, server.requests)
	firstReq := receiveNvidiaRivaRequest(t, server.requests)
	secondReq := receiveNvidiaRivaRequest(t, server.requests)
	if configReq.GetStreamingConfig() == nil { t.Fatal("first request missing config") }
	if !bytes.Equal(firstReq.GetAudioContent(), first.Data) { t.Fatalf("first audio = %v", firstReq.GetAudioContent()) }
	if !bytes.Equal(secondReq.GetAudioContent(), second.Data) { t.Fatalf("second audio = %v", secondReq.GetAudioContent()) }
}
```

- [ ] **Step 2: Run test and verify RED**

Run:

```sh
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransportSendsConfigAndAudioInOrder$' -count=1
```

Expected: timeout waiting for Riva requests because `PushFrame` still stores the
placeholder transport error.

- [ ] **Step 3: Add client factory and stream-owned transport state**

Add to `NvidiaSTT`:

```go
clientFactory nvidiaSTTClientFactory
```

Initialize it in `NewNvidiaSTT` to `newNvidiaSTTClient`. Add these stream fields:

```go
transportCancel context.CancelFunc
transportDone   chan struct{}
transportNotify chan struct{}
transportAudio  [][]byte
transportEOF    bool
events          []stt.SpeechEvent
```

Create credentials and client factory:

```go
type nvidiaSTTClientFactory func(context.Context, *NvidiaSTT) (rivapb.RivaSpeechRecognitionClient, io.Closer, error)

func nvidiaSTTTransportCredentials(useSSL bool) credentials.TransportCredentials {
	if useSSL {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return insecure.NewCredentials()
}

func newNvidiaSTTClient(_ context.Context, s *NvidiaSTT) (rivapb.RivaSpeechRecognitionClient, io.Closer, error) {
	conn, err := grpc.NewClient(s.server, grpc.WithTransportCredentials(nvidiaSTTTransportCredentials(s.useSSL)))
	if err != nil {
		return nil, nil, err
	}
	return rivapb.NewRivaSpeechRecognitionClient(conn), conn, nil
}
```

In `Stream`, create a derived context, initialize `transportDone` and
`transportNotify`, and start `go stream.runTransport()` before returning. Make
the existing `Close` call `transportCancel` so Task 2 tests and real callers can
stop the first transport implementation; Task 4 adds the final wait and
single-terminal-publication rules.

- [ ] **Step 4: Implement ordered sender**

Replace placeholder error assignment in `PushFrame` with:

```go
s.enqueueTransportAudioLocked(frame.Data)
```

Implement queue notification by closing and replacing `transportNotify` while
holding `s.mu`. `runTransport` must:

1. Create the client and close it on return.
2. Add outgoing metadata.
3. Open `StreamingRecognize`.
4. Send exactly one config request.
5. Send copied `transportAudio` entries FIFO.
6. Call `CloseSend` after `flushed` or `inputEnded` becomes true.

Use the generated oneof wrappers exactly:

```go
&rivapb.StreamingRecognizeRequest{
	StreamingRequest: &rivapb.StreamingRecognizeRequest_StreamingConfig{
		StreamingConfig: nvidiaSTTStreamingConfig(s.stt, s.language),
	},
}

&rivapb.StreamingRecognizeRequest{
	StreamingRequest: &rivapb.StreamingRecognizeRequest_AudioContent{
		AudioContent: append([]byte(nil), audio...),
	},
}
```

Never hold `s.mu` during `Send`, `CloseSend`, `Recv`, client creation, or
connection close.

- [ ] **Step 5: Run test and verify GREEN**

```sh
gofmt -w adapter/nvidia/stt.go adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransportSendsConfigAndAudioInOrder$' -count=1
```

Expected: pass with config first and two ordered audio requests.

- [ ] **Step 6: Commit**

```sh
git add adapter/nvidia/stt.go adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
git commit -m "feat(nvidia): stream audio to riva stt"
git fetch origin main
git rebase origin/main
```

---

### Task 3: Drain Ordered Interim and Final Events Across Flush

**Files:**
- Modify: `adapter/nvidia/stt.go`
- Modify: `adapter/nvidia/stt_transport.go`
- Modify: `adapter/nvidia/nvidia_test.go`

**Interfaces:**
- Consumes: local transport and generated Riva responses
- Produces: `func (s *nvidiaSTTStream) receiveTransportResponses(grpc.BidiStreamingClient[...]) error`
- Produces: event-first `Next` terminal behavior

- [ ] **Step 1: Write failing response/flush test**

Extend the test server so receipt of client EOF sends one response containing
an interim result followed by a final result, then returns:

```go
return stream.Send(&rivapb.StreamingRecognizeResponse{Results: []*rivapb.StreamingRecognitionResult{
	{Alternatives: []*rivapb.SpeechRecognitionAlternative{{Transcript: "hello", Confidence: .7}}, IsFinal: false},
	{Alternatives: []*rivapb.SpeechRecognitionAlternative{{Transcript: "hello there", Confidence: .9}}, IsFinal: true},
}})
```

The focused test must push one frame, call `Flush`, then call `Next` four times
and assert this exact sequence:

```go
[]stt.SpeechEventType{
	stt.SpeechEventStartOfSpeech,
	stt.SpeechEventInterimTranscript,
	stt.SpeechEventFinalTranscript,
	stt.SpeechEventEndOfSpeech,
}
```

The fifth `Next` must return `io.EOF`. Assert both transcript events share one
synthetic `nvidia-` request ID and contain the expected text.

- [ ] **Step 2: Run test and verify RED**

```sh
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransportDrainsFinalAfterFlush$' -count=1
```

Expected: failure because responses are not received or queued.

- [ ] **Step 3: Implement receiver and event-first terminal state**

Start response receiving after config send. For each response:

```go
converted := nvidiaSTTResponse{Results: make([]nvidiaSTTResult, 0, len(response.GetResults()))}
for _, result := range response.GetResults() {
	if len(result.GetAlternatives()) == 0 {
		continue
	}
	alt := result.GetAlternatives()[0]
	words := make([]nvidiaSTTWord, 0, len(alt.GetWords()))
	for _, word := range alt.GetWords() {
		words = append(words, nvidiaSTTWord{
			Word: word.GetWord(), StartTime: float64(word.GetStartTime()),
			EndTime: float64(word.GetEndTime()), SpeakerTag: int(word.GetSpeakerTag()),
		})
	}
	converted.Results = append(converted.Results, nvidiaSTTResult{
		IsFinal: result.GetIsFinal(),
		Alternative: nvidiaSTTAlternative{
			Transcript: alt.GetTranscript(), Confidence: float64(alt.GetConfidence()), Words: words,
		},
	})
}
events := s.eventsFromResponse(converted)
```

Append `events` under lock and notify blocked readers. On clean provider EOF,
mark `transportEOF=true`. On error, set `streamErr` unless stream context is
canceled or `Close` already owns termination.

Change `Next` priority to:

1. Pop queued event.
2. Return caller context error.
3. Return provider error.
4. Return EOF for closed/clean terminal state.
5. Wait for state notification.

- [ ] **Step 4: Run response, ordering, and existing conversion tests**

```sh
gofmt -w adapter/nvidia/stt.go adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
go test ./adapter/nvidia -run '^(TestNvidiaSTTNativeStreamingTransportDrainsFinalAfterFlush|TestNvidiaSTTResponseEventsMatchReferenceOrdering|TestNvidiaSTTResponseEventsPreserveMultipleResultOrder)$' -count=1
```

Expected: pass; fifth read returns EOF only after four ordered events.

- [ ] **Step 5: Commit**

```sh
git add adapter/nvidia/stt.go adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
git commit -m "feat(nvidia): drain riva stt transcripts"
git fetch origin main
git rebase origin/main
```

---

### Task 4: Enforce Metadata, Cancellation, Errors, and Close

**Files:**
- Modify: `adapter/nvidia/stt.go`
- Modify: `adapter/nvidia/stt_transport.go`
- Modify: `adapter/nvidia/nvidia_test.go`

**Interfaces:**
- Consumes: `nvidiaSTTClientFactory`, stream lifecycle
- Produces: hosted/local metadata contract and leak-free termination

- [ ] **Step 1: Write failing metadata tests**

For local mode, inspect incoming server metadata and assert:

```go
if got := md.Get("authorization"); len(got) != 0 { t.Fatalf("authorization = %v", got) }
if got := md.Get("function-id"); !slices.Equal(got, []string{"local-function"}) { t.Fatalf("function-id = %v", got) }
```

For hosted behavior without external TLS, set the provider's hosted fields but
inject a `nvidiaSTTClientFactory` that connects to the same in-process server
with insecure test credentials. The production worker still creates outgoing
metadata from the hosted provider fields. Inspect server-side incoming metadata
and assert it contains:

```go
authorization: Bearer secret
function-id: hosted-function
```

Also unit-test credential selection:

```go
if got := nvidiaSTTTransportCredentials(true).Info().SecurityProtocol; got != "tls" { t.Fatalf("hosted protocol = %q", got) }
if got := nvidiaSTTTransportCredentials(false).Info().SecurityProtocol; got != "insecure" { t.Fatalf("local protocol = %q", got) }
```

- [ ] **Step 2: Run metadata tests and verify RED**

```sh
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransport(HostedMetadata|LocalMetadata|Credentials)$' -count=1
```

Expected: missing metadata or credential-selection failures.

- [ ] **Step 3: Add exact outgoing metadata**

Before opening the RPC:

```go
pairs := []string{"function-id", s.stt.functionID}
if s.stt.apiKey != "" {
	pairs = append(pairs, "authorization", "Bearer "+s.stt.apiKey)
}
rpcCtx := metadata.NewOutgoingContext(transportCtx, metadata.Pairs(pairs...))
```

Use `rpcCtx` for `StreamingRecognize`. Do not attach authorization when the key
is empty.

- [ ] **Step 4: Write failing cancellation/error/close test**

Use a test server that blocks `Recv` until context cancellation. Assert:

- canceling caller context makes blocked `Next` return `context.Canceled`
- `Close` returns after server context closes
- second `Close` returns nil
- server `codes.Unavailable` before caller cancellation reaches `Next` once
- no later event is emitted after terminal error

Coordinate with channels named `rpcStarted`, `rpcDone`, and `release`; every
wait uses a one-second `select` deadline.

- [ ] **Step 5: Run lifecycle test and verify RED**

```sh
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransport(Cancel|Error|Close)' -count=1
```

Expected: at least one blocked operation or wrong terminal error.

- [ ] **Step 6: Implement cancellation-safe teardown**

`Close` must capture `transportDone`, cancel outside no blocking call, mark
closed under lock, notify readers, unlock, then wait for `transportDone`.
`runTransport` must always close the client connection and
`transportDone`. Sender and receiver goroutines must exit through the same
derived context. Terminal publication must occur once.

Use `errors.Is(err, context.Canceled)` and `status.Code(err)` only for
classification; preserve the original provider error returned by `Next`.

- [ ] **Step 7: Run all focused transport tests and full NVIDIA package**

```sh
gofmt -w adapter/nvidia/stt.go adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransport' -count=1
go test ./adapter/nvidia -count=1
```

Expected: all pass with no timeout, race, or leaked blocked test.

- [ ] **Step 8: Commit**

```sh
git add adapter/nvidia/stt.go adapter/nvidia/stt_transport.go adapter/nvidia/nvidia_test.go
git commit -m "fix(nvidia): close riva stt transport"
git fetch origin main
git rebase origin/main
```

---

### Task 5: Replace Placeholder Evidence and Run Parity Gate

**Files:**
- Modify: `adapter/nvidia/nvidia_test.go`
- Modify: `scripts/parity-fixtures/test-cases.tsv`

**Interfaces:**
- Consumes: complete native transport
- Produces: manifest case `nvidia-stt-reference-native-streaming-transport`

- [ ] **Step 1: Update obsolete placeholder tests without weakening lifecycle coverage**

Find tests that assert non-empty audio produces
`nvidia riva stt streaming is not implemented`. Replace only those assertions
with injected client-factory failures so the same asynchronous error surface is
still covered. Keep offline `Recognize` assertions unchanged.

Use a deterministic error:

```go
wantErr := errors.New("riva test transport unavailable")
provider.clientFactory = func(context.Context, *NvidiaSTT) (rivapb.RivaSpeechRecognitionClient, io.Closer, error) {
	return nil, nil, wantErr
}
```

Assert `PushFrame` returns nil and `Next` returns `wantErr`.

- [ ] **Step 2: Run existing STT suite and verify it detects stale expectations**

Before editing each stale assertion, run its exact test name. Expected: FAIL
because streaming transport is supported and no placeholder error is emitted.
After replacement, rerun the same test. Expected: PASS with the injected error.

- [ ] **Step 3: Add TSV manifest row**

Add one TSV row with no tabs inside fields:

```text
nvidia-stt-reference-native-streaming-transport	go-test	refs/agents/livekit-plugins/livekit-plugins-nvidia/livekit/plugins/nvidia/stt.py; refs/agents/livekit-plugins/livekit-plugins-nvidia/livekit/plugins/nvidia/auth.py	adapter/nvidia/nvidia_test.go	./adapter/nvidia	TestNvidiaSTTNativeStreamingTransportMatchesReference				stt native streaming transport contract	NVIDIA STT sends reference Riva configuration and ordered mono PCM, preserves interim/final response order, and drains final provider responses after endpoint close.	Go-test case uses an in-process Riva gRPC service; no cloud credentials or external service are required.
```

If focused transport behavior is split across several test functions, add the
named aggregate test as subtests calling shared scenario helpers. Do not add
one manifest row per low-level assertion.

- [ ] **Step 4: Run required validation**

```sh
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransportMatchesReference$' -count=1
scripts/parity-validate.sh --case nvidia-stt-reference-native-streaming-transport
go test ./adapter/nvidia -count=1
scripts/parity-gate.sh --case nvidia-stt-reference-native-streaming-transport
go build ./...
```

Expected: every command exits zero. The targeted parity gate must run because
this change adds generated protocol code, concurrency, and a provider boundary.

- [ ] **Step 5: Inspect final diff and dead code**

```sh
git diff --check
git status --short
rg -n 'nvidia riva stt streaming is not implemented' adapter/nvidia
```

Expected: no streaming-path placeholder remains; only intentional historical
text is absent. Confirm changes outside `adapter/nvidia/**` are limited to the
single TSV row and Superpowers documents.

- [ ] **Step 6: Commit, rebase, and revalidate**

```sh
git add adapter/nvidia/nvidia_test.go scripts/parity-fixtures/test-cases.tsv
git commit -m "test(nvidia): prove native riva stt parity"
git fetch origin main
git rebase origin/main
go test ./adapter/nvidia -run '^TestNvidiaSTTNativeStreamingTransportMatchesReference$' -count=1
scripts/parity-validate.sh --case nvidia-stt-reference-native-streaming-transport
git status --short --branch
```

Expected: focused test and parity case pass after rebase; worktree is clean.

---

## Final Review Checklist

- [ ] Config precedes every audio request.
- [ ] PCM order and normalized mono/rate are preserved.
- [ ] Interim/final events drain before EOF.
- [ ] Hosted and local metadata/credentials match approved design.
- [ ] Flush tail reaches Riva before close-send.
- [ ] Cancellation and close unblock all work.
- [ ] Offline recognition remains unsupported.
- [ ] No external service is required by tests.
- [ ] Generated provenance and licenses are present.
- [ ] TSV remains valid and contains one aggregate transport case.
- [ ] Every commit is rebased and freshly validated.
