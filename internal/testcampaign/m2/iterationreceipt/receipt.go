package iterationreceipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	SchemaVersion   = 1
	Prefix          = "SPK_M2_ITERATION_V1 "
	FamilyCutPrefix = "SPK_M2_FAMILY_CUT_V1 "
	maxText         = 256
	maxEvidence     = 16 << 10
)

// FamilyCutRecord is emitted by the executing family-bound coordinator
// iteration. Unlike a composed matrix assertion, it retains the exact sealed
// production plan identity and observed emitted family set used at the cut.
type FamilyCutRecord struct {
	SchemaVersion       int             `json:"schema_version"`
	DefinitionID        string          `json:"definition_id"`
	Family              string          `json:"family"`
	Cut                 string          `json:"cut"`
	Index               int             `json:"index"`
	Seed                string          `json:"seed"`
	PlanSHA256          string          `json:"plan_sha256"`
	PublicationID       string          `json:"publication_id"`
	EmittedFamilies     []string        `json:"emitted_families"`
	Outcome             string          `json:"outcome"`
	DurationNanoseconds int64           `json:"duration_nanoseconds"`
	ObservedFacts       json.RawMessage `json:"observed_facts"`
	ObservedFactsSHA256 string          `json:"observed_facts_sha256"`
	InvariantEvidence   json.RawMessage `json:"invariant_evidence"`
	InvariantSHA256     string          `json:"invariant_sha256"`
}

func NewFamilyCut(definitionID, family, cut string, index int, seed, planSHA, publicationID string, emitted []string, outcome string, duration time.Duration, facts, invariants any) (FamilyCutRecord, error) {
	factBytes, err := canonicalJSON(facts)
	if err != nil {
		return FamilyCutRecord{}, err
	}
	invariantBytes, err := canonicalJSON(invariants)
	if err != nil {
		return FamilyCutRecord{}, err
	}
	factSum, invariantSum := sha256.Sum256(factBytes), sha256.Sum256(invariantBytes)
	record := FamilyCutRecord{SchemaVersion: SchemaVersion, DefinitionID: definitionID, Family: family, Cut: cut, Index: index, Seed: seed, PlanSHA256: planSHA, PublicationID: publicationID, EmittedFamilies: append([]string(nil), emitted...), Outcome: outcome, DurationNanoseconds: duration.Nanoseconds(), ObservedFacts: factBytes, ObservedFactsSHA256: hex.EncodeToString(factSum[:]), InvariantEvidence: invariantBytes, InvariantSHA256: hex.EncodeToString(invariantSum[:])}
	return record, record.Validate()
}

func (r FamilyCutRecord) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.Index < 0 || !bounded(r.DefinitionID) || !bounded(r.Family) || !bounded(r.Cut) || !bounded(r.Seed) || !bounded(r.Outcome) || r.DurationNanoseconds <= 0 || len(r.EmittedFamilies) == 0 || !digestText(r.PlanSHA256) || !digestText(r.PublicationID) {
		return fmt.Errorf("invalid M2 family/cut receipt")
	}
	for index, family := range r.EmittedFamilies {
		if !bounded(family) || index > 0 && r.EmittedFamilies[index-1] >= family {
			return fmt.Errorf("invalid M2 family/cut family census")
		}
	}
	for _, pair := range []struct {
		raw    json.RawMessage
		digest string
	}{{r.ObservedFacts, r.ObservedFactsSHA256}, {r.InvariantEvidence, r.InvariantSHA256}} {
		canonical, err := canonicalJSON(json.RawMessage(pair.raw))
		if err != nil || len(pair.raw) == 0 || len(pair.raw) > maxEvidence || !jsonEqual(canonical, pair.raw) || !digestText(pair.digest) {
			return fmt.Errorf("invalid M2 family/cut evidence")
		}
		sum := sha256.Sum256(canonical)
		if hex.EncodeToString(sum[:]) != pair.digest {
			return fmt.Errorf("invalid M2 family/cut evidence digest")
		}
	}
	return nil
}

func EncodeFamilyCut(r FamilyCutRecord) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return canonicalJSON(r)
}

func EmitFamilyCut(log Logger, record FamilyCutRecord) {
	log.Helper()
	encoded, err := EncodeFamilyCut(record)
	if err != nil {
		panic(err)
	}
	log.Logf("%s%s", FamilyCutPrefix, encoded)
}

func digestText(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

// Record is emitted by the executing test iteration. It binds the actual
// scheduling/input seed, observed typed outcome, duration, canonical observed
// facts, and the universal-invariant result before a campaign can certify it.
type Record struct {
	SchemaVersion           int             `json:"schema_version"`
	DefinitionID            string          `json:"definition_id"`
	Index                   int             `json:"index"`
	Seed                    string          `json:"seed"`
	FaultBoundary           string          `json:"fault_boundary"`
	Outcome                 string          `json:"outcome"`
	DurationNanoseconds     int64           `json:"duration_nanoseconds"`
	ObservedFacts           json.RawMessage `json:"observed_facts"`
	ObservedFactsSHA256     string          `json:"observed_facts_sha256"`
	InvariantEvidence       json.RawMessage `json:"invariant_evidence"`
	InvariantEvidenceSHA256 string          `json:"invariant_evidence_sha256"`
	InvariantResult         string          `json:"invariant_result"`
}

func New(definitionID string, index int, seed, boundary, outcome string, duration time.Duration, observedFacts, invariantEvidence any) (Record, error) {
	facts, err := canonicalJSON(observedFacts)
	if err != nil {
		return Record{}, fmt.Errorf("canonical observed facts: %w", err)
	}
	sum := sha256.Sum256(facts)
	invariants, err := canonicalJSON(invariantEvidence)
	if err != nil {
		return Record{}, fmt.Errorf("canonical invariant evidence: %w", err)
	}
	invariantSum := sha256.Sum256(invariants)
	record := Record{
		SchemaVersion: SchemaVersion, DefinitionID: definitionID, Index: index,
		Seed: seed, FaultBoundary: boundary, Outcome: outcome,
		DurationNanoseconds: duration.Nanoseconds(), ObservedFacts: facts, ObservedFactsSHA256: hex.EncodeToString(sum[:]),
		InvariantEvidence: invariants, InvariantEvidenceSHA256: hex.EncodeToString(invariantSum[:]), InvariantResult: "pass",
	}
	return record, record.Validate()
}

// DefinitionID lets the qualifying runner bind a reusable campaign test to
// its exact closed census entry without changing product behavior.
func DefinitionID(fallback string) string {
	if value := os.Getenv("SPEC_KITTY_M2_DEFINITION_ID"); value != "" {
		return value
	}
	return fallback
}

func (r Record) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.Index < 0 || !bounded(r.DefinitionID) || !bounded(r.Seed) || !bounded(r.FaultBoundary) || !bounded(r.Outcome) || r.DurationNanoseconds <= 0 || !bounded(r.InvariantResult) || r.InvariantResult != "pass" {
		return fmt.Errorf("invalid M2 iteration receipt")
	}
	for field, pair := range map[string]struct {
		raw    json.RawMessage
		digest string
	}{
		"observed-facts":     {r.ObservedFacts, r.ObservedFactsSHA256},
		"invariant-evidence": {r.InvariantEvidence, r.InvariantEvidenceSHA256},
	} {
		canonical, err := canonicalJSON(json.RawMessage(pair.raw))
		if err != nil || len(pair.raw) == 0 || len(pair.raw) > maxEvidence || !jsonEqual(canonical, pair.raw) || len(pair.digest) != sha256.Size*2 {
			return fmt.Errorf("invalid %s evidence", field)
		}
		sum := sha256.Sum256(canonical)
		if hex.EncodeToString(sum[:]) != pair.digest {
			return fmt.Errorf("invalid %s digest", field)
		}
	}
	return nil
}

func Encode(r Record) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return canonicalJSON(r)
}

type Logger interface {
	Helper()
	Logf(string, ...any)
}

func Emit(log Logger, record Record) {
	log.Helper()
	encoded, err := Encode(record)
	if err != nil {
		panic(err)
	}
	log.Logf("%s%s", Prefix, encoded)
}

func bounded(value string) bool {
	return value != "" && len(value) <= maxText && strings.TrimSpace(value) == value
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func jsonEqual(left, right []byte) bool { return string(left) == string(right) }
