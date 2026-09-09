# Installer bootstrap command visibility

The M2 absence guard correctly rejects the initial WP07 shell command substitutions and then its unresolved executable variable. Both real failures are preserved against their exact candidates. The guard is not changed.

The selected installer design requires an explicit absolute compiler path whose basename is `go`. After resolving the source and selected compiler directories physically, the shell invokes the literal `./go` from that compiler directory and passes the physical source root with `-C`. This keeps compiler choice explicit and makes the command visible to source analysis. There is no PATH discovery, interpreter indirection, descriptor execution or allowance for arbitrary unresolved command names.

This is an installer bootstrap restriction: a renamed compiler must be supplied through a standard `go` entry point. Nonstandard basenames are rejected before execution. Existing canonical Go1.26.7 installations and hosted setup-go paths meet this requirement. The compiled qualifier API does not acquire an unnecessary new restriction. Exact full version output and compiler exit status, sanitized environment, safe temporary capture, and source paths containing spaces remain required.

The root architect authorized this bounded choice under the existing implementation request. Independent reviewer spec_risk_review found no substantive design blocker and confirmed the required path/status/output/refusal controls. Corrected-source validation remains required before the implementation is accepted. No full qualification receipt is rebound to the corrected source.
