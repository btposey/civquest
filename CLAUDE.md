# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:1105d646 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->


## Build & Test

### Go Orchestrator (Hermetic Default Suite)
```bash
cd orchestrator-go && go test -count=1 ./...
```
- Runs 100% offline/hermetic (live-LLM and live-Minecraft integration tests are gated behind build tags).

### Go Race Detector (System 1 & Agent Runtime)
```bash
cd orchestrator-go && go test -race -count=1 ./internal/agent/... ./internal/system1/...
```

### Live-LLM Integration Suite (Requires Running OpenAI-Compatible Endpoint)
```bash
cd orchestrator-go && go test -tags livellm -v ./test/integration -run TestLiveLLM
```

### Architecture Size & Complexity Baseline Check
```bash
./scripts/check-arch-budgets.sh          # Report mode (always exits 0)
./scripts/check-arch-budgets.sh --strict # Enforce recorded baseline ceilings
```

### Capability Coverage Matrix
```bash
./scripts/check-capability-coverage.sh           # Report coverage % (always exits 0)
./scripts/check-capability-coverage.sh --strict  # Fail on any GAP/partial row or missing referenced test
./scripts/check-capability-coverage.sh --deep    # Also verify Go tests via `go test -list`
```
- Matrix: `doc/capability-coverage.yaml` (source of truth) and `doc/capability-coverage.md` (generated via `--emit-md`).
- **Definition of Done:** a new agent feature or capability is not done until it has a row in the matrix and a smoke test (integration-sim, replay, GameTest or live level; a unit test alone does not count).

### NeoForge Server Mod
```bash
./gradlew :mod-neoforge:build
```

### Live Agent Smoke (before handing a build to a playtest)
```bash
./scripts/live-agent-smoke.sh               # 6 real agents, 5 min, throwaway server and XDG sandbox
```
- Fails if an agent brain stalls or goes silent, or a chat gets no reply. Simulation fixtures can differ from the live server (recipes, terrain), so run this as well.

## Architecture Overview

_Add a brief overview of your project architecture_

## Conventions & Patterns

_Add your project-specific conventions here_
