package rich

import (
	"strings"
	"testing"
)

func TestPaddedTableLayout(t *testing.T) {
	tb := PaddedTable{Headers: []string{"Check", "Count"}, MinWidths: []int{20, 6}, Justify: []byte("lr"), Rows: [][]string{{"drift", "0"}}}
	got := strings.Join(tb.Lines(80, NoColor), "\n")
	want := "  Check                    Count  \n  drift                        0  "
	if got != want {
		t.Errorf("fits:\n%q\nwant\n%q", got, want)
	}
	// Too wide for 40 columns: min widths win back after the collapse and
	// the lines are cropped at the console width; markup is stripped.
	tb = PaddedTable{Headers: []string{"Name", "Authority", "Present"}, MinWidths: []int{20, 10, 8}, Justify: []byte("llc"),
		Rows: [][]string{{"a_rather_long_surface_name", "[yellow]derived[/yellow]", "[green]Y[/green]"}}}
	lines := tb.Lines(40, NoColor)
	for _, l := range lines {
		if CellLen(l) > 40 {
			t.Errorf("line not cropped: %q", l)
		}
	}
	if !strings.Contains(lines[1], "a_rather_long_surfa…") || !strings.Contains(lines[1], "derived") || strings.Contains(lines[1], "[yellow]") {
		t.Errorf("row: %q", lines[1])
	}
}
