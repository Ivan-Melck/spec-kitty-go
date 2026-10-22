package workflow

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/encoding"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// BuiltinRoot is the asset directory of the shipped workflows; its
// _fixtures subdirectory is the second built-in search root.
const BuiltinRoot = "charter/offering/workflows"

// BuiltinDisplayPrefix stands in for the upstream wheel's site-packages
// directory when a built-in workflow path is shown to the user.
const BuiltinDisplayPrefix = "<spec-kitty>/"

// Source is a workflow YAML location: a project file or an embedded asset.
type Source struct {
	Path  string // display path (str(path) upstream)
	Asset string // asset name when built in
}

func (s Source) exists() bool {
	if s.Asset != "" {
		return assets.Exists(s.Asset)
	}
	_, err := os.Stat(s.Path)
	return err == nil
}

// Read returns the raw bytes.
func (s Source) Read() ([]byte, error) {
	if s.Asset != "" {
		return assets.ReadFile(s.Asset)
	}
	return os.ReadFile(s.Path)
}

func builtin(asset string) Source {
	return Source{Path: BuiltinDisplayPrefix + asset, Asset: asset}
}

// GuardedError is a kernel.errors.GuardedReadError subclass
// (UnknownWorkflowError / WorkflowFileError): the CLI prints "Error: <msg>"
// on stderr and exits 1.
type GuardedError struct{ Msg string }

func (e *GuardedError) Error() string { return e.Msg }

var reWorkflowID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidateID is validate_workflow_id.
func ValidateID(id string) error {
	if !reWorkflowID.MatchString(id) {
		return &GuardedError{fmt.Sprintf("Invalid workflow_id %s: must match [a-z0-9][a-z0-9-]*. "+
			"Path-traversal sequences, uppercase letters, spaces, and special "+
			"characters are not permitted in workflow identifiers.", pyjson.ReprString(id))}
	}
	return nil
}

func overrideRoot(projectRoot string) string {
	return filepath.Join(projectRoot, ".kittify", "overrides", "workflows")
}

func candidates(id, projectRoot string) []Source {
	var out []Source
	if projectRoot != "" {
		r := overrideRoot(projectRoot)
		out = append(out, Source{Path: filepath.Join(r, id+".workflow.yaml")}, Source{Path: filepath.Join(r, id+".yaml")})
	}
	return append(out, builtin(path.Join(BuiltinRoot, id+".workflow.yaml")), builtin(path.Join(BuiltinRoot, "_fixtures", id+".workflow.yaml")))
}

// Resolve is resolve_workflow_path.
func Resolve(id, projectRoot string) (Source, error) {
	if err := ValidateID(id); err != nil {
		return Source{}, err
	}
	cands := candidates(id, projectRoot)
	for _, c := range cands {
		if c.exists() {
			return c, nil
		}
	}
	searched := make([]string, len(cands))
	for i, c := range cands {
		searched[i] = c.Path
	}
	return Source{}, &GuardedError{fmt.Sprintf("Unknown workflow_id=%s. Available: %s. Searched: %s.",
		pyjson.ReprString(id), pyjson.ReprStrings(List(projectRoot)), pyjson.ReprStrings(searched))}
}

// Load is load_workflow_file (requested may be "" for None).
func Load(src Source, requested string) (*Sequence, error) {
	fail := func(reason string) error {
		return &GuardedError{fmt.Sprintf("workflow file %s is not valid: %s", pyjson.ReprString(src.Path), reason)}
	}
	b, err := src.Read()
	if err != nil {
		return nil, fail(encoding.PyOSError(err))
	}
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, fail(err.Error())
	}
	// read_text: universal newlines
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	raw, err := pyyaml.SafeLoad(text)
	if err != nil {
		var pe *pyyaml.PyError
		if errors.As(err, &pe) {
			return nil, pe
		}
		return nil, fail(err.Error())
	}
	seq, err := Validate(raw)
	if err != nil {
		return nil, fail(err.Error())
	}
	if err := ValidateID(seq.WorkflowID); err != nil {
		return nil, err
	}
	if requested != "" && seq.WorkflowID != requested {
		return nil, &GuardedError{fmt.Sprintf("Workflow file %s declares workflow_id=%s but was requested as %s.",
			src.Path, pyjson.ReprString(seq.WorkflowID), pyjson.ReprString(requested))}
	}
	return seq, nil
}

func idFromName(name string) string {
	if strings.HasSuffix(name, ".workflow.yaml") {
		return strings.TrimSuffix(name, ".workflow.yaml")
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// List is list_available_workflows: the sorted, de-duplicated ids of every
// loadable workflow under the search roots.
func List(projectRoot string) []string {
	var sources []Source
	if projectRoot != "" {
		r := overrideRoot(projectRoot)
		if entries, err := os.ReadDir(r); err == nil {
			var names []string
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".yaml") {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, n := range names {
				sources = append(sources, Source{Path: filepath.Join(r, n)})
			}
		}
	}
	for _, root := range []string{BuiltinRoot, path.Join(BuiltinRoot, "_fixtures")} {
		entries, err := assets.List(root)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yaml") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			sources = append(sources, builtin(path.Join(root, n)))
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range sources {
		id := idFromName(path.Base(filepath.ToSlash(s.Path)))
		if ValidateID(id) != nil {
			continue
		}
		if _, err := Load(s, id); err != nil {
			continue
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
