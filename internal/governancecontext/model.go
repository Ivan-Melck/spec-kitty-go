package governancecontext

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const (
	SchemaVersion   uint16 = 1
	AlgorithmSHA256        = "sha256"
)

type ErrorCode string

const (
	CodeInvalidAlgorithm  ErrorCode = "invalid_algorithm"
	CodeInvalidDigest     ErrorCode = "invalid_digest"
	CodeInvalidEntryKind  ErrorCode = "invalid_entry_kind"
	CodeInvalidRef        ErrorCode = "invalid_ref"
	CodeInvalidRevision   ErrorCode = "invalid_revision"
	CodeDuplicateEntry    ErrorCode = "duplicate_entry"
	CodeUnsupportedSchema ErrorCode = "unsupported_schema"
	CodeEmptyEntries      ErrorCode = "empty_entries"
	CodeInvalidContextID  ErrorCode = "invalid_context_id"
	CodeInvalidCreatedAt  ErrorCode = "invalid_created_at"
	CodeAggregateMismatch ErrorCode = "aggregate_mismatch"
)

type ValidationError struct {
	Code    ErrorCode
	Field   string
	Message string
	Cause   error
}

func (e *ValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("governance context %s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("governance context %s: %s", e.Code, e.Message)
}

func (e *ValidationError) Unwrap() error { return e.Cause }

type Commitment struct {
	algorithm string
	digest    string
}

func NewCommitment(algorithm, digest string) (Commitment, error) {
	value := Commitment{algorithm: algorithm, digest: digest}
	if err := value.Validate(); err != nil {
		return Commitment{}, err
	}
	return value, nil
}

func (c Commitment) Algorithm() string { return c.algorithm }
func (c Commitment) Digest() string    { return c.digest }

func (c Commitment) Validate() error {
	if c.algorithm != AlgorithmSHA256 {
		return &ValidationError{Code: CodeInvalidAlgorithm, Field: "algorithm", Message: "algorithm must be explicitly sha256"}
	}
	if !validSHA256Digest.MatchString(c.digest) {
		return &ValidationError{Code: CodeInvalidDigest, Field: "digest", Message: "sha256 digest must be 64 lowercase hexadecimal characters"}
	}
	return nil
}

type EntryKind string

const (
	KindConfig      EntryKind = "config"
	KindDoctrine    EntryKind = "doctrine"
	KindProfile     EntryKind = "profile"
	KindMissionType EntryKind = "mission_type"
)

var validSHA256Digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Entry kinds are intentionally closed. A future portable kind is an explicit
// model and commitment-schema extension, not mutable process-global state.
func validEntryKind(kind EntryKind) bool {
	switch kind {
	case KindConfig, KindDoctrine, KindProfile, KindMissionType:
		return true
	default:
		return false
	}
}

type Entry struct {
	kind       EntryKind
	ref        string
	revision   string
	commitment Commitment
}

func NewEntry(kind EntryKind, ref, revision string, commitment Commitment) (Entry, error) {
	entry := Entry{kind: kind, ref: ref, revision: revision, commitment: commitment}
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (e Entry) Kind() EntryKind        { return e.kind }
func (e Entry) Ref() string            { return e.ref }
func (e Entry) Revision() string       { return e.revision }
func (e Entry) Commitment() Commitment { return e.commitment }

func (e Entry) Validate() error {
	if !validEntryKind(e.kind) {
		return &ValidationError{Code: CodeInvalidEntryKind, Field: "kind", Message: fmt.Sprintf("entry kind %q is not registered", e.kind)}
	}
	if strings.TrimSpace(e.ref) == "" {
		return &ValidationError{Code: CodeInvalidRef, Field: "ref", Message: "entry ref must not be blank"}
	}
	if strings.TrimSpace(e.revision) == "" {
		return &ValidationError{Code: CodeInvalidRevision, Field: "revision", Message: "entry revision must not be blank"}
	}
	return e.commitment.Validate()
}

type Context struct {
	id            ids.GovernanceContextID
	schemaVersion uint16
	entries       []Entry
	aggregate     Commitment
	createdAt     time.Time
}

func NewContext(id ids.GovernanceContextID, schemaVersion uint16, entries []Entry, createdAt time.Time) (Context, error) {
	aggregate, err := ComputeAggregate(schemaVersion, entries)
	if err != nil {
		return Context{}, err
	}
	return RestoreContext(id, schemaVersion, entries, aggregate, createdAt)
}

// RestoreContext validates a context read from a boundary and verifies its
// claimed aggregate by recomputation before making the value available.
func RestoreContext(id ids.GovernanceContextID, schemaVersion uint16, entries []Entry, aggregate Commitment, createdAt time.Time) (Context, error) {
	value := Context{
		id: id, schemaVersion: schemaVersion, entries: append([]Entry(nil), entries...),
		aggregate: aggregate, createdAt: createdAt.UTC(),
	}
	if err := value.Validate(); err != nil {
		return Context{}, err
	}
	return value, nil
}

func (c Context) ID() ids.GovernanceContextID { return c.id }
func (c Context) SchemaVersion() uint16       { return c.schemaVersion }
func (c Context) Aggregate() Commitment       { return c.aggregate }
func (c Context) CreatedAt() time.Time        { return c.createdAt }
func (c Context) Entries() []Entry            { return append([]Entry(nil), c.entries...) }

func (c Context) Validate() error {
	if _, err := ids.ParseGovernanceContextID(c.id.String()); err != nil {
		return &ValidationError{Code: CodeInvalidContextID, Field: "id", Message: "context ID must be a portable typed identity", Cause: err}
	}
	if c.schemaVersion != SchemaVersion {
		return &ValidationError{Code: CodeUnsupportedSchema, Field: "schema_version", Message: fmt.Sprintf("schema version must be %d", SchemaVersion)}
	}
	if len(c.entries) == 0 {
		return &ValidationError{Code: CodeEmptyEntries, Field: "entries", Message: "context must contain at least one ordered entry"}
	}
	if c.createdAt.IsZero() {
		return &ValidationError{Code: CodeInvalidCreatedAt, Field: "created_at", Message: "created timestamp must be non-zero"}
	}
	if err := validateEntries(c.entries); err != nil {
		return err
	}
	if err := c.aggregate.Validate(); err != nil {
		return err
	}
	expected, err := computeAggregate(c.schemaVersion, c.entries)
	if err != nil {
		return err
	}
	if expected != c.aggregate {
		return &ValidationError{Code: CodeAggregateMismatch, Field: "aggregate", Message: "claimed aggregate does not match the ordered context entries"}
	}
	return nil
}

func validateEntries(entries []Entry) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		identity := string(entry.kind) + "\x00" + entry.ref
		if _, exists := seen[identity]; exists {
			return &ValidationError{Code: CodeDuplicateEntry, Field: "entries", Message: fmt.Sprintf("duplicate entry identity (%s, %s)", entry.kind, entry.ref)}
		}
		seen[identity] = struct{}{}
	}
	return nil
}
