---
id: deferred
title: Deferred documentation
---

# Deferred documentation

The following areas are intentionally not expanded into detailed how-to pages in this version of the docs:

- Provider-specific pages for every adapter. The old pages used unsupported conceptual APIs. They have been replaced by a capability matrix until each provider has source-backed examples and tests worth documenting.
- Deployment recipes for specific platforms. The source exposes worker and CLI primitives, but platform-specific production recipes need verified integration details.
- Full LiveKit Agents compatibility statements. The repository uses parity tests, but not every LiveKit Agents feature is proven equivalent.
- Provider model catalogs. Model names change outside this repository; docs should only include model names that appear in source examples or tests.

These pages can be added when the source, tests, or parity manifest provide stable evidence.

