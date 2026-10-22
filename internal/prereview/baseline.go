package prereview

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// UnknownSourceIdentity is scope_source.UNKNOWN_SOURCE_IDENTITY.
const UnknownSourceIdentity = "unknown"

// CaptureTimeoutSeconds is CAPTURE_BASELINE_TIMEOUT_SECONDS (also the head
// run's default timeout).
const CaptureTimeoutSeconds = 300

// Failure is BaselineFailure.
type Failure struct {
	Test, Error, File string
}

func (f Failure) object() *jsonx.Object {
	return jsonx.NewObject("test", f.Test, "error", f.Error, "file", f.File)
}

// Baseline is BaselineTestResult (Failed == -1 is the capture-failed
// sentinel, never persisted).
type Baseline struct {
	WPID, CapturedAt, BaseBranch, BaseCommit, TestRunner string
	Total, Passed, Failed, Skipped                      int
	Failures                                            []Failure
	SourceIdentity                                      string
}

// Object is to_dict().
func (b *Baseline) Object() *jsonx.Object {
	fs := []any{}
	for _, f := range b.Failures {
		fs = append(fs, f.object())
	}
	return jsonx.NewObject("wp_id", b.WPID, "captured_at", b.CapturedAt, "base_branch", b.BaseBranch,
		"base_commit", b.BaseCommit, "test_runner", b.TestRunner, "total", b.Total, "passed", b.Passed,
		"failed", b.Failed, "skipped", b.Skipped, "failures", fs, "source_identity", b.SourceIdentity)
}

// Save writes json.dumps(to_dict(), indent=2) (no trailing newline).
func (b *Baseline) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(jsonx.Dumps(b.Object(), jsonx.Pretty)), 0o644)
}

// ReadError is ReviewBaselineReadError (malformed baseline JSON).
type ReadError struct{ Path, Reason string }

func (e *ReadError) Error() string { return fmt.Sprintf("Malformed baseline JSON at %s: %s", e.Path, e.Reason) }

// LoadBaseline is BaselineTestResult.load: nil when the artifact is absent.
func LoadBaseline(path string) (*Baseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	v, err := pyjson.LoadsBytes(raw)
	if err != nil {
		return nil, &ReadError{Path: path, Reason: err.Error()}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("baseline JSON at %s is not an object", path)
	}
	need := func(k string) (any, error) {
		x, ok := o.Get(k)
		if !ok {
			return nil, fmt.Errorf("KeyError: %s", pyjson.ReprString(k))
		}
		return x, nil
	}
	b := &Baseline{SourceIdentity: UnknownSourceIdentity}
	for _, f := range []struct {
		k string
		p *string
	}{{"wp_id", &b.WPID}, {"captured_at", &b.CapturedAt}, {"base_branch", &b.BaseBranch}, {"base_commit", &b.BaseCommit}, {"test_runner", &b.TestRunner}} {
		x, err := need(f.k)
		if err != nil {
			return nil, err
		}
		*f.p, _ = x.(string)
	}
	for _, f := range []struct {
		k string
		p *int
	}{{"total", &b.Total}, {"passed", &b.Passed}, {"failed", &b.Failed}, {"skipped", &b.Skipped}} {
		x, err := need(f.k)
		if err != nil {
			return nil, err
		}
		*f.p = toInt(x)
	}
	for _, x := range o.GetList("failures") {
		fo, _ := x.(*jsonx.Object)
		if fo == nil {
			continue
		}
		b.Failures = append(b.Failures, Failure{Test: fo.GetString("test"), Error: fo.GetString("error"), File: fo.GetString("file")})
	}
	if s, ok := o.Get("source_identity"); ok {
		if str, ok := s.(string); ok {
			b.SourceIdentity = str
		}
	}
	return b, nil
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	if n, ok := v.(interface{ Int64() (int64, error) }); ok {
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// Sentinel is _make_sentinel.
func Sentinel(wpID, baseBranch, baseCommit string) *Baseline {
	return &Baseline{WPID: wpID, CapturedAt: UTCSecondStamp(), BaseBranch: baseBranch, BaseCommit: baseCommit,
		TestRunner: "pytest", Failed: -1, SourceIdentity: UnknownSourceIdentity}
}

// UTCSecondStamp is kernel.clock.now_utc_stamp (%Y-%m-%dT%H:%M:%SZ).
func UTCSecondStamp() string { return clock.Now().UTC().Format("2006-01-02T15:04:05Z") }

// Diff is diff_baseline: (pre-existing, new, fixed).
func Diff(b *Baseline, current []Failure) (pre, newF []Failure, fixed []string) {
	if b.Failed == -1 {
		return nil, append([]Failure(nil), current...), nil
	}
	base := map[string]bool{}
	for _, f := range b.Failures {
		base[f.Test] = true
	}
	cur := map[string]bool{}
	for _, f := range current {
		cur[f.Test] = true
	}
	for _, f := range current {
		if base[f.Test] {
			pre = append(pre, f)
		} else {
			newF = append(newF, f)
		}
	}
	for _, f := range b.Failures {
		if !cur[f.Test] {
			fixed = append(fixed, f.Test)
		}
	}
	return
}

// ParseJUnit is _parse_junit_xml: every <testcase> anywhere in the tree.
func ParseJUnit(path string) (total, passed, failed, skipped int, failures []Failure, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	dec := xml.NewDecoder(fh)
	type caseState struct {
		attrs            map[string]string
		failure, errorEl *map[string]string
		skipped          bool
		depth            int
	}
	var stack []*caseState
	depth := 0
	attrMap := func(se xml.StartElement) map[string]string {
		m := map[string]string{}
		for _, a := range se.Attr {
			m[a.Name.Local] = a.Value
		}
		return m
	}
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			err = e
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "testcase" {
				stack = append(stack, &caseState{attrs: attrMap(t), depth: depth})
				continue
			}
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				if depth == top.depth+1 {
					m := attrMap(t)
					switch t.Name.Local {
					case "failure":
						if top.failure == nil {
							top.failure = &m
						}
					case "error":
						if top.errorEl == nil {
							top.errorEl = &m
						}
					case "skipped":
						top.skipped = true
					}
				}
			}
		case xml.EndElement:
			if len(stack) > 0 && t.Name.Local == "testcase" && stack[len(stack)-1].depth == depth {
				c := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				total++
				switch {
				case c.failure != nil || c.errorEl != nil:
					failed++
					active := c.failure
					if active == nil {
						active = c.errorEl
					}
					msg, ok := (*active)["message"]
					if !ok {
						msg = "Unknown error"
					}
					msg = strings.SplitN(msg, "\n", 2)[0]
					if r := []rune(msg); len(r) > 200 {
						msg = string(r[:200])
					}
					name := c.attrs["name"]
					test := name
					if cls := c.attrs["classname"]; cls != "" {
						test = cls + "." + name
					}
					file := c.attrs["file"]
					if file == "" {
						file = "unknown"
					}
					line := c.attrs["line"]
					if line == "" {
						line = "?"
					}
					failures = append(failures, Failure{Test: test, Error: msg, File: file + ":" + line})
				case c.skipped:
					skipped++
				default:
					passed++
				}
			}
			depth--
		}
	}
	return
}
