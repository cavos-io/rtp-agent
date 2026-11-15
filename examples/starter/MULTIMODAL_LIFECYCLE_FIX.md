# Multimodal lifecycle race follow-up

The previous native experiment exposed races in the unrelated realtime assistant
lifecycle. A stopped run could still read ma.session after a restart replaced it.
Close detached the provider before provider cleanup completed, allowing a second
Close (including AgentSession.Stop) to return while cleanup was still active.
The old run also called ma.Close on cancellation, which could target a replacement
session rather than its own provider.

The fix gives each receive run a cancel function, completion channel and provider
close-once ownership. Start, Close and model replacement serialize lifecycle
transitions. Close cancels the run, closes its provider and waits for receive-loop
completion without holding the state mutex. Restart/replacement waits before
changing shared state. Audio processing uses that run's provider. Event handling
snapshots shared session/activity/publish state under the owning mutexes.

Fatal realtime error delivery is dispatched with captured session/model values
outside the receive loop: error handling can synchronously stop AgentSession,
which must be able to join the loop without joining itself. Direct event handling
retains its existing synchronous behavior. Blocking provider Close calls and
callbacks must still return; this change does not forcibly terminate Go code.

Regression coverage:

- Close waits for a blocked receive loop.
- Concurrent Close waits for provider cleanup and closes it once.
- Twenty repeated Start/Close cycles do not overlap old/new session ownership.
- Model replacement joins the old loop; old cancellation cannot close the new provider.
- Parent cancellation and explicit Close share one completed cleanup.
- A fatal realtime event closes AgentSession without a self-join deadlock.

The first three regressions failed before the fix, including a reproduced
Start/run data race. A broader multimodal race run also found test-fixture races:
four tests read mutable fake-provider/playback fields before their asynchronous
work finished. They now await existing speech completion or capture the existing
playback signal before launching work. Assertions are preserved.

Validation: the complete TestMultimodal* selection plus the original
TestAgentSessionStopClosesMultimodalRealtimeSession passed ten repetitions under
-race. Raw red/green and broader-suite evidence is preserved under
../native-receive-probe/results/multimodal-lifecycle/. All2930 earlier artifact
hashes remain unchanged. Native five-session results were not rerun or rescored.
No production transport, codec, STT/TTS adapter or playout policy changed.

Reproduce:

```sh
go test -race ./core/agent -run '^Test(Multimodal|AgentSessionStopClosesMultimodalRealtimeSession)' -count=10
go test ./core/agent ./examples/starter -count=1
go vet ./core/agent
```

The core suite requires localhost permissions for httptest fixtures. This is
focused multimodal race coverage, not a claim that every repository race test
has passed.
