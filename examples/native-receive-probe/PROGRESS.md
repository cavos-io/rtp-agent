# Experiment ledger

- Approved: current checkout, new branch from main; isolated native FFI prototype only.
- Branch: experiment/native-rtc-pcm-receive; base ea6c369c0df0aebf36de6193f2538807032517e6.
- Source, schema and artifact: rust-sdks 5a656c4bc4bc5c4f7fad17ab69044db3b487dfeb, FFI 0.12.80, Python RTC 1.1.21.
- Native macOS ARM64 archive SHA-256 verified against release metadata.
- Existing tools available; no system packages installed. Python dependencies installed only in local .venv.
- Wrapper tests: observed undefined-symbol failure, then passed after implementation.
- Ruling: match Python 1.1.21's explicit queue_size_frames=0 for native stream. The SDK uses an unbounded native queue; wrapper callbacks remain bounded and overflow invalidates the run. This measures reference settings, not a production queue recommendation.
- Ruling: generate upstream protobuf code into ignored internal/ffipb rather than commit the entire SDK schema/API. Setup regenerates from the pinned archive.
- Reused only an upstream speech fixture, not prior custom Go receive/playout experiments.
- Git status defaults hide untracked files. Explicit inventory found unrelated untracked docs and session_report.json; preserve and exclude them.
- Local socket binding requires sandbox escalation; no production room or paid provider is used.
- Pilot failures diagnosed from native errors/source: connect requires ReadyForRoomEventRequest; AudioStream watches track-handle disposal. Fixed handshake and retained track through stream shutdown. Earlier failed pilots remain in ignored results, never used as receive-engine evidence.
- Fixture is 10.8 seconds; looped using FFmpeg to make a deterministic 20-second content source.
- Native and Python stream produce frames before publisher RTP begins. Startup now explicitly means subscription-to-first-frame; content latency measured independently by markers.
- Corrected pilot (pilot-3): all three scenarios delivered 29.000s in the common 29s window and 8.000s in the interior 8s silence window, in both runtimes. All 20 ordered markers detected. DTX silence sent 33 packets vs 500 continuously; loss injected 25 missing packets. These are pilot measurements, separate from final ten-run results.
- Focused Go tests, Go race tests, go vet, Python analyzer tests and Python syntax checks passed.
- Checkpoint committed: 778dcc7ce. Commit hooks passed architecture and test-integrity checks. Root targeted-test hook cannot discover a nested module; the prototype's tests were run directly instead.
- Connection-refused plus three connected cancellation cycles passed with normal and race-instrumented binaries.
- Manual review found two harness defects: protobuf scalar maps could panic during recursive ownership traversal; cleanup could stop after one hung process. Added failing regressions, then fixes. Added an explicit error on missing disconnect callback.
- Full 30-run experiment keeps the original checkpoint executable unchanged (SHA-256 6a297e3c59b54e5069b73820fc66c1fef816dda43c0ed4f132a2c1b5e8d1656a). Review fixes do not change receive/playout behavior; they will receive a separate live regression run with a distinct binary.
- No subagent review tool is available in this environment; review is local source/diff inspection, not independent review.
- Full-run dtx-03 is a real failed content check, retained: both runtimes produced marker 8 twice. Publisher packet 402 was 453.805ms late; Python delivery had a 476.084ms gap, while Go native callback cadence stayed below 56ms. This demonstrates why duration matching alone is insufficient. The cause of host scheduling disturbance is not established.
- Reviewed race-instrumented binary passed refused connection, cancellation during a stalled WebSocket connection, and three connected shutdown cycles.
- Full-run scheduling disturbance is retained as a failed gate, not reclassified as DTX or packet loss. No acceptance tolerance was relaxed.
- Added packet-evidence checks to the analyzer so continuous silence, DTX omission, and the 25-packet loss interval must actually be observed.
- Native opaque heap/thread leak freedom remains unproven. Available evidence covers disconnect/dispose completion and child-process exit, including race-instrumented lifecycle checks.
- Started the unfiltered repository parity gate, then stopped it after >10 minutes still enumerating 9,434 unrelated manifest cases. This isolated nested module has no manifest row and makes no production parity change. No full-repository parity pass is claimed; focused native/Python experiments and direct nested-module tests are the evidence for this task.
- Final 30-run results: continuous PCM in all scenarios; one shared DTX marker duplication and a continuous-silence onset p95 difference of 50.32ms against a 50ms gate. Both failures retained.
- Reviewed race build: all three additional scenario checks passed; Go callback-to-consumer p95 0.49–0.54ms, max 1.41ms; explicit retained handle releases succeeded.
- Resource limitation: immediate Go goroutine count after native dispose was 6–8 vs baseline 3. No leak diagnosis is made from count alone, and the no-outstanding-goroutines acceptance condition is not established. All subprocesses exited; no race report.

- Same-process follow-up: 100 room cycles with one FFI initialization, and 100 with per-cycle initialize/dispose, completed under the Go race detector with PCM every cycle, successful release/disconnect, empty settled queues and unchanged Go-owned goroutine identities.
- Retained initial lifecycle failure: the fixed-20-warmup foreign-context plateau criterion failed at cycle 58 (6 -> 7). That attempt lacked native thread IDs and was not relabeled as a pass.
- Opt-in macOS thread snapshots directly observed a new Go callback context on a native thread already present after the first cycle. Seven foreign contexts remained after final disposal, matching Go pthread callback retention and the pinned FFI's process-global runtimes. No per-cycle Go-owned goroutine growth observed; native heap leak freedom remains unproven.
- Prototype lifecycle separates room cleanup from process FFI disposal and avoids repeated dlopen reference increments. Production code and original content/latency failures remain unchanged.
- Test-integrity hook rejected t.Skip for the opt-in integration test. Moved it behind the nativecycles build tag, retained the default classifier unit test, and reran focused race tests, tagged vet and five live room cycles successfully. No hook bypass.

- Controlled follow-up preserved all 210 original artifact checksums plus acceptance/summary/measurement hashes. No original score changed.
- Added opt-in publisher stage/source capture, process/event-loop heartbeats, Python FFI-to-iterator timing, and native GetStats sampling. No receive timing policy changes.
- Ten continuous-silence and ten DTX baseline trials passed original content/latency gates; six matched uninjected stats controls also passed. Host was shared and only 9/20 baseline plus 3/6 controls met the <=20ms scheduling attribution threshold; all trials retained.
- Three 450ms publisher stalls before DTX packet 402 reproduced split marker 8 in both native receivers. Native concealment/buffer counters establish real playout disruption; detector duplicate IDs alone do not prove complete speech replay.
- Three stalls before continuous packet 566 reproduced buffer buildup and elevated absolute onset latency, but not the original Go-over-Python relative-latency failure. Original host/blocking trigger and per-instance latency differential remain unresolved.
- Focused Go race tests, vet, seven Python tests and syntax checks passed. Original content and latency failures remain unchanged; no production transport change or original-incident fix claimed.

- Synchronized follow-up: 23 paired local trials (pilot, two controls, ten baseline, ten publisher stalls); original relative-latency gates passed, original historical failure not reproduced.
- Socket traces localize new variation after packet delivery: stall08 marker-arrival skew 0.152ms, native buffer difference 74.5ms. Frame position contributes to raw onset differences; adjusted estimates do not replace acceptance metrics.
- Retained stall00 content failure: both native tracks subscribed after publishing began, missing packets 0–10 and marker 0. Full trace attribution remains failed. Shared stall04 onset latency 433.728/439.917ms also retained.
- All 774 prior artifact checksums verified unchanged. No production transport or custom playout policy changes. Detailed evidence, limits and reproducible commands: ARRIVAL.md and ARRIVAL-SUMMARY.json.

- Subscription barrier follow-up: runner waits for both native subscribed events and exclusively creates a per-run release file; publisher waits after track publication, with cancellation and a 20s bound. No media or receive timing policy changes.
- Ten baseline and ten stall reruns passed unchanged per-run and relative-latency gates. Every trace contains packet0 and all 1500 packets; both subscriptions precede source start in all 20 runs. All 20 ordered markers retained by both runtimes.
- Second onset p95: baseline Go79.367/Python81.247ms; stall Go305.230/Python304.176ms. Elevated absolute stall latency remains; historical failures are unchanged.
- Verified all 2523 preserved artifact hashes. Go race tests/vet, 12 Python tests and syntax checks passed. Details: BARRIER.md and BARRIER-SUMMARY.json.

- Recovery follow-up analyzes the existing 20 barrier captures; no new media or receive code. Supplemental recovery definitions frozen before scoring, original acceptance gates unchanged.
- Native buffer recovery (pre-stall median +20ms for two seconds) began at median9.558s Go (10/10 confirmed), median9.129s Python (8/10 confirmed); Python02/03 remain unconfirmed at about18.2s observation end. Target-delay recovery usually preceded buffer recovery.
- PCM marker recovery confirmed in10/10 Go and9/10 Python trials; no markers exist during silence, so earliest content observation is about8.23s after the stall. All original content/order gates remain passed.
- Corrected supplemental counter-roundoff comparison with a failing regression; initial sidecars retained, corrected outputs use v2. Verified2899 prior hashes and identical original acceptance summaries. Report: RECOVERY.md.

- Approved starter integration: opt-in Go helper process and hidden receive-only participant; Pion continues worker/output ownership. Exact caller/audio-track selection, versioned PCM pipe, bounded backlog/error handling and process cleanup. Default StartSession path unchanged.
- Provider-free local starter tests passed under race instrumentation for default input, native input and selected-track unpublish shutdown. Native mode left Pion subscriptions disabled. PCM tone energy reached the actual session input hook.
- lk2.19.4 CLI/source verified: echo track publishes before dispatch; no startup/greeting dependency cycle. Paid provider load test not run. Commands: ../starter/NATIVE_RECEIVE.md.
