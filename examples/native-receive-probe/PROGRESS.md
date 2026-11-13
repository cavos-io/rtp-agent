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
