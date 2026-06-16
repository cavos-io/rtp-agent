---
id: deferred
title: Deferred documentation
---

# Deferred documentation

Use this page to see which LiveKit-shaped docs are intentionally not expanded yet.

These areas are deferred because the repository does not contain enough source, tests, examples, or deployment evidence to document them as supported behavior:

- Provider-specific pages for every adapter. The old pages used unsupported conceptual APIs. They have been replaced by a capability matrix until each provider has source-backed examples and tests worth documenting.
- Hosted product pages for Agent Builder, Agent Console, and Agent Embed Widget. The Go repository does not implement those LiveKit hosted products.
- Full frontend, telephony provisioning, and WebRTC transport guides. The repository has transport and worker primitives, but the docs should not invent a product workflow before source-backed examples exist.
- Dedicated RAG, supervisor, and workflow frameworks. Current source exposes tools, agent/session state, and worker lifecycle primitives; it does not expose complete named frameworks for those patterns.
- Deployment recipes for specific platforms. The source exposes worker and CLI primitives, but platform-specific production recipes need verified integration details.
- Full LiveKit Agents compatibility statements. The repository uses parity tests, but not every LiveKit Agents feature is proven equivalent.
- Provider model catalogs. Model names change outside this repository; docs should only include model names that appear in source examples or tests.

Add these pages only when the source, tests, examples, or parity manifest provide stable evidence.
