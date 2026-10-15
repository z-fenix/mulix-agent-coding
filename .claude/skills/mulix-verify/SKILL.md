---
name: mulix-verify
description: Use when the active change is in the verify phase, to run the project's build/test commands and record the outcome before archiving.
---

# Verify phase

**REQUIRED SUB-SKILL:** `verification-before-completion` governs every
claim this phase records.

Run the project's actual build and test commands — the whole suite, not
just the tests written during build. Then check what got built against
`spec.md`'s requirements and success criteria and the approved design
(`design_path`, or the in-chat design for a bounded change), not just
the ledger: a completion line is a claim, this phase checks the claim.
Read the build's "Rulings I made" and deferred minors from the ledger at
`.mulix/.runtime/<change>/sdd/tasks/progress.md` and say, for each
ruling, whether it holds up against the spec.

Write the commands, their output summary, the requirement-by-requirement
check, and the rulings review to `docs/changes/<change>/report.md`, and
record the result honestly:

```
mulix state set report_path docs/changes/<change>/report.md
mulix state set verify_result pass    # or: fail
```

## If it passes

```
mulix state transition verify-pass
```

This sets `archive_confirmation=pending` — the archive phase requires an
explicit human decision before anything is integrated.

## If it fails

```
mulix state transition verify-fail
```

This sends the change back to build and increments the failure counter.
Say in report.md which tasks the failure traces to. Back in build, fix
through the executor's own loop (a failing test first, then the fix, and
a ledger line for it) — the existing `Task N: complete` lines stay; the
fix adds to the record rather than rewriting it. Report the failure to
the human once `verify_failures` climbs past a couple of rounds — don't
keep looping silently.

Never set `verify_result=pass` because you expect the tests would pass,
or because you already fixed what you assume was wrong. Run them.
