# Deferred minors — read-only workbench

## stale-label

**Status**: deferred; route to a later independently scoped text-copy change.

`mods/mulix-workbench/hooks/view.tsx` uses `过期（读取失败，保留同根快照）` for all stale states. A successful determination that no active change exists can coexist with that generic read-failure wording. The data remains explicitly stale and the detailed reason is shown; this is a clarity/polish issue, not an additional code-fix item in the single final-review pass.

Suggested future direction: a cause-neutral stale label or wording derived from the known reason. No fix was made in this change, and this entry is not approval to implement that follow-up.
