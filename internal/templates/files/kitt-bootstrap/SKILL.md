---
name: kitt-bootstrap
description: First-time setup of this project's AI files (AGENTS.md, RULES.md, project memory). Use when AGENTS.md contains the kitt:bootstrap marker.
---

# kitt bootstrap

This project was just initialised by kitt. Fill in its AI files from the actual code, then remove the setup marker.

1. Explore the repository: README, build files, entry points, tests, CI.
2. Fill in the Project, Commands and Architecture sections of `AGENTS.md`. Be factual and short: only write what you checked in the code. Leave the other sections as they are.
3. If `RULES.md` only contains placeholders, propose rules inferred from the code (formatting, naming, error handling, git conventions) and mark each one `(proposed)` so the team can validate it.
4. Write 1 to 3 documents in `.agents/docs/` on the topics a newcomer needs first, following `.agents/docs/README.md`. Set `verified` to the output of `git rev-parse HEAD`.
5. Remove the `<!-- kitt:bootstrap -->` line and the setup note right under it from `AGENTS.md`.
6. Run `kitt install` to refresh the docs index in `AGENTS.md`.
7. Summarise what you wrote and what the team should review.

Do not change any code and do not commit.
