---
id: cli
title: CLI
---

# CLI reference

Status: **implemented**.

Use `interface/cli.RunApp` to expose the repository CLI modes for an `AgentServer`.

The CLI expects a subcommand:

```bash
worker <subcommand>
```

## Modes

| Mode | Purpose |
|---|---|
| `start` | Run the worker process. |
| `dev` | Run development mode, including reload behavior where configured. |
| `connect` | Connect to a room from the CLI path. |
| `console` | Run local console interaction in text or audio mode. |
| `download-files` | Run plugin download hooks. |

## Console usage

The source-backed console usage is:

```bash
worker console [--text|--audio] [--record] [--input-device <device>] [--output-device <device>]
```

## Worker options

CLI parsing applies LiveKit URL, API key, API secret, log level, dev/reload settings, and drain timeout to `worker.AgentServer.Options`.

Evidence:

- `interface/cli/cli.go`
- `interface/cli/cli_test.go`
- `interface/cli/watcher.go`
