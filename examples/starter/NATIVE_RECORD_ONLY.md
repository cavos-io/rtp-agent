# Native record-only session mute retention

The in-process transport now retains primary and auxiliary recordings when downstream calls `AgentSession.SetInputAudioMuted(true)` for record-only mode. Agent input remains muted. This matches the default transport's recorder-before-agent processing order.

The fix is confined to `examples/internal/nativeroom/room.go`. Primary in-process recording happens before the session mute check; auxiliary recording uses RecorderIO directly. `RecordRemoteParticipants` and `DisableAudioInput` still control auxiliary setup. Helper-mode mute/recording behavior and default Pion implementation are unchanged.

## Regression evidence

`TestNativeRecordOnlyRetention` exercises the actual callbacks configured by Room.Start using a connection fixture, without a native library, network, or provider. It sends a tone during session mute, finalizes the real recorder and decodes the resulting Ogg/Opus audio to check that the recording contains signal. Separate subtests cover primary and auxiliary recording. A counting assistant verifies muted frames are not delivered, unmute resumes primary delivery, and auxiliary audio never enters agent input. A helper subtest verifies its original muted-recording behavior remains unchanged.

Before the fix, both native recording subtests failed with zero decoded energy; the helper case passed. After the fix all three passed with the Go race detector. Evidence is retained under `examples/native-receive-probe/results/native-record-only-20261117/`, including `red.log` and `green.log`. The regression uses the default Ogg writer (`!ffmpeg`); the routing fix is shared by recording formats, but an FFmpeg-specific retention test was not run.

This is recording/callback regression proof, not a live SIP transfer or downstream integration run. The existing ordinary-recording cloud/local results and failed attempts are preserved. Public native API, downstream room events, reliable test evidence and transfer/hold migration remain separate blockers.
