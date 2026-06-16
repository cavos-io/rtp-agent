---
id: virtual-avatar
title: Virtual avatar
---

# Virtual avatar

Status: **partial**.

Use a virtual avatar provider when the agent should drive avatar state alongside room audio/video behavior.

The core boundary is `agent.AvatarProvider`:

- `Start(ctx)` starts the provider.
- `UpdateState(state)` moves between avatar states such as idle and speaking.

Avatar start information is carried through context with `ContextWithAvatarStartInfo` and `AvatarStartInfoFromContext`, including LiveKit URL, token, room name, and agent identity.

## Provider rule

Only adapters with an `avatar.go` capability file should be documented as avatar providers. Check the provider page and adapter tests before relying on provider-specific startup behavior.

Evidence:

- `core/agent/avatar.go`
- `adapter/*/avatar.go`
- avatar adapter tests under `adapter/*/*_test.go`
