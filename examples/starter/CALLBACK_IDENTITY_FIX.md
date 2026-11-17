# Playback callback identity fix — 2026-11-17

`TestRoomIOOffPlaybackStartedUsesCallbackIdentity` failed because the legacy `OffPlaybackStarted` API compared `reflect.ValueOf(fn).Pointer()`: the function's code address. Two closures capturing different channels can share that address, so removing the second removed the first. `OffPlaybackFinished` had the same defect.

Both now use a typed function-value identity pointer, following the existing `AgentSession.Off` compatibility implementation. Handler slices retain the callbacks and the comparison uses GC-visible pointers. This relies on the gc compiler's function-value representation; it is not a portable Go language guarantee. The ID-based session playback-controller unsubscribe API is unchanged. No callback API, transport selection, media processing or helper behavior was redesigned.

Before the fix, the original started-playback test and the added finished-playback regression both failed. The new regression uses a non-inlined closure factory to ensure shared code, checks unregistered closure removal, and removes the same callback twice. The original test and its assertions remain intact.

Validation after the fix:

- Callback removal and panic-isolation regressions: 20 repetitions under `-race`, passed.
- Entire `interface/worker/livekit`, `examples/internal/nativeroom`, and `examples/starter` suites with `-race -tags nativeinprocess`: passed.
- Nested native wrapper suite with `-race`: passed.
- Actual pre-commit hooks (test integrity, architecture, targeted tests): passed using a temporary index containing all 20 caption/checkpoint files; see `results/callback-identity-20261117/hooks-02.log` and `hook-files.txt`. The real staging area was not changed and no commit was made.

The first hook invocation passed the callback suite but failed three helper test functions on their one-second startup waits (four readiness failures including subtests). The unchanged hook rerun passed all packages. `hooks.log` preserves that failure; no timeout, assertion, hook configuration or helper implementation was changed to make it pass. This is an observed transient startup failure, with its cause unproven; do not describe every invocation as successful.

Preservation verified: all 3441 historical result hashes and all 22 live-caption artifact hashes match their manifests. The two original callback regression failures were reproduced before editing; no existing result was overwritten.

Earlier failed logs remain in `results/native-captions-20261117/`. New verification logs are under `examples/native-receive-probe/results/callback-identity-20261117/`. No live audio run was repeated for this listener-removal fix. The user's frontend caption confirmation is recorded in `NATIVE_CAPTIONS.md`, separately from instrumented local evidence.
