package scenario

// Original configured origin only: no IO, registration/cleanliness, historical
// physical effects, path correspondence token, producer DB or complete census.
import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// Scalar copies only; this commitment is private integrity, NOT a durable
// digest, an admission witness or a cross-trace token. No Repository pointer.
type terminalRepositoryCapture struct {
	present                                bool
	binding                                workspace.RepositoryBinding
	primaryDeclaration, managedDeclaration string
	historySHA                             string
	capture                                journalv2.StoreRevisionV2
	graph                                  [32]byte
	integrity                              [32]byte
}

func terminalRepositoryDriverPresent(owner *portableHarness) bool {
	if owner == nil || owner.git == nil {
		return false
	}
	if g, ok := owner.git.(*realScenarioGit); ok {
		return g != nil
	}
	return true
}

func captureTerminalRepository(p *terminalCoordinateProof, owner *portableHarness) terminalRepositoryCapture {
	g, ok := owner.git.(*realScenarioGit)
	if !ok || g == nil || g.repository == nil {
		return terminalRepositoryCapture{}
	}
	c := terminalRepositoryCapture{present: true, binding: g.repository.Binding(), primaryDeclaration: g.repositoryPath(), managedDeclaration: g.managedRoot(), historySHA: p.historySHA, capture: p.capture, graph: terminalRepositoryGraphIntegrity(p)}
	// Fail closed at the separate configured layer, not the logical constructor.
	c.integrity, _ = c.commitment()
	return c
}

func terminalRepositoryWrite(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}

func (c terminalRepositoryCapture) commitment() ([32]byte, error) {
	if c.graph == ([32]byte{}) {
		return [32]byte{}, fmt.Errorf("repository capture: missing/oversized graph")
	}
	b := c.binding
	fields := []string{b.RepositoryID.String(), b.ProjectID.String(), b.PrimaryPath, b.CommonDir, b.ManagedRoot, b.TargetRef, b.Primary.Head, b.Primary.IndexDigest, b.Primary.TreeDigest, c.primaryDeclaration, c.managedDeclaration, c.historySHA, string(c.capture.DatabaseID()), fmt.Sprint(c.capture.CurrentRevision()), fmt.Sprint(c.capture.DurableThroughRevision()), b.CreatedAt.Format(time.RFC3339Nano), b.VerifiedAt.Format(time.RFC3339Nano)}
	h := sha256.New()
	terminalRepositoryWrite(h, "private-original-configured-repository/v1")
	for _, f := range fields {
		if f == "" || len(f) > workspace.MaxWorkspacePathBytesV2 || !utf8.ValidString(f) || strings.ContainsRune(f, 0) {
			return [32]byte{}, fmt.Errorf("repository capture: incomplete/oversized scalar")
		}
		terminalRepositoryWrite(h, f)
	}
	// Bind produces UTC, monotonic-free times. Keep the actual values, not a
	// reparsed approximation; refuse unusual time encodings rather than repair.
	for _, at := range []time.Time{b.CreatedAt, b.VerifiedAt} {
		if at.IsZero() || at.Location() != time.UTC || at != at.Round(0) {
			return [32]byte{}, fmt.Errorf("repository capture: timestamp encoding")
		}
	}
	_, _ = h.Write(c.graph[:])
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// Commit to the exact validated graph surface consumed below, including old
// versions and original claim bytes. Sorting does not rewrite source facts.
func terminalRepositoryGraphIntegrity(p *terminalCoordinateProof) [32]byte {
	// Private capture budget only; exceeding it never changes logical proof.
	const maxFacts = 4096
	const maxBytes = 64 * 1024 * 1024
	if len(p.states) > maxFacts || len(p.workspaces) > maxFacts || len(p.events) > maxFacts || len(p.boundExecutions) > maxFacts {
		return [32]byte{}
	}
	h := sha256.New()
	terminalRepositoryWrite(h, "private-configured-owner-graph/v1")
	terminalRepositoryWrite(h, p.historySHA)
	terminalRepositoryWrite(h, string(mustTerminalJSON(p.scope)))
	terminalRepositoryWrite(h, string(p.capture.DatabaseID()))
	terminalRepositoryWrite(h, fmt.Sprint(p.capture.CurrentRevision()))
	terminalRepositoryWrite(h, fmt.Sprint(p.capture.DurableThroughRevision()))
	var rows []string
	total := 0
	add := func(row string) bool {
		if len(row) > maxBytes-total {
			return false
		}
		total += len(row)
		rows = append(rows, row)
		return true
	}
	for key, s := range p.states {
		if len(s.canonical) > journalv2.MaxCanonicalPayloadBytes || len(s.envelope.Payload()) > journalv2.MaxCanonicalPayloadBytes {
			return [32]byte{}
		}
		envelope, err := journalv2.EncodeEventEnvelopeV2(s.envelope)
		if !add("state" + terminalRepositoryKeyBytes(key) + terminalRepositoryKeyBytes(s.key) + string(mustTerminalJSON(struct {
			Canonical []byte
			Digest    string
			Envelope  []byte
			Error     string
		}{s.canonical, s.digest.String(), envelope, fmt.Sprint(err)}))) {
			return [32]byte{}
		}
	}
	for c, w := range p.workspaces {
		if len(w.versions) > maxFacts || len(w.first.Dependencies) > maxFacts || len(w.claim.CanonicalValue()) > journalv2.MaxCanonicalPayloadBytes {
			return [32]byte{}
		}
		var versions []string
		for _, key := range w.versions {
			versions = append(versions, terminalRepositoryKeyBytes(key))
		}
		if !add("workspace" + string(mustTerminalJSON(struct {
			Coordinate, Owner journalv2.AggregateCoordinate
			Versions          []string
			First             workspace.BindingV2
			Relative, Claim   string
			ClaimBytes        []byte
			Revision          uint64
		}{c, w.coordinate, versions, w.first, w.relative, w.claim.ClaimDigest().String(), w.claim.CanonicalValue(), w.claimRevision}))) {
			return [32]byte{}
		}
	}
	for event, key := range p.events {
		if !add("event" + string(event) + terminalRepositoryKeyBytes(key)) {
			return [32]byte{}
		}
	}
	for x, w := range p.boundExecutions {
		if !add("bound" + terminalRepositoryKeyBytes(x) + terminalRepositoryKeyBytes(w)) {
			return [32]byte{}
		}
	}
	sort.Strings(rows)
	for _, row := range rows {
		terminalRepositoryWrite(h, row)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
func terminalRepositoryKeyBytes(k terminalHistoricalKey) string {
	return string(mustTerminalJSON(struct {
		Database    journalv2.DatabaseID
		Coordinate  journalv2.AggregateCoordinate
		Version     journalv2.LogicalVersion
		Revision    uint64
		Event       journalv2.EventID
		Publication journalv2.PublicationID
	}{k.database, k.coordinate, k.version, k.revision, k.event, k.publication}))
}
func terminalRepositoryCanonicalRoot(s string) bool {
	return s != "/" && filepath.IsAbs(s) && filepath.Clean(s) == s && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && len(s) <= workspace.MaxWorkspacePathBytesV2
}
func terminalRepositoryWithin(a, b string) bool {
	rel, err := filepath.Rel(b, a)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func terminalRepositoryHex(s string, sizes ...int) bool {
	valid := false
	for _, n := range sizes {
		valid = valid || len(s) == n
	}
	_, err := hex.DecodeString(s)
	return valid && err == nil && strings.ToLower(s) == s
}
func (c terminalRepositoryCapture) validate(p *terminalCoordinateProof) error {
	sum, err := c.commitment()
	if !c.present || err != nil || sum == ([32]byte{}) || sum != c.integrity || c.historySHA != p.historySHA || c.capture != p.capture || c.graph != terminalRepositoryGraphIntegrity(p) {
		return fmt.Errorf("repository proof: missing/mutated/mixed original capture")
	}
	b := c.binding
	if _, err := ids.ParseRepositoryID(b.RepositoryID.String()); err != nil {
		return err
	}
	if _, err := ids.ParseProjectID(b.ProjectID.String()); err != nil {
		return err
	}
	if b.ProjectID != ids.ProjectID(p.scope.ProjectID) || !terminalRepositoryCanonicalRoot(b.PrimaryPath) || !terminalRepositoryCanonicalRoot(b.CommonDir) || !terminalRepositoryCanonicalRoot(b.ManagedRoot) || b.PrimaryPath != c.primaryDeclaration || b.ManagedRoot != c.managedDeclaration {
		return fmt.Errorf("repository proof: configured owner/declaration")
	}
	for _, root := range []string{b.PrimaryPath, b.CommonDir} {
		if terminalRepositoryWithin(root, b.ManagedRoot) || terminalRepositoryWithin(b.ManagedRoot, root) {
			return fmt.Errorf("repository proof: configured root overlap")
		}
	}
	// A linked primary's common directory is allowed outside its checkout.
	// Target and initial snapshot are retained verbatim, not compared to later Git.
	if b.TargetRef == "" || !terminalRepositoryHex(b.Primary.Head, 40, 64) || !terminalRepositoryHex(b.Primary.IndexDigest, 64) || !terminalRepositoryHex(b.Primary.TreeDigest, 64) {
		return fmt.Errorf("repository proof: incomplete original snapshot")
	}
	return nil
}

type terminalRepositoryEntry struct {
	canonical     []byte
	digest        journalv2.Digest
	claim         journalv2.WorkspacePathClaimV2
	claimRevision uint64
}
type terminalRepositoryProof struct {
	origin  terminalRepositoryCapture
	entries map[terminalHistoricalKey]terminalRepositoryEntry
}
type terminalRepositoryReference struct {
	historySHA string
	capture    journalv2.StoreRevisionV2
	origin     [32]byte
	key        terminalHistoricalKey
	digest     journalv2.Digest
}
type terminalRepositoryWorkspace struct {
	key           terminalHistoricalKey
	configured    workspace.RepositoryBinding
	canonical     []byte
	claim         journalv2.WorkspacePathClaimV2
	claimRevision uint64
}

// No owner/root parameter: consumes only the same construction's validated
// graph and optional original capture. EVERY retained workspace version closes.
func newTerminalRepositoryProof(p *terminalCoordinateProof) (*terminalRepositoryProof, error) {
	if p == nil {
		return nil, fmt.Errorf("repository proof: missing graph")
	}
	c := p.repositoryCapture
	if err := c.validate(p); err != nil {
		return nil, err
	}
	result := &terminalRepositoryProof{origin: c, entries: make(map[terminalHistoricalKey]terminalRepositoryEntry)}
	for coordinate, w := range p.workspaces {
		if len(w.versions) == 0 {
			return nil, fmt.Errorf("repository proof: empty inventory")
		}
		for _, key := range w.versions {
			s, ok := p.states[key]
			state, err := workspace.DecodeStateV2(s.canonical)
			digest, digestErr := journalv2.ReducerStateDigestV2(s.canonical)
			if !ok || err != nil || digestErr != nil || digest != s.digest || s.key != key || key.database != c.capture.DatabaseID() || key.coordinate != coordinate || state.LogicalVersion != uint64(key.version) || key.revision > c.capture.CurrentRevision() {
				return nil, fmt.Errorf("repository proof: nonexact workspace version")
			}
			b := state.Binding
			if b.Validate() != nil || b.RepositoryID != c.binding.RepositoryID || b.ProjectID != c.binding.ProjectID || b.ManagedRoot != c.binding.ManagedRoot || filepath.Join(c.binding.ManagedRoot, b.WorkspaceID.String()) != b.CanonicalPath || b.Generation > math.MaxInt64 {
				return nil, fmt.Errorf("repository proof: workspace configuration/layout/adapter generation")
			}
			if w.claimRevision == 0 || w.claimRevision > key.revision || w.claim.ClaimDigest() != b.PathClaimDigest {
				return nil, fmt.Errorf("repository proof: original claim")
			}
			if _, duplicate := result.entries[key]; duplicate {
				return nil, fmt.Errorf("repository proof: duplicate workspace version")
			}
			claim, err := journalv2.NewWorkspacePathClaimV2(w.claim.ClaimDigest(), w.claim.CanonicalValue())
			if err != nil {
				return nil, err
			}
			result.entries[key] = terminalRepositoryEntry{append([]byte(nil), s.canonical...), s.digest, claim, w.claimRevision}
		}
	}
	return result, nil
}
func (p *terminalRepositoryProof) resolve(ref terminalRepositoryReference) (terminalRepositoryWorkspace, error) {
	if p == nil || ref.historySHA != p.origin.historySHA || ref.capture != p.origin.capture || ref.origin != p.origin.integrity {
		return terminalRepositoryWorkspace{}, fmt.Errorf("repository lookup: cross capture/history/origin")
	}
	sum, err := p.origin.commitment()
	if err != nil || sum != p.origin.integrity {
		return terminalRepositoryWorkspace{}, fmt.Errorf("repository lookup: mutated origin")
	}
	entry, ok := p.entries[ref.key]
	digest, err := journalv2.ReducerStateDigestV2(entry.canonical)
	if !ok || err != nil || digest != entry.digest || digest != ref.digest {
		return terminalRepositoryWorkspace{}, fmt.Errorf("repository lookup: nonexact full historical key/digest")
	}
	claim, err := journalv2.NewWorkspacePathClaimV2(entry.claim.ClaimDigest(), entry.claim.CanonicalValue())
	if err != nil {
		return terminalRepositoryWorkspace{}, err
	}
	return terminalRepositoryWorkspace{ref.key, p.origin.binding, bytes.Clone(entry.canonical), claim, entry.claimRevision}, nil
}
