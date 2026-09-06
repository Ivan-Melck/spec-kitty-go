# WP16 review feedback — cycle 20

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

Bare `time` semantics are applied without considering the script interpreter:

```sh
#!/bin/dash
review_retired_cli=sql\ite3
select_native_cli() { review_retired_cli=native; }
time select_native_cli
"$review_retired_cli" /tmp/x
```

Dash resolves `time` externally here, cannot invoke the shell function, leaves
the prior retired value intact, and then executes it. The proof applies Bash's
reserved-word behavior to every `.sh` file and reports absence.

Make shell identity an explicit analyzer input derived from a bounded shebang
grammar. Recognized Bash-family interpreters may receive their proven reserved
word semantics; recognized external/Dash forms retain wrapper lookup behavior.
When the interpreter is missing, indirect, or ambiguous and the distinction can
change tracked authority, preserve both possibilities or fail typed ambiguity.
Never infer Bash semantics from a `.sh` suffix alone.

Support common direct and `/usr/bin/env` shebang forms within a closed grammar;
dynamic or unsupported interpreter selection must not silently choose the safer
state transition.

## Preserve

- Cycle-20 return-scope behavior and the correct explicit Bash/time cases.
- Path/quoted/command/env/nested external-time bypass semantics.
- The function-effect lattice and all heredoc, redirection, compound scope,
  assignment, dispatch, lexer, ambiguity, and data controls.
- The observed receipt/schema-3 evidence system and all artifact/source/runtime
  identity invariants.

## Required re-review evidence

- Kill the exact Dash attack.
- Exercise Bash, Dash, direct shebangs, env shebangs, missing/ambiguous shebangs,
  and path-spelled external `time` with relevant function effects.
- Update and verify the closed observed census with zero anomalies.
- Qualify the exact final integrated tree and recompute all evidence digests.
