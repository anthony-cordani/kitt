<p align="center">
  <img src="assets/header.svg" alt="kitt" width="720">
</p>

Install, pin and publish AI agent skills per project, straight from git.

kitt gives every project the same AI setup on every machine: an `AGENTS.md` the assistants read first, team rules, a project memory, and a set of [Agent Skills](https://agentskills.io) pinned to exact versions in a versioned `kitt.toml`. Skills stay in your own git repositories: kitt hosts nothing, it resolves and installs.

- **Pinned**: `kitt.toml` records the version, the commit and a content hash of every skill. A clone gets exactly the same skills.
- **Per project or per user**: project skills only load in that project (`kitt install`), user skills load everywhere (`kitt install -g`).
- **Every assistant**: skills live in `.agents/skills/` (read by Codex, Copilot and OpenCode); kitt links them into `.claude/skills/` for Claude Code.
- **Git only**: kitt runs your `git`, with your credentials. No token, no account, no telemetry.

## Install

```sh
brew install anthony-cordani/tap/kitt          # macOS, Linux
scoop bucket add kitt https://github.com/anthony-cordani/scoop-bucket
scoop install kitt                              # Windows
winget install AnthonyCordani.kitt              # Windows (winget)
go install github.com/anthony-cordani/kitt/cmd/kitt@latest
```

Binaries for every platform are also attached to each [GitHub release](https://github.com/anthony-cordani/kitt/releases). kitt needs `git` in your `PATH`.

## Quick start

```sh
cd my-project
kitt init                  # asks for your skills repository, writes the project files
kitt install review@^1     # adds a skill, pins it in kitt.toml
git add -A && git commit -m "Set up AI assistants"
```

Then open your AI assistant in the project. On a new `AGENTS.md`, it runs the `kitt-bootstrap` skill first: it fills in `AGENTS.md` from the code, proposes rules in `RULES.md`, writes the first project docs, then removes the setup marker.

A teammate who clones the project runs `kitt install` once; after that, a git hook keeps the skills in sync on every pull and checkout.

## What kitt writes in a project

| Path | Versioned | Role |
|---|---|---|
| `kitt.toml` | yes | Skill sources and pinned skills |
| `AGENTS.md` | yes | What every assistant reads first; ends with the project memory index |
| `RULES.md` | yes | Team rules, for humans and assistants, checked in review |
| `.agents/docs/` | yes | Project memory: one Markdown file per non-obvious topic |
| `.agents/skills/` | no | Installed skills (restored by `kitt install`) |
| `.claude/skills/<name>` | no | Links for Claude Code, one per skill |
| `CLAUDE.md` | no | `@AGENTS.md`, so Claude Code reads the same file |
| `.gitignore` | yes | A block managed by kitt for the unversioned paths |
| `.git/hooks/post-merge`, `post-checkout` | no | Run `kitt install` after a pull or a checkout |

kitt never overwrites a file it did not create. On a project that already has an `AGENTS.md`, `kitt init` only appends the project memory section. Existing git hooks (git-lfs, husky through `core.hooksPath`) are kept: kitt appends its own block.

## Commands

| Command | What it does |
|---|---|
| `kitt init [--source alias=url]…` | Writes the missing project files, installs the git hooks, installs the skills. Without `--source`, asks for the skills repositories and checks that git can reach them. |
| `kitt install` | Restores every skill exactly as pinned in `kitt.toml`. Fast and offline when nothing changed. |
| `kitt install [source/]skill[@constraint]` | Adds a skill and pins it. The source can be omitted when `kitt.toml` has only one. |
| `kitt upgrade [skill] [--major]` | Moves skills to their newest release in the same major version, showing the diff of each skill. `--major` allows a new major version. Exact pins (`1.2.0`) are kept. |
| `kitt list` | Installed skills, and the skills available in each source with their latest release. |
| `kitt doctor` | Reports drift: skill modified locally, missing link, outdated `.gitignore` block or docs index, stale project docs. Exit code 2 when something is wrong. Changes nothing. |
| `kitt release <skill>` | In a skills repository: tags and pushes a new version of a skill (see below). |

`install`, `upgrade`, `list` and `doctor` take `-g` to work on user-level skills instead of the current project.

### Version constraints

| Constraint | Picks |
|---|---|
| *(none)* | The latest release, saved as `^<version>` |
| `^1`, `^1.2`, `^1.2.3` | The latest release with major 1, at least the given version |
| `1.2.3` | Exactly 1.2.3, never upgraded |

A skill without any release is pinned to the latest commit of the default branch.

## `kitt.toml`

```toml
[sources]
team = "git@gitlab.example.com:ai/skills.git"
public = "https://github.com/someone/skills.git"

[skills.review]
source = "team"
version = "^1.4.0"
[skills.review.resolved]          # written by kitt
version = "1.4.0"
commit = "9f1c2e7a…"
hash = "h1:Lr…"
```

You can edit `[sources]` and the `source` / `version` of a skill by hand; `kitt install` resolves and pins entries that have no `resolved` block. User-level skills use the same format in `~/Library/Application Support/kitt/kitt.toml` (macOS), `~/.config/kitt/kitt.toml` (Linux) or `%AppData%\kitt\kitt.toml` (Windows), and install into `~/.agents/skills/`.

## Project memory

`.agents/docs/` holds what the code does not say: a flow, a decision, a trap. `AGENTS.md` tells assistants to check it before exploring the code, and kitt keeps its index up to date.

```markdown
---
title: Drive sync flow
description: How a Drive change reaches the RAG corpus, and where it can fail.
paths:
  - src/sync/**
verified: 3f2a9c1e…
---
```

`kitt doctor` reports a document as stale when the code under `paths` changed after the `verified` commit.

## Skills repositories

Any git repository can be a source if it follows this layout:

```
skills/
  review/
    SKILL.md          # Agent Skills format, version in metadata.version
    references/…
  adr/
    SKILL.md
```

```markdown
---
name: review
description: Review a diff against RULES.md. Use before opening a merge request.
metadata:
  version: "1.4.0"
---
```

- One directory per skill under `skills/`; the directory name is the skill name (lowercase letters, digits and hyphens).
- A release of a skill is a git tag `<name>/v<X.Y.Z>`, e.g. `review/v1.4.0`. Each skill has its own versions.
- Use semantic versioning: major when the assistant behaves differently, minor for an addition, patch for wording.
- Names starting with `kitt-` are reserved.

### Publishing a version

1. Change the skill and bump `metadata.version` in its `SKILL.md`, in a merge request.
2. Once it is merged, from an up-to-date default branch: `kitt release review`.

`kitt release` refuses to publish unless you are on the default branch, level with the remote, with a clean working tree, a version greater than the last release, and changes in the skill since that release. It then creates the tag and pushes it.

### Who can publish (configure your git host)

kitt does not manage permissions: your git host does. Protect two things.

**GitHub**

1. *Settings → Rules → Rulesets → New branch ruleset*: target the default branch, enable *Require a pull request before merging*.
2. *New tag ruleset*: target the pattern `*/v*`, enable *Restrict creations*, *Restrict updates* and *Restrict deletions*, and add the people or teams allowed to publish to the bypass list.

**GitLab**

1. *Settings → Repository → Protected branches*: protect the default branch, *Allowed to push and merge: No one*, *Allowed to merge: Maintainers*.
2. *Settings → Repository → Protected tags*: protect the wildcard `*/v*`, *Allowed to create: Maintainers*.

With protected tags, `kitt release` from someone not allowed fails at the push, and kitt removes the local tag.

## License

[MIT](LICENSE)
