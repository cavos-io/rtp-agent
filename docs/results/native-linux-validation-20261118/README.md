# Linux native/runtime image validation — 2026-11-18

Pion remains the default. No commits, deployments, repository configuration,
or dependency pins were changed. All runtime checks used Docker networking
`none`; there were no STG calls. Image builds downloaded pinned artifacts and
private dependencies using an existing credential passed as a BuildKit secret.

## Results

| Check | Linux arm64 | Linux amd64 |
| --- | --- | --- |
| Pinned FFI initialization/disposal in Linux | Pass | Pass |
| Native runtime race suite | Pass | Pass |
| Full downstream image, ffmpeg + nativeinprocess | Pass | Pass |
| Final-image executable and FFI shared libraries resolve | Pass | Pass |
| Real FFI smoke as final-image appuser | Pass | Pass |
| Hecttor ONNX library and hold audio present | Pass | Pass |
| Native flag absent from image configuration | Pass | Pass |
| Pion selected when runtime flag unset | Pass | Pass |
| Native selected with explicit runtime flag | Pass | Pass |
| Idle SIGTERM exit without forced kill, both selectors | Exit 0 | Exit 0 |

Docker host is Linux arm64. amd64 checks ran under Docker emulation, not native
amd64 hardware. The smoke opens and disposes FFI without joining a room. Runtime
race tests exercise the existing test suite; they do not prove live media.
Idle worker tests used synthetic localhost credentials and no external network.

## Local images

- `rtp-native-runtime-check:arm64`
  `sha256:f72b662960c41a0b0307af7a54c7521e4daa6b6611dadc9da11c209bef334c4f`
- `rtp-native-runtime-check:amd64`
  `sha256:2513cec09040b8e44b34a9508b175e85d9b690650fa61715178d395147441d86`
- `agent-worker-validation:arm64-native-true`
  `sha256:6b141b0d22486e99f05ce0903023444944152747d0f18071f859e91d3e16e68d`
- `agent-worker-validation:amd64-native-true`
  `sha256:975a0e4e1476a1bff8533359dfcd5afd9a74381764f4e9922656b63b7ea2dd72`

Images remain local and were not pushed. Temporary runtime containers were removed.

## Build inputs and failures retained

The upstream runtime Dockerfile was built as written. The downstream checkout
has a sibling go.mod replacement, so a temporary Dockerfile supplied a filtered
upstream named context at `/rtp-agent`. The downstream image otherwise used the
repository Dockerfile and `ENABLE_NATIVE=true`. Source snapshots included current
uncommitted implementation. The production dependency still requires a release
containing these upstream changes before a standalone release build.

The first external-Dockerfile invocation did not apply the intended ignore rules.
The local dotenv entered that temporary build context and enabled native for the
untagged `download-files` command, which failed before FFI installation. A diagnostic
launcher confirmed only dotenv presence and the startup error; no dotenv values
were printed. Rebuilding with an explicit source-only snapshot resolved it. The
eight identified failed build layers and their two source-context cache records
were removed with exact-ID filters; unrelated cache records were retained.

An immediate invalid-command startup check did not observe diagnostics because
the logger buffers output (its flush interval is two seconds). The final checks
instead ran the real worker for five seconds before SIGTERM and verified selection
markers and exit 0. The initial arm64 assertion failure is retained in
`image-arm64-initial-checks.log`; final evidence is in the selection logs.

`inputs.json` records unchanged build-definition, installer and downstream go.mod
hashes. Logs alongside this report contain the final successful build and runtime
checks. No source fix was necessary during this validation.

## Remaining promotion gates

- Real native downstream web/SIP/transfer/recording/agent.events acceptance.
- Active-call drain, concurrent media soak, reconnect behavior and failure recovery.
- Native amd64 hardware validation if required by the deployment acceptance policy.
- Release/pin the validated upstream revision, then validate the release build
  without the temporary sibling-source context.

These results do not authorize a default switch or deployment.
