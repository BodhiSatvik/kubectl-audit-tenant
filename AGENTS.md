# audit-tenant: instructions for AI agents

This repo is one project in Satvik Bhasin's Adoption Lab (`../../`). It will be **public**. Read the lab rules in `../../AGENTS.md` once per session.

## Rules for this repo

1. **Clean room.** Use only the published paper, public standards, and new work. Never copy anything from the lab's `../../context/` folder (employer material and unpublished drafts). Run `../../scripts/clean-room-check.sh .` before each release.
2. **Measured numbers only.** Benchmarks come from this code. Never copy the paper's simulated results.
3. **Never delete files** without Satvik's say-so. Move old files to `_archive/`.
4. **Plain writing.** Short sentences, active voice, define terms on first use.
5. Design lives in `DESIGN.md`. Build list lives in `TODOS.md`. gstack keeps its working docs in `~/.gstack/projects/BodhiSatvik-kubectl-audit-tenant/` (its slug for this repo).

## Tools

- gstack: `/office-hours` → `/plan-ceo-review` → `/plan-eng-review` → build → `/review` + `/codex` → `/qa` → `/ship`.
- Matt Pocock's skills: run `/mattpocock-skills:setup-matt-pocock-skills` once in this repo. Then use `grill-with-docs` before design work and `tdd` while you build.
- Go style: load `/modern-go-guidelines:use-modern-go` (JetBrains' modern Go guidelines, installed as a user-scope plugin) before writing or reviewing any Go code in this repo.
- Ponytail (`/ponytail`, the "lazy senior dev" plugin, installed at user scope): a hook loads it into every session at level `full`. Keep `full` while coding here and never use `ultra`: `DESIGN.md` is the ask, do not re-argue it. Its reuse ladder (does it need to exist, already in the repo, standard library, installed dependency, one line, else the minimum) is the same ladder gstack's reviews use. Never cut validation, error handling, security, tests that `DESIGN.md` or `CONSTRAINTS.md` require, or anything Satvik asked for. Mark a deliberate shortcut in code as `// shortcut: <limit>, <when to upgrade>`; this is the repo's only shortcut-comment format (add the gstack decision id in the text when one exists). Run `/ponytail-review` on a task's diff before you commit it. gstack `/review` + `/codex` stay the pre-merge gate. Run `/ponytail-debt` before each release.
- Workflow: `../../docs/workflow/agent-tooling.md`.

## Idea and paper

- Idea brief: `../../ideas/audit-tenant/IDEA.md`
- Fable summary: `../../ideas/audit-tenant/fable/SUMMARY.md`
- Paper brief: see the `paper:` line in the idea brief

## Commands

<!-- fill in: install, test, lint, benchmark -->
