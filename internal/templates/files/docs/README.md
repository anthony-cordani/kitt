# Project memory

Each file in this folder explains one topic of the project that is not obvious from the code: a flow, a decision, a trap. AI assistants read the index in `AGENTS.md` before exploring the code, and add a document here when they learn something worth keeping.

## Format

~~~markdown
---
title: Drive sync flow
description: How a Drive change reaches the RAG corpus, and where it can fail.
paths:
  - src/sync/**
verified: 3f2a9c1e0b7d4a6f8e2c5b1a9d0e7f3c6b4a2d8e
---

Content…
~~~

- `description` is the line shown in the `AGENTS.md` index.
- `paths` lists the code the document describes; `kitt doctor` warns when that code changed after `verified`.
- Set `verified` to the output of `git rev-parse HEAD` each time you check the document against the code.
- One topic per file, short, written for the next reader.
