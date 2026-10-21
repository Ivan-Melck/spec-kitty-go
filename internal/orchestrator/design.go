package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// DesignError is a typed design-authoring refusal (DesignError /
// DesignContextError): code, message and safe details.
type DesignError struct {
	Code    string
	Message string
	Details *jsonx.Object
}

func (e *DesignError) Error() string { return e.Message }

// NewDesignError builds a DesignError with details pairs.
func NewDesignError(code, message string, pairs ...any) *DesignError {
	d := jsonx.NewObject()
	for i := 0; i+1 < len(pairs); i += 2 {
		d.Set(pairs[i].(string), pairs[i+1])
	}
	return &DesignError{Code: code, Message: message, Details: d}
}

// Absent is the first-submission revision sentinel.
const Absent = "absent"

// SHA256Hex is the bare hex sha256 digest of b.
func SHA256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func commonDir(root string) (string, error) {
	d, err := gitx.CommonDir(root)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(d) {
		d = filepath.Join(root, d)
	}
	return d, nil
}

// designPaths returns the API-authoring marker and receipts paths
// (<git-common-dir>/spec-kitty-design/<sha256(slug)>.{enabled,json}).
func designPaths(root, slug string) (string, string, error) {
	cd, err := commonDir(root)
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(cd, "spec-kitty-design", SHA256Hex([]byte(slug)))
	return base + ".enabled", base + ".json", nil
}

// AuthoringEnabled is api_authoring_enabled (false outside a git repo).
func AuthoringEnabled(root, slug string) bool {
	marker, _, err := designPaths(root, slug)
	if err != nil {
		return false
	}
	_, err = os.Stat(marker)
	return err == nil
}

// Receipt is one accepted authoring receipt.
type Receipt struct {
	Kind          string
	ArtifactID    *string
	SHA256        string
	Parents       *jsonx.Object
	ContextSHA256 string
	ContextAction string
	Actor         string
}

// Object renders the receipt in pydantic field order.
func (r *Receipt) Object() *jsonx.Object {
	var id any
	if r.ArtifactID != nil {
		id = *r.ArtifactID
	}
	parents := r.Parents
	if parents == nil {
		parents = jsonx.NewObject()
	}
	return jsonx.NewObject("kind", r.Kind, "artifact_id", id, "sha256", r.SHA256, "parents", parents,
		"context_sha256", r.ContextSHA256, "context_action", r.ContextAction, "actor", r.Actor)
}

// ReceiptSet is the host-local provenance set (key -> receipt, insertion order).
type ReceiptSet struct {
	Keys     []string
	Receipts map[string]*Receipt
}

// NewReceiptSet returns an empty set.
func NewReceiptSet() *ReceiptSet { return &ReceiptSet{Receipts: map[string]*Receipt{}} }

// Put records a receipt under key (keeping first-insertion order).
func (s *ReceiptSet) Put(key string, r *Receipt) {
	if _, ok := s.Receipts[key]; !ok {
		s.Keys = append(s.Keys, key)
	}
	s.Receipts[key] = r
}

// JSON is ReceiptSet.model_dump_json() (compact, ensure_ascii=False).
func (s *ReceiptSet) JSON() string {
	arts := jsonx.NewObject()
	for _, k := range s.Keys {
		arts.Set(k, s.Receipts[k].Object())
	}
	return jsonx.Dumps(jsonx.NewObject("artifacts", arts), jsonx.Options{Indent: -1, Compact: true})
}

var errReceipts = NewDesignError("DESIGN_RECEIPT_UNREADABLE", "API authoring provenance requires reconciliation")

// ReadReceipts is read_receipts: nil when the mission is not API-authored.
func ReadReceipts(root, slug string) (*ReceiptSet, error) {
	marker, path, err := designPaths(root, slug)
	if err != nil {
		return nil, nil
	}
	if _, err := os.Stat(marker); err != nil {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errReceipts
	}
	v, err := pyjson.LoadsBytes(b)
	if err != nil {
		return nil, errReceipts
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, errReceipts
	}
	set := NewReceiptSet()
	for _, k := range o.Keys() {
		if k != "artifacts" {
			return nil, errReceipts
		}
	}
	av, present := o.Get("artifacts")
	if !present {
		return set, nil
	}
	arts, ok := av.(*jsonx.Object)
	if !ok {
		return nil, errReceipts
	}
	for _, kv := range arts.Pairs() {
		ro, ok := kv.Value.(*jsonx.Object)
		if !ok {
			return nil, errReceipts
		}
		r := &Receipt{}
		var bad bool
		getS := func(k string) string {
			v, ok := ro.Get(k)
			s, isS := v.(string)
			if !ok || !isS {
				bad = true
			}
			return s
		}
		r.Kind = getS("kind")
		r.SHA256 = getS("sha256")
		r.ContextSHA256 = getS("context_sha256")
		r.ContextAction = getS("context_action")
		r.Actor = getS("actor")
		if v, ok := ro.Get("artifact_id"); !ok {
			bad = true
		} else if s, isS := v.(string); isS {
			r.ArtifactID = &s
		} else if v != nil {
			bad = true
		}
		pv, _ := ro.Get("parents")
		if p, ok := pv.(*jsonx.Object); ok {
			r.Parents = p
		} else {
			bad = true
		}
		if bad || ro.Len() != 7 {
			return nil, errReceipts
		}
		set.Put(kv.Key, r)
	}
	return set, nil
}

// EnableAuthoring is enable_authoring: write the marker and an initial
// receipt set unless the marker already exists.
func EnableAuthoring(root, slug string, receipts *ReceiptSet) error {
	marker, path, err := designPaths(root, slug)
	if err != nil {
		return err
	}
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if err := atomicWrite(marker, "enabled\n"); err != nil {
		return err
	}
	return atomicWrite(path, receipts.JSON())
}

// SaveReceipts is save_receipts.
func SaveReceipts(root, slug string, receipts *ReceiptSet) error {
	_, path, err := designPaths(root, slug)
	if err == nil {
		err = atomicWrite(path, receipts.JSON())
	}
	if err != nil {
		return NewDesignError("DESIGN_RECEIPT_UNREADABLE", "Content committed but provenance requires reconciliation",
			"effect_state", "reconciliation_required")
	}
	return nil
}

func atomicWrite(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TouchAuthoringLock creates the cooperative authoring lock file
// (<git-common-dir>/spec-kitty-locks/design-<sha256(slug)>.lock) the way
// upstream's machine_file_lock leaves it behind.
func TouchAuthoringLock(root, slug string) {
	cd, err := commonDir(root)
	if err != nil {
		return
	}
	p := filepath.Join(cd, "spec-kitty-locks", "design-"+SHA256Hex([]byte(slug))+".lock")
	if _, err := os.Stat(p); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
}

// TasksFinalized is status.tasks_are_finalized(statusDir) with the
// snapshot-drift check against status.json.
func TasksFinalized(statusDir string) (bool, error) {
	snap, err := status.ReduceDir(statusDir)
	if err != nil {
		return false, err
	}
	if err := checkNoSnapshotDrift(statusDir, snap); err != nil {
		return false, err
	}
	return snap.WorkPackages.Len() > 0, nil
}

func checkNoSnapshotDrift(dir string, snap *status.Snapshot) error {
	p := filepath.Join(dir, status.SnapshotFile)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("status.json could not be read for snapshot-drift comparison: %v", err)
	}
	v, err := pyjson.LoadsBytes(b)
	if err != nil {
		return fmt.Errorf("status.json could not be read for snapshot-drift comparison: %v", err)
	}
	persisted, _ := v.(*jsonx.Object)
	var missing []string
	if persisted != nil {
		if wps, ok := persisted.Get("work_packages"); ok {
			if o, ok := wps.(*jsonx.Object); ok {
				for _, k := range o.Keys() {
					if snap.WP(k) == nil {
						missing = append(missing, k)
					}
				}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("snapshot drift: status.json's persisted work_packages set names %s, which the freshly-reduced "+
		"status.events.jsonl no longer contains -- treating this as a torn/truncated event-log read (SNAPSHOT_DRIFT, cf. "+
		"audit/classifiers/status_json.py, issue #1782) rather than trusting a silently-shrunk reduction", pyjson.ReprStrings(missing))
}
