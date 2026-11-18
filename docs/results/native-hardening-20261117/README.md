# Native transport hardening — 2026-11-17

Scope: supported options, embedded CLI shutdown, explicit downstream transport
selection logs, and opt-in Linux packaging. Pion remains the default; no commits,
deployments, or STG calls were made.

## Changes

- Native RoomOptions rejects every unsupported explicit setting before mutating
  selection or acquiring media. Pre-connect remains explicitly disabled; no new
  audio features or supervisor transport migration.
- `cli.RunAppWithOptionsError` returns command errors after server closure.
  Downstream uses it so its existing defer drains the server and disposes FFI
  only after successful closure. Legacy standalone CLI retains exit status 1.
- Downstream startup and job logs identify `pion` or `native_inprocess` and
  compiled native support. These selection logs do not assert successful media.
- The Linux installer verifies release archive checksums for amd64/arm64.
  Downstream `ENABLE_NATIVE=true` builds ffmpeg/native support and installs FFI;
  the runtime flag remains unset. A separate Dockerfile tests FFI startup/shutdown
  and the runtime race suite without STG.

## Verification

Passed:

- Upstream `scripts/go-test-all.sh` (see all-tests.log).
- Native room/runtime and CLI race suites (see race.log).
- Downstream focused app/native/RoomOptions race tests with and without native
  build tag (see downstream-native.log and downstream-default.log).
- Native and default downstream executable builds.
- Architecture checks in both repositories and diff whitespace checks.
- Real pinned FFI initialization/disposal smoke on macOS arm64.
- Linux amd64 and arm64 installer downloads, archive checksum verification,
  extraction, and ELF architecture identification.
- Installer rejection of corrupt archives and unsupported architectures.

Initial native options tests reproduced silently accepted unsupported fields.
The initial CLI test could not compile before the new API existed. The first
race run encountered a sandbox socket denial and an existing console test racing
on bytes.Buffer. That fixture now synchronizes its reads/writes; final race tests
pass. The installer initially used checksum command flags unsupported by the
host's BSD utility; portable digest comparison passed for both real archives.

## Remaining evidence gaps

The local Docker daemon is stopped/unavailable. Neither the Linux runtime image
nor the complete downstream Linux image was executed. macOS library smoke is
not Linux certification. The downstream production dependency must include this
upstream revision; its existing sibling replacement remains untouched. No
production dependency pin or default selector was changed.

Real native downstream calls, concurrent media soak, SIGTERM under active calls,
reconnect acceptance, and Linux image execution remain required before promotion.

Extracted library SHA-256:

- amd64: `05a58fc83b3515cd4e984a66c69c22e0aad09eadb6e972edde626488dafbf5ad`
- arm64: `8550643d4d19d5426965a4743c1626dde68c717ee44bc82d45d220a5a3162e9a`
