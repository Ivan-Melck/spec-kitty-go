package setup

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Compat planner (specify_cli.compat). The Go port never queries PyPI, so
// the CLI status always reports latest_version=null / latest_source="none"
// and the upgrade nag never fires.

// Decision is compat.planner.Decision.
type Decision string

const (
	DecisionAllow            Decision = "ALLOW"
	DecisionAllowWithNag     Decision = "ALLOW_WITH_NAG"
	DecisionBlockMigration   Decision = "BLOCK_PROJECT_MIGRATION"
	DecisionBlockCLIUpgrade  Decision = "BLOCK_CLI_UPGRADE"
	DecisionBlockCorrupt     Decision = "BLOCK_PROJECT_CORRUPT"
	DecisionBlockIncompatile Decision = "BLOCK_INCOMPATIBLE_FLAGS"
)

var decisionExit = map[Decision]int{DecisionAllow: 0, DecisionAllowWithNag: 0, DecisionBlockMigration: 4, DecisionBlockCLIUpgrade: 5, DecisionBlockCorrupt: 6, DecisionBlockIncompatile: 2}

// safetyRegistry is compat.safety.SAFETY_REGISTRY (keys joined by space;
// "" value = always safe, "dry-run" = safe only with --dry-run).
var safetyRegistry = map[string]string{
	"upgrade": "", "migrate": "", "migrate backfill-runtime-state": "dry-run", "init": "", "status": "", "doctor": "",
	"help": "", "version": "", "agent check-prerequisites": "", "agent mission branch-context": "",
	"agent mission check-prerequisites": "", "agent context resolve": "", "agent tasks status": "",
}

// Invocation is compat.planner.Invocation (the fields the decision reads).
type Invocation struct {
	CommandPath []string
	RawArgs     []string
	IsHelp      bool
	IsVersion   bool
}

// Safe is compat.safety.classify.
func (i Invocation) Safe() bool {
	v, ok := safetyRegistry[strings.Join(i.CommandPath, " ")]
	if !ok {
		return false
	}
	if v == "dry-run" {
		for _, a := range i.RawArgs {
			if a == "--dry-run" {
				return true
			}
		}
		return false
	}
	return true
}

// CompatPlan is compat.planner.Plan.
type CompatPlan struct {
	Decision Decision
	Case     string
	ExitCode int
	Project  kittify.ProjectStatus
	Safe     bool
	Human    string
	Pending  []any // pending_migrations entries
}

const unknownInstallNote = "Your install method could not be detected automatically. Upgrade Spec Kitty using the same method you used to install it. See https://spec-kitty.dev/docs/guides/install-and-upgrade for guidance."

var safeValueRe = regexp.MustCompile(`^[A-Za-z0-9.\-+_ /=:]{1,256}$`)

func safeValue(v string) string {
	if safeValueRe.MatchString(v) {
		return v
	}
	return "<unavailable>"
}

// PlanCompat is compat.planner.plan for a resolved project root.
func PlanCompat(inv Invocation, root string) CompatPlan {
	st := kittify.ScanProjectStatus(root)
	p := CompatPlan{Project: st, Safe: inv.Safe(), Decision: DecisionAllow, Case: "none"}
	switch {
	case inv.IsHelp || inv.IsVersion:
	case st.State == kittify.StateCorrupt:
		p.Decision, p.Case = DecisionBlockCorrupt, "project_metadata_corrupt"
	case st.State == kittify.StateTooNew && !p.Safe:
		p.Decision, p.Case = DecisionBlockCLIUpgrade, "project_too_new_for_cli"
	case st.NeedsMigration() && !p.Safe:
		p.Decision, p.Case = DecisionBlockMigration, "project_migration_needed"
	case (st.State == kittify.StateNoProject || st.State == kittify.StateUninitialized) && !p.Safe:
		p.Case = "project_not_initialized"
	}
	p.ExitCode = decisionExit[p.Decision]
	if p.Decision == DecisionBlockMigration && st.Root != "" {
		p.Pending = pendingMigrationSteps(st.Root)
	}
	sv := "<unavailable>"
	if st.SchemaVersion != nil {
		sv = safeValue(fmt.Sprint(*st.SchemaVersion))
	}
	merr := "<unavailable>"
	if st.MetadataError != "" {
		merr = safeValue(st.MetadataError)
	}
	switch p.Case {
	case "project_migration_needed":
		p.Human = "This project needs Spec Kitty project migrations before this command can run.\nRun: spec-kitty upgrade\nPreview first: spec-kitty upgrade --dry-run"
	case "project_too_new_for_cli":
		p.Human = fmt.Sprintf("This project uses Spec Kitty project schema %s, but this CLI supports up to schema %d.\nUpgrade your CLI: %s", sv, kittify.MaxSupportedSchema, safeValue(unknownInstallNote))
	case "project_not_initialized":
		p.Human = "This directory is not a Spec Kitty project. Run: spec-kitty init"
	case "project_metadata_corrupt":
		p.Human = fmt.Sprintf("This project's .kittify/metadata.yaml is unreadable: %s.\nRun: spec-kitty doctor", merr)
	}
	return p
}

// JSON is compat.messages.render_json.
func (p CompatPlan) JSON() *jsonx.Object {
	st := p.Project
	var rootV, svV, merrV any
	if st.Root != "" {
		rootV = st.Root
	}
	if st.SchemaVersion != nil {
		svV = *st.SchemaVersion
	}
	if st.MetadataError != "" {
		merrV = st.MetadataError
	}
	state := string(st.State)
	safety := "unsafe"
	if p.Safe {
		safety = "safe"
	}
	pending := p.Pending
	if pending == nil {
		pending = []any{}
	}
	return jsonx.NewObject(
		"schema_version", 1, "case", p.Case, "decision", string(p.Decision), "exit_code", p.ExitCode,
		"cli", jsonx.NewObject("installed_version", app.Version, "latest_version", nil, "latest_source", "none", "is_outdated", false, "fetched_at", nil),
		"project", jsonx.NewObject("state", state, "project_root", rootV, "schema_version", svV, "min_supported", kittify.MinSupportedSchema,
			"max_supported", kittify.MaxSupportedSchema, "metadata_error", merrV),
		"safety", safety, "install_method", "unknown",
		"upgrade_hint", jsonx.NewObject("install_method", "unknown", "command", nil, "note", unknownInstallNote),
		"pending_migrations", pending, "rendered_human", "")
}

// commandPathFromArgv is migration.gate._build_command_path.
func commandPathFromArgv(argv []string) []string {
	var p []string
	for _, a := range argv {
		if strings.HasPrefix(a, "-") {
			break
		}
		p = append(p, a)
	}
	if len(p) > 0 && p[0] == "orchestrator-api" {
		return p[:1]
	}
	return p
}

// SchemaGate is migration.gate.check_schema_version, run from the root
// callback for every command except upgrade/init and the hook fast paths.
func SchemaGate(ctx *cli.Context, argv []string) error {
	pos := positionals(argv)
	if len(pos) >= 1 && pos[0] == "session-start" {
		return nil
	}
	if len(pos) >= 2 && pos[0] == "live-work" && pos[1] == "hook" {
		return nil
	}
	root := project.Locate("")
	if root == "" {
		return nil
	}
	if st, err := os.Stat(project.Kittify(root)); err != nil || !st.IsDir() {
		return nil
	}
	first := firstCommandToken(argv)
	if first == "upgrade" || first == "init" {
		return nil
	}
	inv := Invocation{CommandPath: commandPathFromArgv(argv), RawArgs: argv}
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			inv.IsHelp = true
		}
	}
	if len(argv) > 0 && (argv[0] == "--version" || argv[0] == "-v") {
		inv.IsVersion = true
	}
	p := PlanCompat(inv, root)
	switch p.Decision {
	case DecisionBlockMigration, DecisionBlockCLIUpgrade, DecisionBlockCorrupt:
		fmt.Fprintln(ctx.Stderr, p.Human)
		return cli.Exit(p.ExitCode)
	}
	return nil
}
