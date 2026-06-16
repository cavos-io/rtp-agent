---
id: overview
title: Prebuilt components, tasks, and tools
---

# Prebuilt components, tasks, and tools

Status: **partial**.

Use prebuilt packages when they match the source-backed task you need; do not assume a complete LiveKit prebuilt catalog exists.

The repository has several kinds of reusable pieces:

- beta tools such as session end and DTMF helpers
- beta workflow tasks for structured collection and warm transfer
- provider adapters for model capabilities
- tokenizers and VAD adapters
- realtime and avatar adapters where capability files exist

Most prebuilt pieces are regular Go packages. Import and configure the package directly, or select it through `app.AppConfig` when the app layer supports that provider/task.

Because several packages live under `core/beta`, check the package tests before using them as stable product behavior.

Evidence:

- `core/beta/tools`
- `core/beta/workflows`
- `adapter/*`
