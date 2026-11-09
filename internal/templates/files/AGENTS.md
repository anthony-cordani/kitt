# AGENTS.md

<!-- kitt:bootstrap -->
> **Setup pending.** kitt initialised this project but the `kitt-bootstrap` skill has not run yet. Run it only when the user asks you to set up the project (if your tool has no skills, read `.agents/skills/kitt-bootstrap/SKILL.md`). For any other request, ignore this note and do the task.

## Project

_What this project does, for whom, in a few lines._

## Commands

_Build, test, lint and run commands._

## Architecture

_Main components and how they talk to each other._

## Conventions

The team rules are in [RULES.md](RULES.md). Read them before changing code.

## Project memory

Before reading code on a specific topic, check whether a document below already explains it. When you learn something non-obvious about this project, write it down in `.agents/docs/` (format: [.agents/docs/README.md](.agents/docs/README.md)).

<!-- kitt:docs:start -->
<!-- kitt:docs:end -->

<!-- kitt:setup:start -->
## Skills

This project's AI skills are managed by [kitt](https://github.com/anthony-cordani/kitt): pinned in `kitt.toml`, installed in `.agents/skills/`, which git does not track. A fresh clone has none of them.

If `kitt.toml` pins skills and `.agents/skills/` is missing, set the project up before your task:

1. Run `kitt --version`. If kitt is not installed, tell the developer and install it only with their agreement, using the [install instructions](https://github.com/anthony-cordani/kitt#install) for their system.
2. Run `kitt init`. On a project that already has its files, it only adds what is missing, installs the git hooks and restores the pinned skills.

If `kitt init` fails, report the error to the developer and carry on with the task.
<!-- kitt:setup:end -->
