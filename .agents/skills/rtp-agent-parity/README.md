# RTP Agent parity skill

This project skill guides AI coding agents through explicit reference-parity work in RTP Agent.

It is intentionally narrow. Agents should load it when a task explicitly asks for parity or behavior mirroring against LiveKit Agents or plugins, Pipecat or Smart Turn, TEN Framework or TEN VAD, or their repository reference roots. Ordinary adapter and runtime changes do not trigger it.

The skill follows the open Agent Skills `SKILL.md` format and contains no vendor-specific metadata. The project-level parity policy and command reference live in [`DEVELOPMENT.md`](../../../DEVELOPMENT.md).

Current location:

```text
agents/skills/rtp-agent-parity/
```

Move the directory to a skill-discovery location supported by the target agent when enabling it. Common repository locations include `.agents/skills/rtp-agent-parity/` and `.github/skills/rtp-agent-parity/`.
