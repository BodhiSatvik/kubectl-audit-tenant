# Agent instructions

All agent instructions for this repo are in `CLAUDE.md`. Read it first. The rules there apply to every agent (Claude, Codex, Gemini, Cursor, and others).

Short version:
- Read `CONSTRAINTS.md` before writing code. `make check` must pass before a task is called done.
- Clean room: never copy anything from the lab's `../../context/` folder.
- Never delete files. Move old files to `_archive/`.
- Lab rules: `../../AGENTS.md`.
- Codex: gstack skills are named `gstack-<skill>` here (for example `gstack-review`).
