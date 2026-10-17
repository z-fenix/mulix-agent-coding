# Workbench task plan — independent review

**Date**: 2026-10-16
**Change**: `2026-10-15-claude-code-2-1-300-mods-read-only-workbench`
**Plan**: `tasks.md`
**Status**: Independent focused recheck Approved; initial Issue 1 confirmed closed; user task approval pending

## Scope and evidence

Fresh general-purpose reviewer on an advanced model independently compared the plan with the approved design, main spec plus the explicit 2.1.301 amendment, current fc53804 source, engine lock/load/drift tests, lock-check script and CI, the exact 2.1.301 declarations and native testing reference.

The initial reviewer verified the relevant API names/result shapes, core versus op hook answers, native testing Engine versus plugin EngineInterface distinction, complete Pane props, intrinsic test/validate commands, Go lock fields/test paths and meaningful T1 RED/GREEN. This was read-only source verification, not a compile, test, UI or runtime pass.

## Initial result

**Status: Issues — one blocking Issue.**

**Issue 1**: T4 only asserted two fixed visible strings. A pane could omit the required workflow fields, actual token values, provider/window distinctions and failure feedback while its state-only tests passed. That would leave FR-004/008/009/011's visible behavior unverified.

Four additional non-blocking advisories concerned fixture version/empty-state shapes, root-relative script paths, wrong-version user feedback and cross-model labels.

## Applied correction

- T4 requires keyed native Text rows and asserts actual root/change/phase/design/verification values, query UTC timestamp, source, and fresh/stale transitions across multiple changing query results; a root change cannot retain old fields.
- Usage UI asserts the native main session/provider, actual context 512/4096/12% then 768/4096/18%, four observed token classes 90/0/910/25, the weighted 91% hit, two valid main turns and both UTC observation-window endpoints. Before data exists, rows must disclose unavailable rather than zero.
- Fees remain unavailable; a positive raw host amount cannot appear as a cost figure. This does not add pricing sources or a new scope decision.
- Unplaced UI and missing usage are separate cases. Both unplaced commands require visible non-debug ui.log feedback and zero workflow subprocesses; every wrong-version request requires fresh visible feedback. ui.notice is not used with an invented tool_use_id.
- Fixture explicitly returns a SessionVersion object, uses the real mock clock and its advance, and falls back to freshState only for undefined native state. Tests check rendered data, not only fixture state.
- Cross-model labels and root-level scripts/ versus mod-relative paths are explicit. Task numbering stays 1..5; steps remain unexecuted.

## Decisions

**Ruling — 决定**: Accept Issue 1 and all four advisories, correct the plan, and ask the same independent reviewer for a focused closure check before requesting human task approval.

**理由**: State correctness alone does not prove the promised visible workbench; these assertions pin the actual observable contract without adding runtime effects or changing A.

**错了代价**: Presenting an unclosed plan as Approved would let a static placeholder UI pass the implementation gate; expanding this into unrelated API or TUI work would break the approved scope.

The exact 2.1.301 ui.log text/to and mock.clock.advance declarations were checked before specifying the new fixtures. Other previously verified SDK/source conclusions are retained rather than re-swept.

## Separate human authorization

The user has explicitly chosen build-only isolated TypeScript >=5.4 installation with fixed version, scripts disabled and no global/product dependency changes. The plan records 5.4.5 as the orchestrator's compatible fixed-version choice; package reachability, integrity and actual installation are still unverified. This authorization is not task approval, execution-method selection, local-commit permission, hot-load choice or real-model-smoke authorization.

## Focused recheck

The same independent reviewer re-read only the revised T4 contracts/tests and related metadata, confirmed **Issue 1 closed**, found no blocking regression in the revision, confirmed all four advisories applied, and returned **Status: Approved**. The reviewer checked the added native matcher/types, real mock clock advance, key/find text shape and visible ui.log handling against the retained target API context. Two notes about matching the chosen row labels and keeping tooling installation scoped were non-blocking and are explicit in the plan.

## Remaining gates

Independent plan review is closed. No task is executed, no compiler is installed, engine.lock remains unchanged, and no implementation or test source exists from this change. User task approval is still required; actual build, execution-method/local-commit authorization and final-review/verification/archive gates remain separate. Review approval is not a compile, automated-test or real-UI result.
