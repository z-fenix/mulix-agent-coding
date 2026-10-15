---
name: mulix-design
description: Use when the active change is in the design phase, before any plan or code exists for it.
---

# Design phase

**REQUIRED SUB-SKILL:** invoke `brainstorming` now and follow it
exactly. This skill only adds what mulix needs around it: where its
output goes, which path applies, and how the phase ends.

## Inputs

Read `docs/specs/<change>/spec.md` (the clarified WHAT/WHY — including
`## Clarifications`) and `.mulix/memory/constitution.md` if it has real
content. The spec is the requirements authority for the design: don't
re-ask what it already answers, and don't design past its Out of Scope.
Read-only exploration of the codebase is always allowed.

## Paths

Classify out loud, as brainstorming says, then record it:

```
mulix state set design_track bounded        # or: architectural
```

- **bounded** — a well-scoped change to a flow that already exists in
  this repo. Clarifying questions, a short design in chat, then STOP for
  an explicit yes. No design doc.
- **architectural** — anything else (new subsystem, new project,
  interfaces others depend on). The full brainstorming process: questions
  one at a time, 2-3 approaches, sectioned design, then the written spec
  at `.mulix/.runtime/<change>/specs/YYYY-MM-DD-<topic>-design.md`, its
  self-review, and the human's review of the written file.
- **spike** does not apply here: the change already has a spec, so its
  output is code that ships. If the human really only wants an answer to a
  feasibility question, say so and stop — that's not a mulix change.

When in doubt, take architectural. The ratchet is one-way: hidden
complexity found mid-design upgrades bounded → architectural (record the
new track), never the reverse.

## Where things go

- Design doc: `.mulix/.runtime/<change>/specs/` (architectural only).
- Visual companion: pass the project root as `--project-dir`; sessions
  land in `.mulix/.runtime/<change>/brainstorm/` (git-ignored).
- Nothing else is writable in this phase — on Claude Code the hook
  blocks spec.md, docs/changes/, and source code; on hosts without the
  hook the same whitelist binds by discipline. If the design shows the
  spec itself is wrong, stop and tell the human; changing requirements
  is their call.

## Ending the phase

Brainstorming's terminal states are replaced by this gate. After the
human's explicit approval of the stage actually presented — the in-chat
design (bounded) or the written spec file (architectural) — record it:

```
mulix state set design_path .mulix/.runtime/<change>/specs/<file>.md   # architectural only
mulix state set design_approved true
mulix state transition design-approved
```

Do not invoke writing-plans or any implementation skill from here — the
tasks phase does that. For a bounded design, carry its approach, files
touched, and testing plan forward in your reply so the tasks phase can
write them down: it's the only record of an in-chat design.

The guard checks the track is set, the approval is recorded, and (for
architectural) the design doc exists, is non-empty, and sits under
`.mulix/.runtime/<change>/specs/`. It cannot check that the approval was
real — never set `design_approved true` on silence or on approval of an
earlier, different stage.
