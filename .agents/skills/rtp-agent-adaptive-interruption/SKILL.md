---
name: rtp-agent-adaptive-interruption
description: Use when adding, implementing, or extending an adaptive interruption detector adapter in RTP Agent, including acoustic barge-in and backchannel classification providers.
---

# Adaptive interruption adapters

Read the [implementation guide](../../../docs/features/adaptive-interruption-adapters.md)
before editing. It contains the current contract, lifecycle and verdict semantics,
LiveKit example, verification checklist, and copyable implementation prompt.

Use the guide with the current source; core interfaces and architecture rules
take precedence over examples copied from older implementations.

1. Establish the provider package, authoritative model/protocol source and
   revision, audio requirements, inference policy, and request-count definition.
2. Verify `agent.InterruptionDetector` and `agent.InterruptionStream` in
   `core/agent/adaptive_interruption.go`. Keep provider protocol in the adapter;
   core retains playback, transcript, endpointing, and fallback ownership.
3. Check model compatibility. Transcript-driven inference requires a scoped core
   extension because the current stream accepts audio and boundaries only.
   A standalone detector package also needs an architecture-contract review;
   `adaptive_interruption.go` alone is not a recognized package capability.
4. Implement the provider adapter and requested composition, using the guide's
   lifecycle and offline test checklist. Preserve exact overlap correlation,
   inconclusive agent-ended outcomes, cancellation, and meaningful error/usage
   reporting.
5. Run relevant focused tests, race checks, and architecture validation. Follow
   DEVELOPMENT for broader gates and explicit reference-parity work. Report the
   behavior, source evidence, checks actually run, and remaining limitations.

Keep detailed protocol and lifecycle guidance in the linked guide rather than
duplicating it in this skill.
