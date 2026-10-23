package setup

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

const migrationLedgerDoc = "Report the migration ledger: which setup steps have run, and their state.\n\nA **read-only** view of the two-partition migration ledger. It lists every\ndeclared setup step in declared (manifest) order and, for each, shows:\n\n\\b\n* whether it is **already in place** (recorded, effect present here),\n* **recorded elsewhere but not here** (a per-clone step the ledger records\n  but whose effect is absent on this checkout — e.g. a freshly pulled clone),\n* **not yet applied**, and\n* whether its source **content changed since it ran** (drift).\n\nDrift is **audit-only**: it is reported, never re-applied. This command never\ncommits and never writes a file.\n\n``--json`` emits the one structured shape the human summary renders from.\n\nExamples:\n\n    spec-kitty migrate ledger\n\n    spec-kitty migrate ledger --json"

func init() {
	app.Register(func(root *cli.Command) {
		group := app.Group(root, "migrate", "")
		group.Add(&cli.Command{Name: "ledger", Short: "Report the migration ledger: which setup steps have run, and their state.", Long: migrationLedgerDoc,
			Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Emit the recorded setup steps and summary as structured JSON (the same shape the human summary renders)."}}, Run: runMigrationLedger})
	})
}

func migrationLedgerPayload(root string) (*jsonx.Object, error) {
	var payload *jsonx.Object
	err := dct.LegacyActivationScope([]string{root}, func() error {
		tracked, err := readMigrationRecords(root, false)
		if err != nil {
			return err
		}
		local, err := readMigrationRecords(root, true)
		if err != nil {
			return err
		}
		selected, err := applicableMigrations("", app.Version, root)
		if err != nil {
			return err
		}
		pending := map[string]bool{}
		for _, m := range selected {
			pending[m.ID] = true
		}
		var steps []any
		applied, pendingCount, elsewhere, driftCount := 0, 0, 0, 0
		for _, m := range migrationCatalog {
			partition, records := "tracked", tracked
			var inPlace any
			if m.CloneLocal {
				partition, records = "clone-local", local
				needed, _ := m.detectErr(root)
				inPlace = !needed
			}
			var result any
			isApplied, drift := false, false
			for _, record := range records {
				if record.Identity != m.Identity {
					continue
				}
				result, isApplied, drift = record.Result, record.Result == "success", record.ContentHash != m.ContentHash
				break
			}
			state := "pending"
			if isApplied {
				applied++
				state = "in-place"
				if inPlace == false {
					state = "recorded-elsewhere"
					elsewhere++
				}
			}
			if pending[m.ID] {
				pendingCount++
			}
			if drift {
				driftCount++
			}
			steps = append(steps, jsonx.NewObject("id", m.ID, "identity", m.Identity, "partition", partition,
				"applied", isApplied, "pending", pending[m.ID], "in_place_here", inPlace, "drift", drift, "result", result, "state", state))
		}
		payload = jsonx.NewObject("summary", jsonx.NewObject("total", len(steps), "applied", applied, "pending", pendingCount, "recorded_elsewhere", elsewhere, "drift", driftCount), "steps", steps)
		return nil
	})
	return payload, err
}

func runMigrationLedger(ctx *cli.Context) error {
	root := project.Locate("")
	if root == "" {
		const message = "Could not locate project root. No .kittify/ directory found in any parent directory."
		if ctx.Bool("json") {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", "NO_PROJECT_ROOT", "message", message)), jsonx.Pretty))
		} else {
			fmt.Fprintln(ctx.Stderr, "Error: "+message)
		}
		return cli.Exit(1)
	}
	payload, err := migrationLedgerPayload(root)
	if err != nil {
		return err
	}
	if ctx.Bool("json") {
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(payload, jsonx.Pretty))
		return nil
	}
	summary := payload.GetObject("summary")
	out(ctx.Stdout, "\n[bold]migration ledger[/bold]")
	for _, item := range [][2]string{{"  Setup steps recorded            : ", "applied"}, {"  Not yet applied                 : ", "pending"}, {"  Recorded elsewhere but not here : ", "recorded_elsewhere"}, {"  Content changed since it ran    : ", "drift"}} {
		value, _ := summary.Get(item[1])
		line := fmt.Sprintf("%s%v", item[0], value)
		if item[1] == "applied" {
			total, _ := summary.Get("total")
			line += fmt.Sprintf(" of %v", total)
		}
		out(ctx.Stdout, line)
	}
	labels := map[string]string{"in-place": "already in place", "recorded-elsewhere": "recorded elsewhere but not here", "pending": "not yet applied"}
	for _, raw := range payload.GetList("steps") {
		step := raw.(*jsonx.Object)
		note := ""
		if step.GetBool("drift") {
			note = " [yellow](content changed since it ran)[/yellow]"
		}
		out(ctx.Stdout, fmt.Sprintf("    %s [dim](%s)[/dim]: %s%s", step.GetString("id"), step.GetString("partition"), labels[step.GetString("state")], note))
	}
	return nil
}
