# Synchronized receive-arrival investigation

Scope: isolated probe only. Preserve original and controlled-run artifacts and
all acceptance gates. Same pinned FFI 0.12.80 / Python RTC 1.1.21, 24kHz/50ms PCM.
No native media-engine replacement, receive policy, or production changes.

Use a macOS process-local receive-syscall tracer because tcpdump exists but BPF
devices are root-only. Observe RTP/SRTP fixed headers after successful UDP socket
reads from the owned server's ports 19000–19100. Record no payload or credentials.
Verify trace SSRC against native inbound stats and map complete media sequence/
timestamp progressions to the publisher. Fail attribution if tracing is absent,
truncated, ambiguous, or if the connection uses an untraced transport.
This boundary is userspace socket delivery, not kernel ingress or NetEq admission.

Sample native track stats on a common absolute CLOCK_MONOTONIC 100ms grid.
Record slot, intended/request/response times and the native report timestamp.
Calibrate realtime-to-monotonic using paired clock readings before/after each
receiver; invalidate timestamp-age attribution if offset moves >1ms. Treat a
paired stats sample as synchronized only if request skew <=10ms, each response
latency <=20ms, and producer timestamps differ <=20ms. Report violations rather
than dropping entire failed runs or assuming scheduling was quiet.

First validate tracing and sample synchronization in one live pilot. Then run
10 continuous-silence baseline and 10 matching publisher-stall trials (450ms at
packet 566), alternating receiver launch order in each group. Run two no-trace
controls using the same synchronized stats. Use fresh output directories and
no concurrent builds/analyses during measured batches. Keep all results.

Compare: publisher -> each actual native UDP socket, per-packet arrival skew,
sequence/timestamp gaps, synchronized jitter-buffer/concealment/stretch counters,
PCM callback/consumer onset and frame position. Original gates remain: ordered
markers, sustained duration, and Go p95 <= Python p95 +50ms. Also flag any paired
onset difference >50ms for detailed attribution; it is a diagnostic, not a new
replacement gate. Clocked stats are observation timers and never drive playout.

Stop after reproducing and localizing a differential or documenting the bounded
non-reproduction and remaining observability limits. Do not infer old packet
arrival histories from new runs or classify every latency difference as a bug.
