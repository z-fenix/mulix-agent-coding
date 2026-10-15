# [FEATURE NAME] Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

<!-- Written by the tasks phase via the writing-plans skill; executed by
     the build phase. mulix depends on exactly one structural rule here:
     every task is a "## Task N: <name>" section, numbered 1..n in
     execution order with no gaps — the executors extract task briefs by
     that heading, and the guards match the execution ledger and task
     reports to it. Everything else follows writing-plans. -->

**Goal:** [One sentence describing what this builds]

**Architecture:** [2-3 sentences about approach. For a bounded change,
the approach approved in chat during the design phase.]

**Tech Stack:** [Key technologies/libraries]

**Spec:** `docs/specs/[YYYY-MM-DD-change-slug]/spec.md`

**Design:** `.mulix/.runtime/[YYYY-MM-DD-change-slug]/specs/[YYYY-MM-DD-topic]-design.md`
[or: approved in chat (bounded) — summarized under Architecture]

## Global Constraints

[The spec's project-wide requirements — version floors, dependency
limits, naming and copy rules, platform requirements — one line each,
exact values copied verbatim from the spec, plus any MUST principle from
`.mulix/memory/constitution.md` that binds this change. Every task's
requirements implicitly include this section.]

## Review Focus

[The five input classes or failure modes the spec implies but no task's
tests exercise that are most likely to bite a person using this software
— one line each, most likely first. Each line gets its pinning test added
to the task that owns the code.]

---

## Task 1: [Component Name]

**Files:**
- Create: `exact/path/to/file`
- Modify: `exact/path/to/existing:123-145`
- Test: `tests/exact/path/to/test`

**Interfaces:**
- Consumes: [what this task uses from earlier tasks — exact signatures]
- Produces: [what later tasks rely on — exact names, parameter and return types]

- [ ] **Step 1: Write the failing test**

```[language]
[complete test code]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `[exact test command]`
Expected: FAIL with "[expected failure message]"

- [ ] **Step 3: Write minimal implementation**

```[language]
[complete implementation code]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `[exact test command]`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add [files]
git commit -m "[message]"
```

## Task 2: [Component Name]

[Same structure. Repeat the code a task needs rather than writing
"similar to Task 1" — the implementer may read tasks out of order and
only ever sees its own task's brief.]
