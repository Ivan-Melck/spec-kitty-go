package setup

import "github.com/spec-kitty/go-kitty3/internal/charter"

func init() {
	migrationImpls["4.0.0rc8_project_intent_directive_rename"] = migrationImpl{
		detect: func(root string) bool {
			report, err := charter.RenameLegacyProjectIntentDirective(root, true)
			return err != nil || len(report.Actions) > 0
		},
		canApply: func(root string) (bool, string) {
			if !exists(root) {
				return false, "Project path does not exist: " + root
			}
			return true, ""
		},
		apply: func(root string, dry bool) migrationResult {
			report, err := charter.RenameLegacyProjectIntentDirective(root, dry)
			if err != nil {
				return migrationResult{Errors: []string{err.Error()}}
			}
			changes := append([]string{}, report.Actions...)
			if dry {
				for i := range changes {
					changes[i] = "Would " + changes[i]
				}
			}
			return migrationResult{Success: true, Changes: changes}
		},
	}
}
