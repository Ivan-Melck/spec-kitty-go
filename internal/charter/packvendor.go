package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const PackPinNothingToBump = "nothing to bump: pass --ref (and/or --subtree). To restore the pinned tree run `make sync-packs`."
const packPinDriftRemedy = "packs/<pack>/ is fetched from spec-kitty-open-packs at the pinned commit and must not be hand-edited. Run `make sync-packs` to restore the pinned tree, or bump the pin (`spec-kitty charter pack vendor --ref <sha-or-branch>`); do not hand-edit packs/<pack>/."

func loadVendorPin(live, pack string) (*PackPin, error) {
	pin, err := LoadPackPin(filepath.Join(filepath.Dir(live), pack+".source.yaml"))
	if err != nil {
		return nil, fmt.Errorf("cannot load the pin lockfile: %s", err)
	}
	return pin, nil
}
func VerifyVendorPin(live, pack string) (*PackPin, error) {
	pin, err := loadVendorPin(live, pack)
	if err != nil {
		return nil, err
	}
	valid, err := CheckPackPin(pin, filepath.Join(filepath.Dir(live), pack))
	if err != nil {
		return nil, fmt.Errorf("packs/%s.source.yaml cannot be verified: %s", pack, err)
	}
	if !valid {
		return nil, fmt.Errorf("packs/%s/ is missing or drifted from the pin (commit %s). %s", pack, pin.Commit, packPinDriftRemedy)
	}
	return pin, nil
}
func BumpPackPin(live, pack string, ref, subtree *string) (*PackPin, error) {
	if ref == nil && subtree == nil {
		return nil, fmt.Errorf("%s", PackPinNothingToBump)
	}
	pin, err := loadVendorPin(live, pack)
	if err != nil {
		return nil, err
	}
	if subtree != nil {
		if err := ValidatePackSubtree(*subtree); err != nil {
			return nil, err
		}
		pin.Subtree = *subtree
	}
	scratch, err := os.MkdirTemp("", "pin-bump-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	sha := pin.Commit
	if ref != nil && *ref != "" {
		sha = *ref
	}
	if !pinSHA.MatchString(sha) {
		output, err := gitx.RemoteContact(scratch, pin.Repository, 30*time.Second, "ls-remote", "--heads", pin.Repository, "refs/heads/"+sha)
		if err != nil {
			return nil, fmt.Errorf("cannot fetch the pinned ref: %s", err)
		}
		found := ""
		for _, line := range strings.Split(output, "\n") {
			value, name, ok := strings.Cut(line, "\t")
			if ok && name == "refs/heads/"+sha {
				found = strings.TrimSpace(value)
			}
		}
		if found == "" {
			return nil, fmt.Errorf("cannot resolve %s to a commit: pass a 40-hex SHA or a branch name of %s", pyjson.Repr(sha), pin.Repository)
		}
		sha = found
	}
	fetched, err := gitx.FetchCommitSubtree(pin.Repository, sha, pin.Subtree, filepath.Join(scratch, "fetched"))
	if err != nil {
		return nil, fmt.Errorf("cannot fetch the pinned ref: %s", err)
	}
	if info, err := os.Stat(fetched); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("commit %s has no %s subtree in %s", sha, pyjson.Repr(pin.Subtree), pin.Repository)
	}
	if err := assertPinnedCoreCompatible(pin, fetched); err != nil {
		return nil, err
	}
	digest, err := PackCleanTreeDigest(fetched, pin.Level)
	if err != nil {
		return nil, err
	}
	snapshot, err := PackFullTreeDigest(fetched)
	if err != nil {
		return nil, fmt.Errorf("the fetched subtree is unusable (%s); the pin was not changed", err)
	}
	pin.Commit, pin.Algorithm, pin.Digest, pin.Snapshot = sha, "clean-tree-sha256", digest, &snapshot
	valid, err := CheckPackPin(pin, fetched)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("the fetched subtree is unusable (symlink or unreadable file); the pin was not changed")
	}
	if err := WritePackPin(pin, filepath.Join(filepath.Dir(live), pack+".source.yaml")); err != nil {
		return nil, err
	}
	return pin, nil
}
