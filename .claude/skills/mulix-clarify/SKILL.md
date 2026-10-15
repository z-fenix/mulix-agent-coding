---
name: mulix-clarify
description: Use when the active change is in the clarify phase, to resolve ambiguity in the spec before design begins.
---

# Clarify phase

Goal: detect and reduce ambiguity or missing decision points in
`spec.md`, and record the clarifications directly in the spec file. This
is expected to run (and finish) before the design phase — if you're
skipping it for a small, already-unambiguous change, say so explicitly and
warn that downstream rework risk goes up.

## Scan for ambiguity first

Read the spec and classify each of these categories as Clear / Partial /
Missing (keep this scan internal, don't dump it unless nothing needs
asking):

- **Functional scope & behavior** — core user goals, explicit
  out-of-scope declarations, actor/persona differences
- **Domain & data model** — entities, attributes, relationships,
  identity/uniqueness, lifecycle/state transitions, scale assumptions
- **Interaction & UX flow** — critical journeys, error/empty/loading
  states, accessibility/localization
- **Non-functional quality** — performance, scalability, reliability,
  observability, security/privacy, compliance
- **Integration & external dependencies** — external services/APIs and
  their failure modes, data formats, versioning
- **Edge cases & failure handling** — negative scenarios, rate limiting,
  concurrent-edit conflicts
- **Constraints & tradeoffs** — technical constraints, explicit
  tradeoffs or rejected alternatives
- **Terminology** — consistent canonical terms, no drift
- **Completion signals** — testable acceptance criteria
- **Placeholders** — leftover TODOs, vague adjectives ("robust",
  "intuitive") lacking quantification

For each Partial/Missing category, it's a candidate question — unless
the answer wouldn't materially change implementation, or it's really a
design/task-breakdown question that belongs in design/tasks instead.

## Ask up to 5, one at a time

Build a prioritized queue (max 5 total) of the `NEEDS CLARIFICATION`
markers and other real ambiguity found above, ranked by how much
downstream rework a wrong guess would cause. Then:

- Present **exactly one question at a time**, never a batch dump.
- Each question must be answerable with a short multiple-choice (2-5
  mutually exclusive options) or a short-phrase answer (<=5 words).
- State your recommended option and why, so the human can just say "yes"
  if they agree.
- After each accepted answer, immediately: append `- Q: <question> → A:
  <answer>` under a `## Clarifications` / `### Session YYYY-MM-DD`
  heading (create these if this is the first answer this session), then
  apply the clarification to the spec section it actually affects
  (Functional Requirements, User Stories, Success Criteria, Edge Cases,
  ...) — don't just log the Q&A and leave the ambiguous text untouched.
- Stop when all critical ambiguity is resolved, the human says "done" /
  "good" / "no more", or you hit 5 questions — whichever comes first.

If there is genuinely nothing worth asking (a rare, small bounded
change), say so to the human and get their explicit agreement to skip,
then record `clarify_skipped=true` — don't skip silently.

## Advancing out of this phase

```
mulix state transition clarify-complete
```

or, only after explicit agreement to skip:

```
mulix state set clarify_skipped true
mulix state transition clarify-skipped
```
