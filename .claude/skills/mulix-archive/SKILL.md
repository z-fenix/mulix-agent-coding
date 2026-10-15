---
name: mulix-archive
description: Use when the active change is in the archive phase, after verification has passed, to integrate the work with an explicit human decision.
---

# Archive phase

**REQUIRED SUB-SKILL:** invoke `finishing-a-development-branch` and
follow it: verify the suite is green, detect the environment, present
its menu exactly as written (merge locally / push and open a PR / keep
as-is — discarding only on an explicit request), and carry out the
human's choice.

Do not author project files in this phase. Claude Code's PreToolUse
hook checks `Write`/`Edit`, not Bash, MCP, NotebookEdit or other writers;
missing paths and hook errors fail open (see `mulix-using`). Hosts without
the hook rely on discipline. This is not a sandbox or permission to route
around the whitelist. Authorized finishing uses git/CLI commands, which
are not gated by this hook. Commit the
change's record before integrating: `docs/specs/<change>/`,
`docs/changes/<change>/`, and `.mulix/.runtime/<change>/` (its state,
design doc, and execution workspace — the visual-companion sessions are
git-ignored).

Only after the human has chosen — never inferred from silence — record
it:

```
mulix state set archive_confirmation confirmed
mulix state transition archived
```

The normal guard requires `archive_confirmation=confirmed`; it checks the
recorded value, not the authenticity of the human's choice. Transition
`--force` skips this readiness guard, not the phase table, and must not be
used to conceal missing consent. Archiving clears the active-change marker.
