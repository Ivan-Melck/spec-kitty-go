**Recovery reason:** The pre-implementation architecture review found that the
ratified projection tier permits only typed read-model dependencies, while the
original WP09 scope omitted the already-reserved mission and workgraph
read-model packages. The governed scope now includes those minimal typed seams
and explicitly represents an unobserved drift projection as `not_checked`
without equating it to semantic `clean`. Reclaim WP09 from the amended plan.
