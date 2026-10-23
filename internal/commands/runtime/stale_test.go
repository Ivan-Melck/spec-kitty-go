package runtime_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var reScanTime = regexp.MustCompile(`in [0-9.]+s \|`)

var reSitePackages = regexp.MustCompile(`\S*/site-packages/`)

// fpRecord is the run_check FP-ceiling UserWarning record (location made
// site-packages-relative, whitespace folded), "" when absent.
func fpRecord(stderr string) string {
	s := strings.Join(strings.Fields(reSitePackages.ReplaceAllString(stderr, "")), " ")
	i := strings.Index(s, "WARNING specify_cli/cli/commands/agent/tests.py:72:")
	if i < 0 {
		return ""
	}
	s = s[i:]
	if j := strings.Index(s, "resolved_root)"); j >= 0 {
		s = s[:j+len("resolved_root)")]
	}
	return s
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return ls[len(ls)-1]
}

const staleSrcV1 = `"""Billing helpers."""

from pkg.util import helper as _helper

__all__ = ["compute_total", "Invoice"]


def compute_total(items):
    """Sum the items."""
    return sum(items)


def legacy_rate(x):
    return x * 2


class Invoice:
    STATUS = "INVOICE_OPEN"

    def render(self):
        return f"Invoice total: {self.total} ({self.currency})"

    def error_text(self):
        return "bad request payload"


def code():
    return "E001"
`

const staleSrcV2 = `"""Billing helpers."""

from pkg.util import helper as _helper
from pkg.legacy import legacy_rate

__all__ = ["compute_sum", "Invoice"]


def compute_sum(items):
    return sum(items)


class Invoice:
    STATUS = "INVOICE_PENDING"

    def render(self):
        return f"Total: {self.total}"


def code():
    return "E002"
`

const staleTests = `import pytest

from pkg.billing import compute_total, legacy_rate, Invoice, code


def test_total():
    assert compute_total([1, 2]) == 3
    assert legacy_rate(2) == 4


class TestInvoice:
    def test_status(self):
        self.assertEqual(Invoice.STATUS, "INVOICE_OPEN")
        self.assertTrue(check(value=compute_total))

    def test_messages(self, capsys):
        with pytest.raises(ValueError) as exc:
            raise ValueError("bad request payload")
        assert "bad request payload" in str(exc.value)
        assert "INVOICE_OPEN" not in capsys.readouterr().out
        assert code() == "E001"
        self.assertNotIn("E001", seen)


def test_nested():
    for x in range(3):
        if x:
            assert x != "INVOICE_OPEN"
`

// TestDiffStaleCheck runs `agent tests stale-check` over a real Python diff:
// a removed function, a renamed one re-exported by import, removed and
// changed string literals, message-content (info) checks and negative
// checks, in Assert statements and assert* calls at several depths.
func TestDiffStaleCheck(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("init", "-q", "-b", "main")
		s.WriteFile("src/pkg/billing.py", staleSrcV1)
		s.WriteFile("src/pkg/other.py", "MESSAGE = \"INVOICE_OPEN\"\nlegacy = 'gone soon'\n")
		s.WriteFile("tests/test_billing.py", staleTests)
		s.WriteFile("tests/broken.py", "def broken(:\n")
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "v1")
		s.WriteFile("src/pkg/billing.py", staleSrcV2)
		s.WriteFile("src/pkg/other.py", "MESSAGE = \"x\"\n")
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "v2")
	})
	for _, args := range [][]string{
		{"agent", "tests", "stale-check", "--base", "HEAD~1", "--json"},
		{"agent", "tests", "stale-check", "--base", "HEAD~1"},
		{"agent", "tests", "stale-check", "--base", "HEAD", "--json"},
		{"agent", "tests", "stale-check", "--base", "HEAD"},
	} {
		g, o := p.Run(args...)
		gs := reScanTime.ReplaceAllString(norm(p.Go, g.Stdout), "in <T>s |")
		os_ := reScanTime.ReplaceAllString(norm(p.Oracle, o.Stdout), "in <T>s |")
		if g.Code != o.Code || gs != os_ {
			t.Errorf("%v: exit %d/%d\n%s", args, g.Code, o.Code, testkit.Diff(os_, gs))
		}
		// stderr: the command's own FP-ceiling line compares exactly; the
		// captured run_check UserWarning record compares with its location
		// reduced to the site-packages-relative module path (whitespace
		// folded: rich wraps the record by that path's length). Upstream's
		// "SyntaxError while parsing <file>" record carries CPython's parser
		// diagnostic and is not reproduced.
		if os.Getenv("KITTY_TEST_TRACE") != "" {
			t.Logf("GO:\n%s\nORACLE:\n%s", g.Stderr, o.Stderr)
		}
		if ge, oe := lastLine(norm(p.Go, g.Stderr)), lastLine(norm(p.Oracle, o.Stderr)); ge != oe {
			t.Errorf("%v: stderr differs\n%s", args, testkit.Diff(oe, ge))
		}
		if gr, or := fpRecord(g.Stderr), fpRecord(o.Stderr); gr != or {
			t.Errorf("%v: FP-ceiling warning record differs\n go:     %s\n oracle: %s", args, gr, or)
		}
	}
}
