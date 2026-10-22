package closeout

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
)

// collectWithOptionalRepair is accept._collect_summary_with_optional_repair:
// with --normalize-encoding, an ArtifactEncodingError triggers the canonical
// normalize_feature_encoding repair (charset_normalizer detection via
// internal/charsetnorm), a report, and exactly one re-collection.
func (r *acceptRun) collectWithOptionalRepair(root, slug string, opts acceptance.CollectOptions, normalize bool) (*acceptance.Summary, error) {
	sum, err := acceptance.Collect(root, slug, opts)
	var ae *acceptance.ArtifactEncodingError
	if err == nil || !normalize || !errors.As(err, &ae) {
		return sum, err
	}
	repaired, err := acceptance.NormalizeFeatureEncoding(root, slug)
	if err != nil {
		return nil, err
	}
	r.reportEncodingRepair(root, repaired)
	sum, err = acceptance.Collect(root, slug, opts)
	if errors.As(err, &ae) {
		return nil, &acceptance.Error{Msg: ae.AmbiguousRecoveryMessage()}
	}
	return sum, err
}

// reportEncodingRepair is accept._report_encoding_repair (printed on the
// console even in --json mode, as upstream does).
func (r *acceptRun) reportEncodingRepair(root string, repaired []string) {
	w := r.ctx.Stdout
	if len(repaired) == 0 {
		say(w, "[yellow]--normalize-encoding enabled but no artifacts required updates.[/yellow]")
		return
	}
	say(w, "[yellow]Normalized acceptance-artifact encoding for:[/yellow]")
	for _, p := range repaired {
		display := p
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			display = rel
		}
		say(w, "  - "+display)
	}
}
