---
id: virtual-avatar
title: Virtual avatar
---

# Virtual avatar

Avatar providers implement `agent.AvatarProvider` and are assigned to `agent.Agent.Avatar`.

Source-backed avatar adapters at `v0.0.67` include packages such as `anam`, `bey`, `bithuman`, `did`, `hedra`, `lemonslice`, `liveavatar`, `runway`, `simli`, `tavus`, and `trugen`.

Example direct constructors:

```go
package main

import (
	"github.com/cavos-io/rtp-agent/adapter/hedra"
	"github.com/cavos-io/rtp-agent/adapter/simli"
)

func configureAvatars(apiKey string) {
	avatar := simli.NewSimliAvatar(apiKey)
	hedraAvatar := hedra.NewHedraAvatar(apiKey)
	_, _ = avatar, hedraAvatar
}
```

Use app-level `RTP_AGENT_AVATAR_PROVIDER` only for providers wired in `app/app.go`.
