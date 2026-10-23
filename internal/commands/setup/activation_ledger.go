package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrationledger"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

const activationMigrationID = "4.0.0rc8_activation_ledger"

func relativeActivationPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(rel)
}

func init() {
	app.Register(func(root *cli.Command) {
		app.Group(root, "migrate", "").Add(&cli.Command{Name: "activation-ledger", Short: "Move charter activation keys into the activation ledger, and nothing else.", Long: activationLedgerDoc,
			Flags: []*cli.Flag{{Name: "dry-run", Kind: cli.Bool, Help: "List the moves (key, from-file, to-file) and write nothing."}, {Name: "json", Kind: cli.Bool, Help: "Emit the result, the moves, any refusal and the migration-ledger record as JSON."}}, Run: runActivationLedger})
	})
	migrationImpls[activationMigrationID] = migrationImpl{noWorktrees: true,
		detectE: func(root string) (bool, error) {
			p, e := charter.PlanLedgerMigration(root)
			if e != nil {
				return false, e
			}
			return !p.Noop(), nil
		},
		applyE: func(root string, dry bool) (migrationResult, error) {
			p, e := charter.PlanLedgerMigration(root)
			if e != nil {
				return migrationResult{}, e
			}
			if p.Refusal != nil {
				return migrationResult{Success: false, Errors: []string{p.Refusal.Error()}}, nil
			}
			r := migrationResult{Success: true}
			verb := "Moved"
			if dry {
				verb = "Would move"
			}
			for _, move := range p.Moves() {
				r.Changes = append(r.Changes, fmt.Sprintf("%s %s: %s -> %s", verb, move.Key, relativeActivationPath(root, move.From), relativeActivationPath(root, move.To)))
			}
			if !dry {
				written, e := charter.ApplyLedgerMigration(p)
				if e != nil {
					return r, e
				}
				for _, path := range written {
					r.Changes = append(r.Changes, "Wrote "+relativeActivationPath(root, path))
				}
			}
			return r, nil
		}}
}

func activationLedgerPayload(root string, dry bool) (*jsonx.Object, string, error) {
	p, e := charter.PlanLedgerMigration(root)
	if e != nil {
		return nil, "", e
	}
	payload := jsonx.NewObject("result", "noop", "moves", []any{}, "refusal", nil, "ledger_record", "not_recorded")
	if p.Refusal != nil {
		files := []any{}
		for _, path := range p.Refusal.Files {
			files = append(files, relativeActivationPath(root, path))
		}
		payload.Set("result", "refused")
		payload.Set("refusal", jsonx.NewObject("key", p.Refusal.Key, "files", files, "reason", p.Refusal.Reason))
		return payload, p.Refusal.Error(), nil
	}
	moves := []any{}
	for _, move := range p.Moves() {
		moves = append(moves, jsonx.NewObject("key", move.Key, "from", relativeActivationPath(root, move.From), "to", relativeActivationPath(root, move.To)))
	}
	payload.Set("moves", moves)
	if dry {
		if !p.Noop() {
			payload.Set("result", "dry_run")
		}
		return payload, "", nil
	}
	if !p.Noop() {
		if _, e := charter.ApplyLedgerMigration(p); e != nil {
			return nil, "", e
		}
		payload.Set("result", "moved")
	}
	if st, e := os.Stat((migrationledger.Ledger{Root: root}).Path(false)); e == nil && st.Mode().IsRegular() {
		m, _ := migrationByID(activationMigrationID)
		if !migrationSettled(root, m) {
			if e := (migrationledger.Ledger{Root: root}).AppendMany([]migrationledger.Record{migrationRecord(m, "success", "")}); e != nil {
				return nil, "", e
			}
		}
		payload.Set("ledger_record", "written")
	}
	return payload, "", nil
}

func runActivationLedger(ctx *cli.Context) error {
	fail := func(code, message string) error {
		if ctx.Bool("json") {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", message)), jsonx.Pretty))
		} else {
			out(ctx.Stderr, "[red]Error:[/red] "+message)
		}
		return cli.Exit(1)
	}
	root := project.Locate("")
	if root == "" {
		return fail("NO_PROJECT_ROOT", "Could not locate project root. No .kittify/ directory found in any parent directory.")
	}
	payload, refusal, e := activationLedgerPayload(root, ctx.Bool("dry-run"))
	if e != nil {
		return fail("ACTIVATION_LEDGER_MIGRATION_FAILED", e.Error())
	}
	if ctx.Bool("json") {
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(payload, jsonx.Pretty))
	} else if refusal != "" {
		out(ctx.Stdout, "[red]Refused:[/red] "+refusal)
	} else {
		verb := ""
		switch payload.GetString("result") {
		case "dry_run":
			verb = "Would move"
		case "moved":
			verb = "Moved"
		}
		if verb == "" || len(payload.GetList("moves")) == 0 {
			out(ctx.Stdout, "Nothing to do: no activation key is left outside "+dct.ActivationLedgerRelative+".")
		} else {
			for _, raw := range payload.GetList("moves") {
				move := raw.(*jsonx.Object)
				out(ctx.Stdout, fmt.Sprintf("  %s %s: %s -> %s", verb, move.GetString("key"), move.GetString("from"), move.GetString("to")))
			}
			if payload.GetString("result") == "moved" {
				out(ctx.Stdout, fmt.Sprintf("[green]Moved the activation keys into %s.[/green] Migration-ledger record: %s.", dct.ActivationLedgerRelative, payload.GetString("ledger_record")))
			}
		}
	}
	if refusal != "" {
		return cli.Exit(1)
	}
	return nil
}

const activationLedgerDoc = "Move charter activation keys into the activation ledger, and nothing else.\n\nRuns exactly the move of the ``4.0.0rc8_activation_ledger`` upgrade\nmigration: the ``activated_<kind>``, ``activated_kinds`` and\n``mission_type_activations`` keys leave the ``charter.yaml`` that\n``.kittify/config.yaml`` points to (or ``config.yaml`` itself, and any\nleftover copy there) for ``.kittify/charter/activation.yaml``, verbatim,\nand the synthesis manifest is re-stamped. Use it on a project that an\nolder CLI wrote activation keys into again, without a full upgrade.\n\n\b\n* It refuses, writing nothing (exit 1), when the files disagree about a\n  key, or when a legacy file holds a ``deactivated_<kind>`` key.\n* It is idempotent: a re-run reports nothing to do and writes nothing.\n* When ``.kittify/migration-ledger.jsonl`` exists it records the\n  migration as applied (``ledger_record: written``); otherwise it records\n  nothing (``not_recorded``) and a later upgrade finds nothing to move.\n\nExit codes: 0 moved or nothing to do, 1 refused (nothing written), 2 usage error.\n\nExamples:\n\n    spec-kitty migrate activation-ledger --dry-run\n\n    spec-kitty migrate activation-ledger\n\n    spec-kitty migrate activation-ledger --json"
