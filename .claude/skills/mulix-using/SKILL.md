---
name: mulix-using
description: Use at the start of any session in a mulix-managed project, and whenever about to write or edit a file, to check which phase gates apply right now.
---

# Using mulix

mulix provides a seven-phase workflow with write-tool checks and
transition evidence guards, backed by agent discipline:

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

1. On Claude Code, a **PreToolUse hook** checks only `Write`/`Edit`
   against the current phase's whitelist. Read a deny's reason and next
   action; don't retry the same call or route around it. **Bash, MCP,
   NotebookEdit, and other writers are not automatically gated.** Missing
   `file_path` tool-input fields are allowed. Parsing, root discovery, active/state loading,
   and response errors **fail open**; no active change also allows calls.
   Hosts without this hook (such as DeepSeek Harness) rely on your
   discipline for all writes. This is not a filesystem security sandbox.
2. **Guard checks** (`mulix guard <event>`, run automatically inside
   `mulix state transition <event>`) check recorded evidence (artifact
   existence, approval flags, ledger completion and RED/GREEN reports).
   They do **not** authenticate human consent or prove tests actually
   executed. A transition must be legal from the current phase; `--force`
   skips readiness guards only and cannot make an illegal event legal.

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
`.claude/skills/` (unprefixed: `brainstorming`, not
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
hand first. New IDs use the local date, e.g. `2026-10-14-add-login`,
with `-2`, `-3` for same-day/title collisions. All three directories share
that ID; filenames stay spec.md/tasks.md/report.md. Old `001-*` changes
remain selectable with `mulix state select <change>`; don't rename them.

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
