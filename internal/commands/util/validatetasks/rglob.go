package validatetasks

import (
	"os"
	"path/filepath"
)

// scandir lists dir in directory (getdents) order, like os.scandir /
// Path.iterdir; unreadable directories list as empty.
func scandir(dir string) []os.DirEntry {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	entries, _ := f.ReadDir(-1)
	return entries
}

// rglob is Python 3.13's Path.rglob(pattern) for a single-segment pattern
// (recurse_symlinks=False), keeping its yield order: the root's matches in
// directory order, then each subdirectory's matches as directories are
// discovered, descending from a LIFO stack. match tests an entry name;
// directories whose name matches are yielded too, as upstream does.
func rglob(root string, match func(name string) bool) []string {
	var out []string
	selectNext := func(dir string) {
		for _, e := range scandir(dir) {
			if match(e.Name()) {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	selectNext(root)
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range scandir(dir) {
			if !e.IsDir() { // DirEntry.is_dir(follow_symlinks=False)
				continue
			}
			p := filepath.Join(dir, e.Name())
			selectNext(p)
			stack = append(stack, p)
		}
	}
	return out
}
