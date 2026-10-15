---
name: mulix-build
description: Use when the active change is in the build phase, before writing any production code or test for it.
---

# Build phase

tasks.md is the implementation plan; this phase executes it, every task
under `test-driven-development`. Nothing here replaces those skills —
this skill picks the executor, and says where mulix expects their
records.

## 1. Choose the execution method

Read tasks.md once (and the spec it names). Then recommend one method in
one sentence — from how much the tasks depend on each other's
interfaces, how many there are, what a shipped mistake would cost — and
ask the human to choose:

- **subagent-driven** — a fresh implementer subagent per task, a task
  review (spec compliance + quality) after each, a whole-branch review at
  the end. Most thorough; a fresh context per task and per review.
- **inline** — you implement every task in this session, then one fresh
  reviewer checks the whole branch. Cheapest; no independent review until
  the end.

Wait for the answer, then record it:

```
mulix state set execution_method subagent-driven   # or: inline
```

On a return from verify (`verify_failures` > 0), keep the recorded
method unless the human asks to change it.

## 2. Execute

**REQUIRED SUB-SKILL:** `test-driven-development`, loaded before Task 1.
Then, by method:

- subagent-driven → **REQUIRED SUB-SKILL:** `subagent-driven-development`
- inline → **REQUIRED SUB-SKILL:** `executing-plans`

The plan file is `docs/changes/<change>/tasks.md`. Follow the executor
skill exactly, including its setup, ledger, reviews, and finish, with
these mulix specifics:

- **Workspace.** `sdd-workspace` resolves to
  `.mulix/.runtime/<change>/sdd/tasks/` (it reads the active change from
  `.mulix/active`). The ledger (`progress.md`), task briefs, reports, test
  logs, and review packages all live there. Run the scripts from the
  project root.
- **Worktree.** `using-git-worktrees` asks before creating one. If you
  do work in a new worktree, run mulix commands from it and keep
  `.mulix/` there — the hook and guards read state relative to the
  directory the agent session runs in.
- **TDD evidence, both methods.** For every task, the workspace must hold
  `task-N-report.md` with a `RED:` line (the command, the failing output
  before implementation, why the failure was expected) and a `GREEN:` line
  (the command and passing output after). The subagent-driven implementer
  writes this under its report contract ("TDD Evidence"). Under inline,
  write it yourself as you finish each task's GREEN step — `task-done`
  keeps the test log but doesn't write the report.
- **Keep the workspace.** The executor skills' finish keeps it (the
  build-complete guard reads it); never `rm -rf` it or `git clean` it
  away.
- **Stop before finishing the branch.** Where the executor says "use
  finishing-a-development-branch", stop instead: post the "Rulings I made"
  list (and "Deferred minors" under inline), then advance to verify.
  Integrating the branch happens in the archive phase.

## Advancing out of this phase

```
mulix state transition build-complete
```

The guard checks that `execution_method` is recorded, the ledger at
`.mulix/.runtime/<change>/sdd/tasks/progress.md` names tasks.md on its
first line and has a `Task N: complete` line for every `## Task N`
section, and every task has a report with RED and GREEN evidence. A fix
round or a Ruling line is not a completion line.

If a task can't be completed as planned, don't edit tasks.md into shape:
it's the approved plan, and the guard matches the ledger against it.
Rule on it in the ledger as the executor skill says, or, if the plan is
broken past ruling, tell the human.
