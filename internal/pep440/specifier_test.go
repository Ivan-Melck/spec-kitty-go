package pep440

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestSpecifierMatchesOracle(t *testing.T) {
	bin := testkit.Oracle(t)
	file, err := os.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(file).ReadString('\n')
	file.Close()
	python := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	versions := []string{"4.0.0rc8", "4.0.0", "4.1.dev1", "4.0.post1", "1.4.5+local"}
	specs := []string{"", "<5", ">=3,<5", ">=4.0.0rc1,<5", ">=4.0", "<4.0", "~=4.0", "==4.0.*", "!=4.0rc8", "===4.0.0rc8", "!=4.*", "~=1.4.5", "==1.4.5+local", "bogus", "==4.*.*"}
	inputs := []any{}
	results := []any{}
	for _, version := range versions {
		for _, spec := range specs {
			inputs = append(inputs, []any{version, spec})
			valid, err := Satisfies(version, spec)
			if err != nil {
				results = append(results, err.Error())
			} else {
				results = append(results, valid)
			}
		}
	}
	script := "import json\nfrom packaging.specifiers import SpecifierSet\nresults=[]\nfor version,spec in " + jsonx.Dumps(inputs, jsonx.Line) + ":\n try: results.append(version in SpecifierSet(spec))\n except Exception as error: results.append(str(error))\nprint(json.dumps(results,indent=2))\n"
	sandbox := testkit.NewSandbox(t)
	result := sandbox.Exec("", python, "-c", script)
	if result.Code != 0 {
		t.Fatal(result.Stderr)
	}
	got := jsonx.Dumps(results, jsonx.Pretty) + "\n"
	if got != result.Stdout {
		t.Error(testkit.Diff(result.Stdout, got))
	}
}
