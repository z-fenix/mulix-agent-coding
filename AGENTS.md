<!-- mulix:begin (do not edit between these markers) -->

# mulix — enforced spec-driven workflow

This project is managed by mulix. Its skills live in `.dsh/skills/`
(`using-mulix` is the entry point; the embedded skills run inside the
phases).

1. Run `mulix state show` before any work — never infer the phase from
   conversation history.
2. Invoke the active phase's skill before acting in that phase.
3. This host has no PreToolUse hook: the guard checks inside
   `mulix state transition` are the enforcement. A failed guard is
   blocking — do what it says, never route around it or reach for
   `--force` out of impatience.
4. Keep every write inside the current phase's artifact paths even
   though nothing technical stops you: `docs/specs/<change>/`
   (specify/clarify), `docs/changes/<change>/` (tasks/verify), and
   `.mulix/.runtime/<change>/` (design docs, build workspace).
<!-- mulix:end -->
