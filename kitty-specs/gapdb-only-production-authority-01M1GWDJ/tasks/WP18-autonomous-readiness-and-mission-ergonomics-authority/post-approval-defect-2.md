# WP18 post-approval defect 2

Independent integration review found that the repository-level autonomy mutant
guard still seals the original 15-mutant WP18 campaign. The approved
acknowledged-WorkRevision repair added five controlled mutants, so the outer
architecture contract now fails and would not detect deletion or substitution
of those new controls.

Required repair: update `internal/architecture/m2_autonomy_mutant_test.go` to
require all 20 mutants and explicitly enumerate the five acknowledgement and
substitution mutant identifiers.
