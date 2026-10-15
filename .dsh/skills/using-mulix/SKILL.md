---
name: using-mulix
description: Use at the start of any session in a mulix-managed project, and whenever about to write or edit a file, to check which phase gates apply right now.
---

# Using mulix

mulix enforces a seven-phase workflow with two layers of enforcement, not
just convention:

```
specify → clarify → design → tasks → build → verify → archive
```

| Phase   | Skill           | Embedded skill it runs                          | Output |
|---------|-----------------|-------------------------------------------------|--------|
| specify | `mulix-specify` | —                                               | `docs/specs/<change>/spec.md` |
| clarify | `mulix-clarify` | —                                               | spec.md `## Clarifications` |
| design  | `mulix-design`  | `brainstorming`                                 | approved design; `.mulix/.runtime/<change>/specs/*-design.md` (architectural) |
| tasks   | `mulix-tasks`   | `writing-plans`                                 | `docs/changes/<change>/tasks.md` (`## Task N` plan) |
| build   | `mulix-build`   | `test-driven-development` + `subagent-driven-development` or `executing-plans` | code, tests; `.mulix/.runtime/<change>/sdd/tasks/` (ledger, briefs, reports) |
| verify  | `mulix-verify`  | `verification-before-completion`                | `docs/changes/<change>/report.md` |
| archive | `mulix-archive` | `finishing-a-development-branch`                | merged / PR / kept |

1. On Claude Code, a **PreToolUse hook** blocks `Write`/`Edit` calls
   that don't match the current phase's whitelist. If a write gets
   blocked, the message says why and what to do next — read it, don't
   retry the same call. Hosts without the hook (such as DeepSeek
   Harness) have no automatic layer here: the whitelist still binds you.
2. **Guard checks** (`mulix guard <event>`, run automatically inside
   `mulix state transition <event>`) verify concrete evidence (an
   artifact exists, the design approval is recorded, every task has a
   ledger completion line and RED/GREEN evidence) before the phase
   advances.

## Before doing anything else

Run `mulix state show` to see the active change's phase and the events
legal from it. Don't assume the phase from conversation history — state
lives on disk in `.mulix/.runtime/<change>/state.yaml`, not in your
context.

Then invoke the phase's skill from the table before taking any action
in that phase. Each applies only while the active change is in its
phase.

## The embedded superpowers skills

`mulix init` installs every superpowers skill as a project skill in
`.dsh/skills/` (unprefixed: `brainstorming`, not
`superpowers:brainstorming`). They keep their full discipline; mulix
only moves their artifacts under `.mulix/.runtime/` and replaces their
hand-offs with phase gates:

- The phase skill decides when one runs. brainstorming ends at the
  design gate, writing-plans at the tasks gate (no execution handoff),
  the executors at build-complete (the branch is finished in archive).
- Supporting skills apply whenever their trigger fires, in any phase:
  `systematic-debugging` for any bug or unexpected failure,
  `verification-before-completion` before any "done/fixed/passing" claim,
  `receiving-code-review` when review feedback arrives,
  `dispatching-parallel-agents` for independent investigations.
- Outside a mulix change (no active change), they behave as upstream,
  with output under `.mulix/.runtime/_shared/`.

## Starting a new change

`mulix new "<title>"` creates the change's spec directory
(`docs/specs/<change>/`), change directory (`docs/changes/<change>/`),
and runtime directory (`.mulix/.runtime/<change>/`) plus a fresh state in
the specify phase, and makes it the active change. Do this before
anything else for a new piece of work — don't start writing a spec by
hand first.

## Red flags — stop and reconsider

- "I'll just write the code first and backfill the phase later" — this
  is exactly what the phase gates exist to prevent: the hook on Claude
  Code, the guards and this discipline on every host. Routing around
  them is a bug, not a shortcut.
- "brainstorming says to invoke writing-plans / the plan says to execute
  now" — under mulix the next step is always the phase gate. Transition,
  then the next phase's skill takes over.
- "The guard check is probably fine to skip with --force" — `--force`
  exists for genuine recovery situations, not for impatience. Read the
  failed check's remediation hint first.
- "I'll infer the user approved this from them not objecting" — the
  design approval, the execution method, and archival all require an
  *explicit* answer recorded in state, not inferred from silence.
