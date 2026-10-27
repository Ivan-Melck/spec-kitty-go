package toolsurface

import (
	"os"
	"path/filepath"
	"sort"
)

// nodeSig is the exact observation upstream retains for a recheck: kind,
// content digest, mode, mtime (files; directories when withDirMtime) and
// node identity.
type nodeSig struct {
	kind, sha, target string
	mode              int
	mtime             int64
	dev, ino          uint64
}

func observeSig(p string, withDirMtime, withIdentity bool) nodeSig {
	info, err := os.Lstat(p)
	if err != nil {
		return nodeSig{kind: "absent"}
	}
	s := nodeSig{mode: int(info.Mode().Perm())}
	if withIdentity {
		s.dev, s.ino = nodeIdentity(p, info)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		s.kind = "symlink"
		s.target, _ = os.Readlink(p)
		s.mtime = info.ModTime().UnixNano()
	case info.IsDir():
		s.kind = "directory"
		if withDirMtime {
			s.mtime = info.ModTime().UnixNano()
		}
	case info.Mode().IsRegular():
		s.kind = "file"
		if b, err := os.ReadFile(p); err == nil {
			s.sha = Digest(b)
		}
		s.mtime = info.ModTime().UnixNano()
	default:
		s.kind = "other"
	}
	return s
}

// observeTreeSigs is profiles._paths.observe_tree (directories keep their
// mtime, children sorted, links not followed).
func observeTreeSigs(root string) []struct {
	path string
	sig  nodeSig
} {
	var out []struct {
		path string
		sig  nodeSig
	}
	var walk func(p string)
	walk = func(p string) {
		s := observeSig(p, true, false)
		out = append(out, struct {
			path string
			sig  nodeSig
		}{p, s})
		if s.kind == "directory" {
			ents, _ := os.ReadDir(p)
			names := make([]string, 0, len(ents))
			for _, e := range ents {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			for _, n := range names {
				walk(filepath.Join(p, n))
			}
		}
	}
	walk(root)
	return out
}

// treeSnapshot captures observeTreeSigs of several roots.
func treeSnapshot(roots []string) func() bool {
	take := func() []struct {
		path string
		sig  nodeSig
	} {
		var all []struct {
			path string
			sig  nodeSig
		}
		for _, r := range roots {
			all = append(all, observeTreeSigs(r)...)
		}
		return all
	}
	before := take()
	return func() bool {
		now := take()
		if len(now) != len(before) {
			return false
		}
		for i := range now {
			if now[i] != before[i] {
				return false
			}
		}
		return true
	}
}
