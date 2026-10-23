package maintenance

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func newCharterEncodingCmd() *cli.Command {
	return &cli.Command{
		Name:  "charter-encoding",
		Short: "Scan charter content for non-UTF-8 encodings; normalize-or-fail-loud.",
		Long:  longHelp["migrate_charter-encoding"],
		Flags: []*cli.Flag{
			boolFlag("dry-run", "Show what would change without writing any files.  Returns exit 0 unless ambiguous files are found."),
			{Name: "yes", Short: "y", Kind: cli.Bool, Help: "Apply normalizations without prompting.  Exits non-zero if any file is ambiguous (CI-safe).  Do NOT pass --yes to silently bypass ambiguous files — manual repair is required for those."},
			boolFlag("json", "Emit a JSON-stable summary report on stdout."),
			projectRootFlag(),
		},
		Run: runCharterEncoding,
	}
}

type encRecord struct {
	path, encoding, body string
	confidence           float64
}

func runCharterEncoding(ctx *cli.Context) error {
	wireWarn(ctx)
	dry := groupBool(ctx, "dry-run")
	yes := ctx.Bool("yes")
	jsonOut := ctx.Bool("json")
	root := resolvedPath(ctx.String("project-root"))
	out, errw := ctx.Stdout, ctx.Stderr
	files := migrate.CharterFiles(root)
	var already []string
	var normalized, ambiguous, collisions []encRecord
	yesAll := yes
	reader := bufio.NewReader(ctx.Stdin)
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			return uncaught(ctx, "OSError", err)
		}
		if isUTF8(data) {
			already = append(already, p)
			continue
		}
		rec, cands, err := migrate.RecoverEncoding(data)
		if err != nil {
			return uncaught(ctx, "UnicodeDecodeError", err)
		}
		if rec == nil {
			body := migrate.AmbiguousBody(p, cands)
			ambiguous = append(ambiguous, encRecord{path: p, body: body})
			cprintf(errw, "[red]AMBIGUOUS:[/red] %s", p)
			cprintf(errw, "  %s", strings.SplitN(body, "\n", 2)[0])
			continue
		}
		migrate.WriteEncodingProvenance(p, rec)
		bak := p + ".bak"
		_, berr := os.Lstat(bak)
		collision := berr == nil
		if dry {
			if collision {
				collisions = append(collisions, encRecord{path: p, encoding: rec.SourceEncoding, confidence: rec.Confidence})
				if !jsonOut {
					cprintf(out, "[red]would refuse (backup collision)[/red] %s -> %s", p, bak)
				}
				continue
			}
			normalized = append(normalized, encRecord{path: p, encoding: rec.SourceEncoding, confidence: rec.Confidence})
			if !jsonOut {
				cprintf(out, "[yellow]would normalize[/yellow] %s (%s, confidence %.2f)", p, rec.SourceEncoding, rec.Confidence)
			}
			continue
		}
		if collision {
			collisions = append(collisions, encRecord{path: p, encoding: rec.SourceEncoding, confidence: rec.Confidence,
				body: fmt.Sprintf("Refusing to normalize %s: a backup already exists at %s and would be silently overwritten. Remove or rename the existing backup, then re-run.", p, bak)})
			cprintf(errw, "[red]BACKUP COLLISION:[/red] %s", p)
			cprintf(errw, "  a backup already exists: %s", bak)
			continue
		}
		if !yesAll {
			cprintf(out, "\n[bold]File:[/bold] %s\n[bold]Detected:[/bold] %s (confidence %.2f)\n[bold]Action:[/bold] normalize to UTF-8 with provenance record?", p, rec.SourceEncoding, rec.Confidence)
			fmt.Fprint(out, "  [y/N/a (yes-all)] [N]: ")
			line, rerr := reader.ReadString('\n')
			if rerr != nil && line == "" {
				rich.TyperAbort(errw)
				return cli.Exit(1)
			}
			resp := strings.ToLower(strings.TrimSpace(line))
			if resp == "" {
				resp = "n"
			}
			if resp == "a" {
				yesAll = true
			} else if resp != "y" {
				if !jsonOut {
					cprintf(out, "  [dim]skipped: %s[/dim]", p)
				}
				already = append(already, p)
				continue
			}
		}
		tmp := p + ".tmp-migrate"
		if err := os.WriteFile(tmp, []byte(rec.Text), 0o644); err != nil {
			return uncaught(ctx, "OSError", err)
		}
		os.WriteFile(bak, data, 0o644)
		os.Rename(tmp, p)
		normalized = append(normalized, encRecord{path: p, encoding: rec.SourceEncoding, confidence: rec.Confidence})
		if !jsonOut {
			cprintf(out, "  [green]normalized[/green] %s (%s → utf-8, confidence %.2f)", p, rec.SourceEncoding, rec.Confidence)
		}
	}
	result := "success"
	if len(ambiguous) > 0 {
		result = "ambiguous_present"
	} else if len(collisions) > 0 {
		result = "backup_collision_present"
	}
	if jsonOut {
		norm := []any{}
		for _, r := range normalized {
			norm = append(norm, jsonx.NewObject("confidence", r.confidence, "encoding", r.encoding, "path", r.path))
		}
		amb := []any{}
		for _, r := range ambiguous {
			amb = append(amb, jsonx.NewObject("diagnostic_body", r.body, "path", r.path))
		}
		col := []any{}
		for _, r := range collisions {
			var body any
			if r.body != "" {
				body = r.body
			}
			col = append(col, jsonx.NewObject("diagnostic_body", body, "path", r.path))
		}
		printJSON(out, jsonx.NewObject("already_utf8", strList(already), "ambiguous", amb, "backup_collisions", col, "dry_run", dry,
			"files_inspected", len(files), "normalized", norm, "result", result))
	} else {
		cprintf(out, "\n%s[bold]charter-encoding migration summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Files inspected  : %d", len(files))
		cprintf(out, "  Already UTF-8    : %d", len(already))
		suffix := ""
		if dry {
			suffix = " (would normalize)"
		}
		cprintf(out, "  Normalized       : %d%s", len(normalized), suffix)
		if len(ambiguous) > 0 {
			cprintf(out, "  [red]Ambiguous        : %d (manual repair required)[/red]", len(ambiguous))
			for _, r := range ambiguous {
				cprintf(out, "    [red]%s[/red]", r.path)
			}
		} else {
			cprint(out, "  [green]Ambiguous        : 0[/green]")
		}
		if len(collisions) > 0 {
			cprintf(out, "  [red]Backup collisions: %d (manual repair required)[/red]", len(collisions))
			for _, r := range collisions {
				cprintf(out, "    [red]%s[/red]", r.path)
			}
		} else {
			cprint(out, "  [green]Backup collisions: 0[/green]")
		}
		if dry {
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		} else if len(ambiguous) == 0 && len(collisions) == 0 {
			cprint(out, "\n[green]Done.[/green] Charter corpus is UTF-8 compliant.")
		}
	}
	if len(ambiguous) > 0 || len(collisions) > 0 {
		return cli.Exit(1)
	}
	return nil
}

func isUTF8(b []byte) bool {
	return utf8.Valid(b)
}
