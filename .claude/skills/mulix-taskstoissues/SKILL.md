---
name: mulix-taskstoissues
description: Use when tasks.md exists for a change and the human asks to convert its tasks into GitHub issues, at any phase.
---

# Converting tasks to GitHub issues

This is a phase-independent utility, not a workflow phase: it does not
touch a change's runtime state, does not require `mulix state
transition`, and can be run any time after `tasks.md` exists, available
on its own rather than as a state-machine step.

mulix uses the `gh` CLI (not a GitHub MCP server) to do this, so it works
offline-installable and needs nothing beyond `gh auth login` having
already been run by the human — check `gh auth status` first, and stop
with a clear message if it fails rather than guessing at credentials.

## Before doing anything

1. Confirm the git remote is actually GitHub:

   ```
   git config --get remote.origin.url
   ```

   > **STOP.** Only proceed to the next steps if the remote is a GitHub
   > URL. Never create issues in a repository that doesn't match the
   > remote — there is no fallback or "just in case" target.

2. Locate the change's `tasks.md` (`mulix state show` for the active
   change's `tasks_path`, or ask which change if ambiguous).

## Deduplicate before creating anything

Each task is a `## Task N: <name>` section (ignore headings inside ```
fences — those are example code, not tasks). Its issue ID is `T` plus N
zero-padded to three digits (`## Task 1: Parser` → `T001`), and its
description is the `<name>` part.

Build the set of task IDs from `tasks.md` first. Then list existing
issues and match titles against the pattern `\bT\d{3,}\b` (three or more
digits, so IDs stay unambiguous once a project passes 999 tasks; this also
matches titles written as `T001 ...`, `T001: ...`, or `[T001] ...`):

```
gh issue list --state all --limit 100 --json number,title
```

Paginate with `--search` or by tracking `number` cursors if the repo has
more than ~100 relevant issues; stop once every task ID in your set has
been matched, or once there are no more pages — don't keep paging through
a large repo's entire issue history after every ID is already accounted
for.

Mark each matched task ID as "already has an issue" and skip it later.

## Creating issues

For every task ID **not** already covered, create one issue with a
canonical title `T001: <description>` (ID once, followed by the task
name — e.g. `## Task 1: Create project structure` becomes
`T001: Create project structure`). The body is the task's section,
from its heading up to the next `## Task` heading, written to a temp
file (not pasted into the command line):

```
gh issue create --title "T001: Create project structure" --body-file <tmp file>
```

> **UNDER NO CIRCUMSTANCES** create issues in a repository that doesn't
> match the remote URL confirmed in step 1.

Report skipped tasks as you go (`T001 already has an issue, skipping`) so
the human sees what happened without needing to check GitHub themselves.

## Hard rules

- Never create an issue in a repository other than the one the git remote
  points at.
- Never guess at deduplication — if `gh issue list` fails or `gh auth
  status` fails, stop and report the failure rather than creating
  possible duplicates.
- One issue per task; don't batch multiple tasks into one issue even if
  they look related — dependency ordering across issues is the human's
  call, not something to collapse away here.
