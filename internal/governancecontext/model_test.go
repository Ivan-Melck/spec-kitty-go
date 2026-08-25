package governancecontext

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestCommitmentValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		algorithm string
		digest    string
		code      ErrorCode
	}{
		{name: "missing algorithm", digest: digestOf('a'), code: CodeInvalidAlgorithm},
		{name: "unsupported algorithm", algorithm: "sha512", digest: digestOf('a'), code: CodeInvalidAlgorithm},
		{name: "blank digest", algorithm: AlgorithmSHA256, code: CodeInvalidDigest},
		{name: "short digest", algorithm: AlgorithmSHA256, digest: "abcd", code: CodeInvalidDigest},
		{name: "uppercase digest", algorithm: AlgorithmSHA256, digest: digestOf('A'), code: CodeInvalidDigest},
		{name: "non hex digest", algorithm: AlgorithmSHA256, digest: digestOf('z'), code: CodeInvalidDigest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewCommitment(test.algorithm, test.digest)
			assertValidationCode(t, err, test.code)
		})
	}
}

func TestContextValidationRejectsInvalidShape(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	validEntry := entryFixture(t, KindConfig, "project-config", "revision-1", "config")
	tests := []struct {
		name    string
		id      ids.GovernanceContextID
		schema  uint16
		entries []Entry
		created time.Time
		code    ErrorCode
	}{
		{name: "invalid id", schema: SchemaVersion, entries: []Entry{validEntry}, created: now, code: CodeInvalidContextID},
		{name: "unsupported schema", id: "context-1", schema: 2, entries: []Entry{validEntry}, created: now, code: CodeUnsupportedSchema},
		{name: "empty entries", id: "context-1", schema: SchemaVersion, created: now, code: CodeEmptyEntries},
		{name: "missing timestamp", id: "context-1", schema: SchemaVersion, entries: []Entry{validEntry}, code: CodeInvalidCreatedAt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewContext(test.id, test.schema, test.entries, test.created)
			assertValidationCode(t, err, test.code)
		})
	}
}

func TestEntryRejectsBlankIdentityAndDuplicateIdentity(t *testing.T) {
	t.Parallel()

	commitment := commitmentFixture(t, "content")
	if _, err := NewEntry(KindConfig, "", "revision-1", commitment); err == nil {
		t.Fatal("blank ref unexpectedly accepted")
	} else {
		assertValidationCode(t, err, CodeInvalidRef)
	}
	if _, err := NewEntry(KindConfig, "config", " ", commitment); err == nil {
		t.Fatal("blank revision unexpectedly accepted")
	} else {
		assertValidationCode(t, err, CodeInvalidRevision)
	}

	first := entryFixture(t, KindDoctrine, "directive-010", "revision-1", "first")
	duplicate := entryFixture(t, KindDoctrine, "directive-010", "revision-2", "second")
	_, err := NewContext("context-1", SchemaVersion, []Entry{first, duplicate}, time.Now().UTC())
	assertValidationCode(t, err, CodeDuplicateEntry)
}

func TestEntryKindsRequireExplicitRegistration(t *testing.T) {
	t.Parallel()

	commitment := commitmentFixture(t, "content")
	if _, err := NewEntry("provider_prompt", "prompt", "revision-1", commitment); err == nil {
		t.Fatal("unregistered kind unexpectedly accepted")
	} else {
		assertValidationCode(t, err, CodeInvalidEntryKind)
	}
	kind, err := RegisterEntryKind("provider_prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEntry(kind, "prompt", "revision-1", commitment); err != nil {
		t.Fatalf("registered extension rejected: %v", err)
	}
}

func TestContextCopiesCallerEntriesAndReturnedEntries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	original := entryFixture(t, KindConfig, "config", "revision-1", "original")
	mutated := entryFixture(t, KindProfile, "profile", "revision-2", "mutated")
	input := []Entry{original}
	contextValue, err := NewContext("context-1", SchemaVersion, input, now)
	if err != nil {
		t.Fatal(err)
	}
	want := contextValue.Aggregate()

	input[0] = mutated
	returned := contextValue.Entries()
	returned[0] = mutated
	if got := contextValue.Entries()[0]; got != original {
		t.Fatalf("stored entry changed: got=%+v want=%+v", got, original)
	}
	if contextValue.Aggregate() != want {
		t.Fatalf("aggregate changed after caller mutation: got=%+v want=%+v", contextValue.Aggregate(), want)
	}
}

func TestRestoreContextRejectsClaimedAggregateMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	entry := entryFixture(t, KindMissionType, "software-dev", "1", "mission type")
	wrong := commitmentFixture(t, "wrong aggregate")
	_, err := RestoreContext("context-1", SchemaVersion, []Entry{entry}, wrong, now)
	assertValidationCode(t, err, CodeAggregateMismatch)
}

func TestContextJSONRoundTripRevalidatesAndPreservesOrder(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	want, err := NewContext("context-1", SchemaVersion, contextEntriesFixture(t), now)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Context
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID() != want.ID() || got.Aggregate() != want.Aggregate() || got.CreatedAt() != want.CreatedAt() || !entriesEqual(got.Entries(), want.Entries()) {
		t.Fatalf("round trip=%+v want=%+v", got, want)
	}
}

func TestContextIdentityConflictIsStableAndTyped(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	first, err := NewContext("context-1", SchemaVersion, []Entry{entryFixture(t, KindConfig, "config", "1", "first")}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewContext("context-1", SchemaVersion, []Entry{entryFixture(t, KindConfig, "config", "1", "second")}, now)
	if err != nil {
		t.Fatal(err)
	}
	err = CheckIdentityConflict(first, second)
	var conflict *IdentityConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrIdentityConflict) || conflict.ID != "context-1" {
		t.Fatalf("conflict=%#v", err)
	}
	sameContentLater, err := NewContext("context-1", SchemaVersion, first.Entries(), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityConflict(first, sameContentLater); err != nil {
		t.Fatalf("timestamp-only change reported as conflict: %v", err)
	}
}

func commitmentFixture(t *testing.T, content string) Commitment {
	t.Helper()
	commitment, err := CommitSHA256([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return commitment
}

func entryFixture(t *testing.T, kind EntryKind, ref, revision, content string) Entry {
	t.Helper()
	entry, err := NewEntry(kind, ref, revision, commitmentFixture(t, content))
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func assertValidationCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != code {
		t.Fatalf("error=%#v, want validation code %q", err, code)
	}
}

func digestOf(value byte) string {
	buffer := make([]byte, 64)
	for index := range buffer {
		buffer[index] = value
	}
	return string(buffer)
}
