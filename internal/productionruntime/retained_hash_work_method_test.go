package productionruntime

// Provider-free author tests of scalar/report algorithms and filesystem refusal.
// Synthetic report/proof VALUES below are NOT production proofs, ACKs, provider
// observations, structural Hosts, source custody authentication or native credit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/projection"
)

func hashWorkModelAttribution(t *testing.T, root int) projection.AttributionV2 {
	t.Helper()
	digest, err := journalv2.ReducerStateDigestV2([]byte(fmt.Sprintf(`{"report_model_root":%d}`, root)))
	if err != nil {
		t.Fatal(err)
	}
	return projection.AttributionV2{DatabaseID: journalv2.DatabaseID(fmt.Sprintf("model-db-%d", root)), RuntimeEpochID: journalv2.RuntimeEpochID(fmt.Sprintf("model-epoch-%d", root)), Revision: 257, DurableThrough: 257, CursorToken: digest}
}
func hashWorkModelSample(t *testing.T, c hashWorkCase, i int) hashWorkSample {
	t.Helper()
	a := hashWorkModelAttribution(t, c.Root)
	s := hashWorkSample{Index: i, Nanoseconds: int64(i + 1), ExpectedOutcome: c.ExpectedOutcome, ObservedOutcome: c.ExpectedOutcome, ExpectedOutcomeMet: true, ACKs: c.ACKs, Before: a, After: a, Invariants: hashWorkInvariant{true, true, true, true, true, true, true, true}}
	if c.ExpectedOutcome == "matched" {
		s.Matched, s.PublicationRows, s.PrivateRows, s.TotalRows, s.ProofAttribution = true, 1951, 6, 1957, a
		s.ErrorType = "<nil>"
	} else {
		s.ProofZero, s.ErrorPresent, s.ErrorType = true, true, "*productionruntime.ProgressError"
		if c.ExpectedOutcome == "canceled" {
			s.ErrorType = "*errors.errorString"
		}
	}
	return s
}

func TestHashWorkMethodCasesAndArithmetic(t *testing.T) {
	want := []hashWorkCase{
		{Name: "pub1951_match", Root: 0, ExpectedOutcome: "matched", ACKs: 256, Rows: 1951, ReservedCalls: 2149, ReservedHashes: 6448, ReservedHashBytes: 34349308448, PlannerAccepts: true},
		{Name: "pub1951_already_canceled", Root: 0, ExpectedOutcome: "canceled", ACKs: 256, Rows: 1951, ReservedCalls: 2149, ReservedHashes: 6448, ReservedHashBytes: 34349308448, PlannerAccepts: true},
		{Name: "ack257_scalar_refuse", Root: 0, ExpectedOutcome: "sanitized_refusal", ACKs: 257, Rows: 1951, ReservedCalls: 2149, ReservedHashes: 6448, ReservedHashBytes: 34349308448, PlannerAccepts: true},
		{Name: "pub1952_hash_preflight_refuse", Root: 1, ExpectedOutcome: "sanitized_refusal", ACKs: 256, Rows: 1952, ReservedCalls: 2150, ReservedHashes: 6451, ReservedHashBytes: 34365289826},
	}
	if got := hashWorkCases(); !reflect.DeepEqual(got, want) || hashWorkSamples != 30 {
		t.Fatalf("case/sample contract: %+v", got)
	}
	if hashWorkCeiling != 34359738368 || hashWorkImageBytes != 5327126 || hashWorkCeiling/hashWorkImageBytes != 6449 || hashWorkCeiling-34349308448 != 10429920 || 34365289826-hashWorkCeiling != 5551458 {
		t.Fatal("reservation boundary arithmetic")
	}
	actualCalls := uint64(1951 + 6 + 3)
	actualHashes := 3*actualCalls + 1
	if actualCalls != 1960 || actualHashes != 5881 || actualHashes*hashWorkImageBytes != 31328828006 || actualHashes >= 6448 {
		t.Fatal("actual-private-six nominal arithmetic, NOT measured work")
	}
	for _, p := range [][2]uint64{{0, 0}, {0, 1951}, {1, 4097}, {1, math.MaxUint64}, {math.MaxUint64, 1951}} {
		if _, _, _, ok := hashWorkPlanWork(p[0], p[1]); ok {
			t.Fatal("zero/overflow/over-cap accepted", p)
		}
	}
	for _, rows := range []uint64{0, 1, 1951, 1952, 4096} {
		hashes := 3*(rows+198) + 1
		image := hashWorkCeiling / hashes
		if _, _, _, ok := hashWorkPlanWork(image, rows); !ok {
			t.Fatal("exact quotient refused")
		}
		if _, _, _, ok := hashWorkPlanWork(image+1, rows); ok {
			t.Fatal("quotient plus one accepted")
		}
	}
}

func TestHashWorkMethodObserved(t *testing.T) {
	a := hashWorkModelAttribution(t, 0)
	nonzero := []RetainedRecordsProof{{publications: 1951, private: 6, attribution: a}, {publications: 1}, {private: 1}, {attribution: a}}
	faults := []error{context.Canceled, fmt.Errorf("wrapped: %w", context.Canceled), context.DeadlineExceeded, fmt.Errorf("wrapped: %w", context.DeadlineExceeded), proofIncomplete("retained_records"), errors.New("untrusted error")}
	for i, p := range nonzero {
		if got := hashWorkObserved(p, nil); got != "matched" {
			t.Fatal("nonzero success classification", i, got)
		}
		for _, err := range faults {
			if got := hashWorkObserved(p, err); got != "nonzero_error_proof" {
				t.Fatal("nonzero error proof", i, got)
			}
		}
	}
	var typedNil *ProgressError
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"all_zero_success", nil, "zero_success"},
		{"exact_cancel", context.Canceled, "canceled"},
		{"wrapped_cancel", fmt.Errorf("wrapped: %w", context.Canceled), "other_error"},
		{"joined_cancel", errors.Join(context.Canceled, errors.New("extra")), "other_error"},
		{"cancel_text", errors.New(context.Canceled.Error()), "other_error"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
		{"wrapped_deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), "deadline_exceeded"},
		{"deadline_text", errors.New(context.DeadlineExceeded.Error()), "other_error"},
		{"sanitized", proofIncomplete("retained_records"), "sanitized_refusal"},
		{"wrapped_sanitized", fmt.Errorf("wrapped: %w", proofIncomplete("retained_records")), "other_error"},
		{"sanitized_text", errors.New("PROGRESS_INCOMPLETE: retained_records"), "other_error"},
		{"typed_nil", typedNil, "other_error"},
		{"wrong_kind", &ProgressError{Kind: ProgressInvalidQuery, Field: "retained_records"}, "other_error"},
		{"empty_kind", &ProgressError{Field: "retained_records"}, "other_error"},
		{"empty_field", proofIncomplete(""), "other_error"},
		{"wrong_field", proofIncomplete("publication"), "other_error"},
		{"field_suffix", proofIncomplete("retained_records "), "other_error"},
		{"field_case", proofIncomplete("Retained_records"), "other_error"},
		{"field_diagnostic", proofIncomplete("retained_records: secret"), "other_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hashWorkObserved(RetainedRecordsProof{}, tc.err); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestHashWorkMethodExpectedMutations(t *testing.T) {
	for _, c := range hashWorkCases() {
		t.Run(c.Name, func(t *testing.T) {
			base := hashWorkModelSample(t, c, 0)
			if !hashWorkExpected(c, base) {
				t.Fatal("valid scalar model rejected")
			}
			check := func(name string, mutate func(*hashWorkSample)) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					s := base
					mutate(&s)
					if hashWorkExpected(c, s) {
						t.Fatal("mutation accepted")
					}
				})
			}
			for _, index := range []int{-1, 30, math.MaxInt} {
				check(fmt.Sprintf("index_%d", index), func(s *hashWorkSample) { s.Index = index })
			}
			for _, ns := range []int64{0, -1, math.MinInt64} {
				check(fmt.Sprintf("time_%d", ns), func(s *hashWorkSample) { s.Nanoseconds = ns })
			}
			for _, i := range []int{0, 29} {
				s := base
				s.Index = i
				if !hashWorkExpected(c, s) {
					t.Fatal("valid endpoint index refused")
				}
			}
			for i := 0; i < reflect.TypeOf(base.Invariants).NumField(); i++ {
				check("invariant_"+reflect.TypeOf(base.Invariants).Field(i).Name, func(s *hashWorkSample) { reflect.ValueOf(&s.Invariants).Elem().Field(i).SetBool(false) })
			}
			for _, outcome := range []string{"matched", "canceled", "sanitized_refusal", "zero_success", "nonzero_error_proof", "deadline_exceeded", "other_error", ""} {
				if outcome != c.ExpectedOutcome {
					check("observed_"+outcome, func(s *hashWorkSample) { s.ObservedOutcome = outcome })
					check("expected_"+outcome, func(s *hashWorkSample) { s.ExpectedOutcome = outcome })
				}
			}
			check("acks", func(s *hashWorkSample) { s.ACKs++ })
			check("matched", func(s *hashWorkSample) { s.Matched = !s.Matched })
			check("zero_proof", func(s *hashWorkSample) { s.ProofZero = !s.ProofZero })
			check("error_present", func(s *hashWorkSample) { s.ErrorPresent = !s.ErrorPresent })
			for _, field := range []string{"PublicationRows", "PrivateRows", "TotalRows"} {
				for _, value := range []uint64{0, 1, 6, 1951, 1957, math.MaxUint64} {
					if reflect.ValueOf(base).FieldByName(field).Uint() == value {
						continue
					}
					check(fmt.Sprintf("%s_%d", field, value), func(s *hashWorkSample) { reflect.ValueOf(s).Elem().FieldByName(field).SetUint(value) })
				}
			}
			for _, field := range []string{"DatabaseID", "RuntimeEpochID", "Revision", "DurableThrough", "CursorToken"} {
				check("after_"+field, func(s *hashWorkSample) { v := reflect.ValueOf(&s.After).Elem().FieldByName(field); v.SetZero() })
				check("before_"+field, func(s *hashWorkSample) { v := reflect.ValueOf(&s.Before).Elem().FieldByName(field); v.SetZero() })
				check("empty_coherent_"+field, func(s *hashWorkSample) {
					v := reflect.ValueOf(&s.Before).Elem().FieldByName(field)
					v.SetZero()
					s.After = s.Before
					if c.ExpectedOutcome == "matched" {
						s.ProofAttribution = s.Before
					}
				})
				check("proof_attribution_"+field, func(s *hashWorkSample) {
					if c.ExpectedOutcome == "matched" {
						reflect.ValueOf(&s.ProofAttribution).Elem().FieldByName(field).SetZero()
					} else {
						reflect.ValueOf(&s.ProofAttribution).Elem().FieldByName(field).Set(reflect.ValueOf(base.Before).FieldByName(field))
					}
				})
			}
			check("durability_behind", func(s *hashWorkSample) { s.Before.DurableThrough--; s.After = s.Before; s.ProofAttribution = s.Before })
			check("durability_ahead", func(s *hashWorkSample) { s.Before.DurableThrough++; s.After = s.Before; s.ProofAttribution = s.Before })
			bad := c
			bad.ExpectedOutcome = "unknown"
			s := base
			s.ExpectedOutcome = "unknown"
			s.ObservedOutcome = "unknown"
			if hashWorkExpected(bad, s) {
				t.Fatal("unknown case outcome accepted")
			}
			if c.ExpectedOutcome == "matched" {
				bad = c
				bad.Rows++
				if hashWorkExpected(bad, base) {
					t.Fatal("wrong positive case rows accepted")
				}
			}
		})
	}
}

func hashWorkModelReport(t *testing.T) hashWorkReport {
	t.Helper()
	binding := hashWorkFileBinding{Path: "/scalar-report-model/NOT-EVIDENCE", SHA256: strings.Repeat("a", 64)}
	r := hashWorkReport{Schema: 1, Stage: "final", GoVersion: "go1.26.7", GOOS: "linux", GOARCH: "amd64", Environment: []string{"GOAMD64=v1", "CGO_ENABLED=1", "GOFLAGS=", "GOEXPERIMENT=", "GODEBUG="}, BuildInfo: &debug.BuildInfo{}, Provider: hashWorkFileBinding{Path: binding.Path, SHA256: hashWorkImageSHA256}, ProviderBytes: int(hashWorkImageBytes), Executable: binding, CustodyFile: binding, Cases: hashWorkCases(), Custody: hashWorkCustody{Sources: []hashWorkFileBinding{binding}, SourceInventory: binding, BuildCommand: binding, RunCommand: binding, GoEnvironment: binding, RawEnvironment: binding, ToolchainVersion: binding, MethodReview: binding, ExecutableSHA256: binding.SHA256, ProviderSHA256: hashWorkImageSHA256, IndependentMethodApproved: true, OrdinaryBuild: true}}
	for root := 0; root < 2; root++ {
		a := hashWorkModelAttribution(t, root)
		roles := map[journalv2.SealedRecordRole]int{journalv2.RecordOperationBinding: 256, journalv2.RecordOperationHead: 256, journalv2.RecordJournalEvent: 256, journalv2.RecordWorkspacePathClaim: 256, journalv2.RecordPublicationWitness: 256, journalv2.RecordEffectIntent: 671 + root}
		r.Roots = append(r.Roots, hashWorkRoot{Root: fmt.Sprintf("/scalar-report-model/root%d", root), Authority: fmt.Sprintf("/scalar-report-model/root%d/authority", root), Runtime: fmt.Sprintf("/scalar-report-model/root%d/runtime", root), RuntimeDirectory: fmt.Sprintf("/scalar-report-model/root%d/runtime/run", root), AdmissionSHA256: strings.Repeat("b", 64), Database: a.DatabaseID, Epoch: a.RuntimeEpochID, Closed: true, Geometry: hashWorkGeometry{ACKs: 256, Rows: uint64(1951 + root), Effects: 671 + root, Roles: roles, Unique: true, FiveBaseRoots: 256}})
	}
	for i := range r.Cases {
		for j := 0; j < 30; j++ {
			r.Cases[i].Samples = append(r.Cases[i].Samples, hashWorkModelSample(t, r.Cases[i], j))
		}
	}
	return r
}
func hashWorkModelFinalize(t *testing.T, r *hashWorkReport, failed, closed, want bool) {
	t.Helper()
	// Even poisoned incoming flags must be reset, on EVERY success/failure path.
	r.Complete = true
	r.WholeLayerQualified, r.WholeMemoryQualified, r.GenuineExpiryQualified, r.PerformanceSLOQualified = true, true, true, true
	hashWorkFinalize(r, failed, closed)
	if r.Complete != want || r.WholeLayerQualified || r.WholeMemoryQualified || r.GenuineExpiryQualified || r.PerformanceSLOQualified {
		t.Fatalf("report ALGORITHM complete=%v want=%v or qualification leaked", r.Complete, want)
	}
}
func TestHashWorkMethodFinalizeHappyAndRefusals(t *testing.T) {
	t.Run("synthetic_4x30_only", func(t *testing.T) {
		r := hashWorkModelReport(t)
		hashWorkModelFinalize(t, &r, false, true, true)
		for _, c := range r.Cases {
			if c.Statistics != (retainedPerformanceStats{Samples: 30, Min: 1, Median: 15, P95: 29, Max: 30, Total: 465}) {
				t.Fatal("nearest-rank statistics", c.Statistics)
			}
			for i, s := range c.Samples {
				if s.Index != i || s.Nanoseconds != int64(i+1) {
					t.Fatal("raw sample order changed")
				}
			}
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(*hashWorkReport)
	}{
		{"missing_root", func(r *hashWorkReport) { r.Roots = r.Roots[:1] }},
		{"extra_root", func(r *hashWorkReport) { r.Roots = append(r.Roots, r.Roots[0]) }},
		{"root_order", func(r *hashWorkReport) { r.Roots[0], r.Roots[1] = r.Roots[1], r.Roots[0] }},
		{"duplicate_root", func(r *hashWorkReport) { r.Roots[1] = r.Roots[0] }},
		{"missing_case", func(r *hashWorkReport) { r.Cases = r.Cases[:3] }},
		{"extra_case", func(r *hashWorkReport) { r.Cases = append(r.Cases, r.Cases[0]) }},
		{"duplicate_case", func(r *hashWorkReport) { r.Cases[1] = r.Cases[0] }},
		{"case_order", func(r *hashWorkReport) { r.Cases[0], r.Cases[1] = r.Cases[1], r.Cases[0] }},
		{"failure", func(r *hashWorkReport) { r.Failures = []string{"prefix write failed"} }},
		{"go_version", func(r *hashWorkReport) { r.GoVersion = "go1.27.2" }},
		{"os", func(r *hashWorkReport) { r.GOOS = "other" }},
		{"arch", func(r *hashWorkReport) { r.GOARCH = "other" }},
		{"provider_digest", func(r *hashWorkReport) { r.Provider.SHA256 = "wrong" }},
		{"provider_size", func(r *hashWorkReport) { r.ProviderBytes++ }},
		{"negative_provider_size", func(r *hashWorkReport) { r.ProviderBytes = -1 }},
		{"executable_empty", func(r *hashWorkReport) { r.Executable.SHA256 = "" }},
		{"executable_binding", func(r *hashWorkReport) { r.Custody.ExecutableSHA256 = "wrong" }},
		{"provider_binding", func(r *hashWorkReport) { r.Custody.ProviderSHA256 = "wrong" }},
		{"method_not_approved", func(r *hashWorkReport) { r.Custody.IndependentMethodApproved = false }},
		{"nonordinary", func(r *hashWorkReport) { r.Custody.OrdinaryBuild = false }},
		{"same_root_path", func(r *hashWorkReport) { r.Roots[1].Root = r.Roots[0].Root }},
		{"same_database", func(r *hashWorkReport) { r.Roots[1].Database = r.Roots[0].Database }},
		{"same_epoch", func(r *hashWorkReport) { r.Roots[1].Epoch = r.Roots[0].Epoch }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := hashWorkModelReport(t)
			tc.mutate(&r)
			hashWorkModelFinalize(t, &r, false, true, false)
		})
	}
	for _, state := range []struct{ failed, closed bool }{{true, true}, {false, false}, {true, false}} {
		r := hashWorkModelReport(t)
		hashWorkModelFinalize(t, &r, state.failed, state.closed, false)
	}
	for root := 0; root < 2; root++ {
		for _, field := range []string{"Closed", "CloseError", "Database", "Epoch", "ACKs", "Rows", "Effects", "Unique", "FiveBaseRoots", "role_count", "extra_role"} {
			t.Run(fmt.Sprintf("root%d_%s", root, field), func(t *testing.T) {
				r := hashWorkModelReport(t)
				v := &r.Roots[root]
				switch field {
				case "Closed":
					v.Closed = false
				case "CloseError":
					v.CloseError = "Close failed"
				case "Database":
					v.Database = ""
				case "Epoch":
					v.Epoch = ""
				case "ACKs":
					v.Geometry.ACKs++
				case "Rows":
					v.Geometry.Rows++
				case "Effects":
					v.Geometry.Effects++
				case "Unique":
					v.Geometry.Unique = false
				case "FiveBaseRoots":
					v.Geometry.FiveBaseRoots++
				case "role_count":
					v.Geometry.Roles[journalv2.RecordOperationHead]--
				case "extra_role":
					v.Geometry.Roles[journalv2.RecordAggregateHead] = 0
				}
				hashWorkModelFinalize(t, &r, false, true, false)
			})
		}
	}
	for ci := 0; ci < 4; ci++ {
		for _, field := range []string{"Name", "Root", "ExpectedOutcome", "ACKs", "Rows", "ReservedCalls", "ReservedHashes", "ReservedHashBytes", "PlannerAccepts", "missing_sample", "extra_sample", "duplicate_sample", "sample_order", "expected_not_met", "sample_failure", "sample_count", "sample_outcome", "time_zero", "time_negative", "time_overflow"} {
			t.Run(fmt.Sprintf("case%d_%s", ci, field), func(t *testing.T) {
				r := hashWorkModelReport(t)
				c := &r.Cases[ci]
				switch field {
				case "missing_sample":
					c.Samples = c.Samples[:29]
				case "extra_sample":
					c.Samples = append(c.Samples, c.Samples[0])
				case "duplicate_sample":
					c.Samples[1] = c.Samples[0]
				case "sample_order":
					c.Samples[0], c.Samples[1] = c.Samples[1], c.Samples[0]
				case "expected_not_met":
					c.Samples[0].ExpectedOutcomeMet = false
				case "sample_failure":
					c.Samples[0].FailureClass = "unexpected_outcome_or_invariant"
				case "sample_count":
					c.Samples[0].PrivateRows++
				case "sample_outcome":
					c.Samples[0].ObservedOutcome = "deadline_exceeded"
				case "time_zero":
					c.Samples[0].Nanoseconds = 0
				case "time_negative":
					c.Samples[0].Nanoseconds = -1
				case "time_overflow":
					for j := range c.Samples {
						c.Samples[j].Nanoseconds = math.MaxInt64
					}
				default:
					v := reflect.ValueOf(c).Elem().FieldByName(field)
					switch v.Kind() {
					case reflect.String:
						v.SetString("wrong")
					case reflect.Int:
						v.SetInt(v.Int() + 1)
					case reflect.Uint64:
						v.SetUint(v.Uint() + 1)
					case reflect.Bool:
						v.SetBool(!v.Bool())
					default:
						t.Fatal("unhandled field")
					}
				}
				hashWorkModelFinalize(t, &r, false, true, false)
			})
		}
	}
	t.Run("statistics_recomputed_not_trusted", func(t *testing.T) {
		r := hashWorkModelReport(t)
		for i := range r.Cases {
			r.Cases[i].Statistics = retainedPerformanceStats{Samples: 999, Total: math.MaxUint64}
			for j := range r.Cases[i].Samples {
				r.Cases[i].Samples[j].Nanoseconds = int64(30 - j)
			}
		}
		hashWorkModelFinalize(t, &r, false, true, true)
		for _, c := range r.Cases {
			if c.Statistics.Total != 465 || c.Statistics.P95 != 29 || c.Samples[0].Nanoseconds != 30 {
				t.Fatal("statistics/raw order")
			}
		}
	})
}

// External independent-review requirements, NOT report-identity exceptions.
// The finalizer cannot authenticate manifest authorship/completeness or reconcile
// effective source/ELF/build profile. Its accepted scalar values grant no credit:
// hashWorkCheckCustody/runtime guards and independent external review remain
// mandatory. Schema, stage, path topology, root linkage and error-type corruption
// are instead rejected by the mutation tests below.
func TestHashWorkMethodUncheckedReportMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*hashWorkReport)
	}{
		{"source_custody_missing", func(r *hashWorkReport) {
			r.Custody.Sources = nil
			r.Custody.SourceInventory = hashWorkFileBinding{}
			r.Custody.MethodReview = hashWorkFileBinding{}
		}},
		{"runtime_build_profile", func(r *hashWorkReport) {
			r.Environment = []string{"GOAMD64=v4", "CGO_ENABLED=0", "GOFLAGS=-race", "GODEBUG=invalid"}
			r.BuildInfo = &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "-race", Value: "true"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := hashWorkModelReport(t)
			tc.mutate(&r)
			hashWorkModelFinalize(t, &r, false, true, true)
			t.Log("EXTERNAL REQUIREMENT: finalizer alone cannot authenticate or reconcile", tc.name)
		})
	}
}

func TestHashWorkMethodFinalizeSchemaPathsAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*hashWorkReport)
	}{
		{"schema_zero", func(r *hashWorkReport) { r.Schema = 0 }},
		{"schema_other", func(r *hashWorkReport) { r.Schema = 999 }},
		{"stage_empty", func(r *hashWorkReport) { r.Stage = "" }},
		{"stage_prefix", func(r *hashWorkReport) { r.Stage = "pre_close_prefix" }},
		{"stage_reserved", func(r *hashWorkReport) { r.Stage = "reserved" }},
		{"stage_other", func(r *hashWorkReport) { r.Stage = "not_final" }},
		{"provider_path", func(r *hashWorkReport) { r.Provider.Path = "" }},
		{"executable_path", func(r *hashWorkReport) { r.Executable.Path = "" }},
		{"custody_file_path", func(r *hashWorkReport) { r.CustodyFile.Path = "" }},
		{"custody_file_hash", func(r *hashWorkReport) { r.CustodyFile.SHA256 = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := hashWorkModelReport(t)
			tc.mutate(&r)
			hashWorkModelFinalize(t, &r, false, true, false)
		})
	}
	for root := 0; root < 2; root++ {
		for _, field := range []string{"Root", "Authority", "Runtime", "RuntimeDirectory", "AdmissionSHA256", "Database", "Epoch"} {
			t.Run(fmt.Sprintf("root%d_empty_%s", root, field), func(t *testing.T) {
				r := hashWorkModelReport(t)
				reflect.ValueOf(&r.Roots[root]).Elem().FieldByName(field).SetString("")
				hashWorkModelFinalize(t, &r, false, true, false)
			})
		}
		for _, tc := range []struct {
			name   string
			mutate func(*hashWorkRoot)
		}{
			{"root_database_substitution", func(r *hashWorkRoot) { r.Database = "unrelated-db" }},
			{"root_epoch_substitution", func(r *hashWorkRoot) { r.Epoch = "unrelated-epoch" }},
			{"authority_is_root", func(r *hashWorkRoot) { r.Authority = r.Root }},
			{"runtime_is_root", func(r *hashWorkRoot) { r.Runtime = r.Root }},
			{"runtime_is_authority", func(r *hashWorkRoot) { r.Runtime = r.Authority }},
			{"session_is_runtime", func(r *hashWorkRoot) { r.RuntimeDirectory = r.Runtime }},
			{"session_under_authority", func(r *hashWorkRoot) { r.RuntimeDirectory = filepath.Join(r.Authority, "session") }},
			{"authority_wrong_child", func(r *hashWorkRoot) { r.Authority = filepath.Join(r.Root, "other") }},
			{"runtime_wrong_child", func(r *hashWorkRoot) {
				r.Runtime = filepath.Join(r.Root, "other")
				r.RuntimeDirectory = filepath.Join(r.Runtime, "session")
			}},
			{"relative_topology", func(r *hashWorkRoot) {
				r.Root = "relative"
				r.Authority = "relative/authority"
				r.Runtime = "relative/runtime"
				r.RuntimeDirectory = "relative/runtime/session"
			}},
			{"unclean_session", func(r *hashWorkRoot) { r.RuntimeDirectory = r.Runtime + "/./session" }},
			{"unclean_root", func(r *hashWorkRoot) {
				r.Root += "/../same"
				r.Authority = filepath.Join(r.Root, "authority")
				r.Runtime = filepath.Join(r.Root, "runtime")
				r.RuntimeDirectory = filepath.Join(r.Runtime, "session")
			}},
			{"not_sibling", func(r *hashWorkRoot) {
				r.Root = "/another-parent/root"
				r.Authority = filepath.Join(r.Root, "authority")
				r.Runtime = filepath.Join(r.Root, "runtime")
				r.RuntimeDirectory = filepath.Join(r.Runtime, "session")
			}},
		} {
			t.Run(fmt.Sprintf("root%d_%s", root, tc.name), func(t *testing.T) {
				r := hashWorkModelReport(t)
				tc.mutate(&r.Roots[root])
				hashWorkModelFinalize(t, &r, false, true, false)
			})
		}
	}
	// Include cross-field aliases across roots, not just equal Root strings.
	for _, field := range []string{"Root", "Authority", "Runtime", "RuntimeDirectory"} {
		t.Run("cross_root_alias_"+field, func(t *testing.T) {
			r := hashWorkModelReport(t)
			reflect.ValueOf(&r.Roots[1]).Elem().FieldByName(field).SetString(r.Roots[0].Authority)
			hashWorkModelFinalize(t, &r, false, true, false)
		})
	}
	for ci := 0; ci < 4; ci++ {
		for _, root := range []int{-1, 2, math.MaxInt, math.MinInt} {
			t.Run(fmt.Sprintf("case%d_bad_root_%d", ci, root), func(t *testing.T) {
				r := hashWorkModelReport(t)
				r.Cases[ci].Root = root
				hashWorkModelFinalize(t, &r, false, true, false)
			})
		}
		t.Run(fmt.Sprintf("case%d_wrong_valid_root", ci), func(t *testing.T) {
			r := hashWorkModelReport(t)
			c := &r.Cases[ci]
			c.Root = 1 - c.Root
			// Even coherently changing samples to the wrong valid root must fail the
			// exact case-to-root contract, independently of sample-link validation.
			a := hashWorkModelAttribution(t, c.Root)
			for j := range c.Samples {
				s := &c.Samples[j]
				s.Before = a
				s.After = a
				if s.Matched {
					s.ProofAttribution = a
				}
			}
			hashWorkModelFinalize(t, &r, false, true, false)
		})
		for j := 0; j < 30; j++ {
			for _, field := range []string{"DatabaseID", "RuntimeEpochID"} {
				t.Run(fmt.Sprintf("case%d_sample%d_coherent_%s", ci, j, field), func(t *testing.T) {
					r := hashWorkModelReport(t)
					s := &r.Cases[ci].Samples[j]
					reflect.ValueOf(&s.Before).Elem().FieldByName(field).SetString("other-identity")
					s.After = s.Before
					if s.Matched {
						s.ProofAttribution = s.Before
					}
					if !hashWorkExpected(r.Cases[ci], *s) {
						t.Fatal("fixture must pass standalone sample consistency so root linkage is tested")
					}
					hashWorkModelFinalize(t, &r, false, true, false)
				})
			}
		}
	}
}

func TestHashWorkMethodExactErrorTypeClasses(t *testing.T) {
	// The pinned Go context source uses errors.New; observed canceled class
	// requires that exact sentinel. Sanitized refusal requires direct *ProgressError.
	actual := []struct {
		err        error
		class, typ string
	}{
		{nil, "matched", "<nil>"},
		{context.Canceled, "canceled", "*errors.errorString"},
		{proofIncomplete("retained_records"), "sanitized_refusal", "*productionruntime.ProgressError"},
	}
	for _, v := range actual {
		if got := fmt.Sprintf("%T", v.err); got != v.typ {
			t.Fatalf("pinned type class changed: %s != %s", got, v.typ)
		}
		proof := RetainedRecordsProof{}
		if v.class == "matched" {
			proof.publications = 1
		}
		if hashWorkObserved(proof, v.err) != v.class {
			t.Fatal("type fixture is not in exact observed class")
		}
	}
	for ci, c := range hashWorkCases() {
		t.Run(c.Name, func(t *testing.T) {
			base := hashWorkModelSample(t, c, 0)
			for _, typ := range []string{"", "<nil>", "*errors.errorString", "*productionruntime.ProgressError", "*fmt.wrapError", "errors.errorString", "context.canceledError", "wrong"} {
				if typ == base.ErrorType {
					continue
				}
				s := base
				s.ErrorType = typ
				if hashWorkExpected(c, s) {
					t.Fatal("wrong error type accepted by sample checker", typ)
				}
				r := hashWorkModelReport(t)
				r.Cases[ci].Samples[0] = s
				hashWorkModelFinalize(t, &r, false, true, false)
			}
		})
	}
}

func TestHashWorkMethodCustodyBindings(t *testing.T) {
	// These are ordinary unit scratch bytes, not real source/image evidence or
	// an independent review. Exercise the binding checker without a provider.
	dir := hashWorkUnitDir(t)
	path := filepath.Join(dir, "unit-custody-input")
	original := []byte("unit-only source bytes\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	binding, body, err := hashWorkBindFile(path)
	if err != nil || binding.Path != path || string(body) != string(original) || len(binding.SHA256) != 64 {
		t.Fatal("scratch binding", err)
	}
	fresh := func() hashWorkCustody {
		return hashWorkCustody{Sources: []hashWorkFileBinding{binding}, SourceInventory: binding, BuildCommand: binding, RunCommand: binding, GoEnvironment: binding, RawEnvironment: binding, ToolchainVersion: binding, MethodReview: binding, ExecutableSHA256: "unit-executable-model", ProviderSHA256: hashWorkImageSHA256, IndependentMethodApproved: true, OrdinaryBuild: true}
	}
	if err := hashWorkCheckCustody(fresh(), "unit-executable-model"); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"SourceInventory", "BuildCommand", "RunCommand", "GoEnvironment", "RawEnvironment", "ToolchainVersion", "MethodReview"} {
		for _, part := range []string{"Path", "SHA256"} {
			t.Run(field+"_empty_"+part, func(t *testing.T) {
				c := fresh()
				reflect.ValueOf(&c).Elem().FieldByName(field).FieldByName(part).SetString("")
				if hashWorkCheckCustody(c, "unit-executable-model") == nil {
					t.Fatal("empty binding accepted")
				}
			})
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*hashWorkCustody)
	}{
		{"missing_sources", func(c *hashWorkCustody) { c.Sources = nil }},
		{"empty_source", func(c *hashWorkCustody) { c.Sources = []hashWorkFileBinding{{}} }},
		{"method_flag", func(c *hashWorkCustody) { c.IndependentMethodApproved = false }},
		{"build_flag", func(c *hashWorkCustody) { c.OrdinaryBuild = false }},
		{"executable_binding", func(c *hashWorkCustody) { c.ExecutableSHA256 = "wrong" }},
		{"image_binding", func(c *hashWorkCustody) { c.ProviderSHA256 = "wrong" }},
		{"source_hash", func(c *hashWorkCustody) { c.Sources[0].SHA256 = "wrong" }},
		{"source_path", func(c *hashWorkCustody) { c.Sources[0].Path = filepath.Join(dir, "missing") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fresh()
			tc.mutate(&c)
			if hashWorkCheckCustody(c, "unit-executable-model") == nil {
				t.Fatal("bad custody accepted")
			}
		})
	}
	if err := os.WriteFile(path, []byte("tampered unit input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if hashWorkCheckCustody(fresh(), "unit-executable-model") == nil {
		t.Fatal("changed bytes accepted")
	}
	if _, _, err := hashWorkBindFile(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("absent file bound")
	}
	t.Log("binding equality only: manifest completeness, review authorship and effective build correspondence still require independent external review")
}

func TestHashWorkMethodAllGeometryRoleMutations(t *testing.T) {
	roles := []journalv2.SealedRecordRole{journalv2.RecordOperationBinding, journalv2.RecordOperationHead, journalv2.RecordJournalEvent, journalv2.RecordWorkspacePathClaim, journalv2.RecordPublicationWitness, journalv2.RecordEffectIntent}
	for root := 0; root < 2; root++ {
		for _, role := range roles {
			for _, delta := range []int{-1, 1} {
				t.Run(fmt.Sprintf("root%d_%s_delta%d", root, role, delta), func(t *testing.T) {
					r := hashWorkModelReport(t)
					r.Roots[root].Geometry.Roles[role] += delta
					hashWorkModelFinalize(t, &r, false, true, false)
				})
			}
		}
	}
}

func TestHashWorkMethodSourceProfileGates(t *testing.T) {
	source, _ := hashWorkMethodSource(t, "TestRetainedHashWorkOriginalHostNative")
	for _, fragment := range []string{
		`runtime.Version() != "go1.26.7"`, `runtime.GOOS != "linux"`, `runtime.GOARCH != "amd64"`,
		`os.Getenv("GOAMD64") != "v1"`, `os.Getenv("CGO_ENABLED") != "1"`,
		`[]string{"GOFLAGS", "GOEXPERIMENT", "GODEBUG"}`, `if os.Getenv(key) != ""`,
		`if r.BuildInfo == nil`, `case "-race", "-msan", "-asan", "-cover", "-tags", "-gcflags", "-asmflags", "-ldflags":`,
		`setting.Value != "" && setting.Value != "false"`,
	} {
		hashWorkMethodOrder(t, source, fragment, `hashWorkBindFile(binary)`, `Start(t.Context(), config)`)
	}
	hashWorkMethodOrder(t, source, `json.Unmarshal(custodyBytes, &r.Custody)`, `hashWorkCheckCustody(r.Custody, r.Executable.SHA256)`, `Start(t.Context(), config)`)
	t.Log("profile/custody guard source coverage only, NOT proof of effective compiler flags or independently reviewed provenance")
}

func hashWorkUnitDir(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
func TestHashWorkMethodReserve(t *testing.T) {
	t.Run("exclusive_actual_mode_and_existing_preserved", func(t *testing.T) {
		dir := hashWorkUnitDir(t)
		path := filepath.Join(dir, "report.json")
		out, parent, err := hashWorkReserve(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := out.Stat()
		if err != nil {
			t.Fatal(err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if parent != dir || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ok || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
			t.Fatal("actual descriptor mode/identity")
		}
		if _, err := out.WriteString("preserve exclusive prefix\n"); err != nil {
			t.Fatal(err)
		}
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
		again, _, err := hashWorkReserve(path)
		if err == nil || again != nil || !errors.Is(err, os.ErrExist) {
			t.Fatal("existing report reopened", err)
		}
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "preserve exclusive prefix\n" {
			t.Fatal("existing report overwritten")
		}
	})
	for _, mode := range []os.FileMode{0755, 0750, 0770, 0777, 0600, 0700 | os.ModeSticky, 0700 | os.ModeSetgid, 0700 | os.ModeSetuid} {
		t.Run(fmt.Sprintf("parent_mode_%v", mode), func(t *testing.T) {
			dir := hashWorkUnitDir(t)
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(dir, 0700)
			path := filepath.Join(dir, "report")
			out, _, err := hashWorkReserve(path)
			if err == nil || out != nil {
				if out != nil {
					out.Close()
				}
				t.Fatal("bad parent accepted")
			}
			// Restore search permission before checking absence for0600 parents.
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("created before parent validation", err)
			}
		})
	}
	t.Run("symlink_parent_and_ancestor", func(t *testing.T) {
		dir := hashWorkUnitDir(t)
		target := filepath.Join(dir, "real")
		if err := os.Mkdir(target, 0700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(target, "child")
		if err := os.Mkdir(child, 0700); err != nil {
			t.Fatal(err)
		}
		for _, parent := range []string{link, filepath.Join(link, "child")} {
			if out, _, err := hashWorkReserve(filepath.Join(parent, "report")); err == nil || out != nil {
				t.Fatal("symlink ancestry accepted")
			}
		}
	})
	t.Run("report_symlink_preserved", func(t *testing.T) {
		dir := hashWorkUnitDir(t)
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "report")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if out, _, err := hashWorkReserve(path); err == nil || out != nil {
			t.Fatal("report symlink accepted")
		}
		body, _ := os.ReadFile(target)
		if string(body) != "unchanged" {
			t.Fatal("symlink target changed")
		}
		if _, err := os.Readlink(path); err != nil {
			t.Fatal("symlink removed")
		}
	})
	t.Run("foreign_parent_uid", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("optional foreign-owner dynamic branch not executed without privilege; no escalation requested")
		}
		dir := hashWorkUnitDir(t)
		uid := os.Geteuid() + 1
		if err := os.Chown(dir, uid, -1); err != nil {
			if errors.Is(err, syscall.EPERM) {
				t.Skip("cannot construct foreign-owned0700 parent without privilege; uid predicate also checked in source")
			}
			t.Fatal(err)
		}
		defer os.Chown(dir, os.Geteuid(), -1)
		path := filepath.Join(dir, "report")
		if out, _, err := hashWorkReserve(path); err == nil || out != nil {
			t.Fatal("foreign parent accepted")
		}
	})
	t.Run("missing_or_file_parent", func(t *testing.T) {
		dir := hashWorkUnitDir(t)
		file := filepath.Join(dir, "file")
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
		for _, parent := range []string{file, filepath.Join(dir, "missing")} {
			if out, _, err := hashWorkReserve(filepath.Join(parent, "report")); err == nil || out != nil {
				t.Fatal("invalid parent accepted")
			}
		}
	})
}

// Umask is process-global. Exercise restrictive post-create actual-mode refusal
// in a separate ordinary unit subprocess, never a concurrent parent mutation.
func TestHashWorkMethodReserveRestrictiveUmask(t *testing.T) {
	dir := hashWorkUnitDir(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestHashWorkMethodReserveModeChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "HASH_WORK_UNIT_MODE_CHILD="+dir)
	body, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ordinary unit mode subprocess: %v\n%s", err, body)
	}
	t.Log(string(body))
	path := filepath.Join(dir, "restricted-report")
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("refused file not preserved", err)
	}
}
func TestHashWorkMethodReserveModeChild(t *testing.T) {
	dir := os.Getenv("HASH_WORK_UNIT_MODE_CHILD")
	if dir == "" {
		t.Skip("ordinary restrictive-umask child only")
	}
	old := syscall.Umask(0200)
	defer syscall.Umask(old)
	out, parent, err := hashWorkReserve(filepath.Join(dir, "restricted-report"))
	if err == nil || out != nil || parent != dir || !strings.Contains(err.Error(), "actual regular") {
		t.Fatal("post-create actual mode not refused", err)
	}
	path := filepath.Join(dir, "restricted-report")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("file erased or not restricted", err)
	}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
		if err == nil && target == path {
			t.Fatal("post-create refusal leaked its descriptor")
		}
	}
}

func TestHashWorkMethodCompletionPrecedesDescriptorAcceptance(t *testing.T) {
	// Pure model plus unit scratch: Complete is deliberately a pre-descriptor
	// state, NOT an assertion that final persistence or the raw command succeeded.
	r := hashWorkModelReport(t)
	hashWorkModelFinalize(t, &r, false, true, true)
	path := filepath.Join(hashWorkUnitDir(t), "late-write-refusal")
	out, _, err := hashWorkReserve(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hashWorkWrite(out, r); !errors.Is(err, os.ErrClosed) {
		t.Fatal("late closed-descriptor write not refused", err)
	}
	if !r.Complete {
		t.Fatal("in-memory completion definition unexpectedly changed")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatal("failed final write changed/deleted exclusive file", err)
	}
	// Separately show that even complete JSON cannot attest later descriptor
	// operations. Double-Close is a scoped refusal, not an injected first-Close
	// kernel fault or a failed native attempt that can be rerun into acceptance.
	path = filepath.Join(hashWorkUnitDir(t), "complete-before-descriptor-outcome")
	out, _, err = hashWorkReserve(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := hashWorkWrite(out, r); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("late descriptor refusal not exposed", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved hashWorkReport
	if err := json.Unmarshal(body, &saved); err != nil || !saved.Complete {
		t.Fatal("pre-descriptor report model", err)
	}
	t.Log("Complete is NOT measurement acceptance: external raw exit0, write/sync/Close success and reconciled source/ELF/profile custody are mandatory; no retry repair")
}

func TestHashWorkMethodPrefixWriteAndCloseFailures(t *testing.T) {
	dir := hashWorkUnitDir(t)
	path := filepath.Join(dir, "unit-report.json")
	out, _, err := hashWorkReserve(path)
	if err != nil {
		t.Fatal(err)
	}
	r := hashWorkModelReport(t)
	r.Stage = "pre_close_prefix"
	r.Cases[0].Samples = r.Cases[0].Samples[:3]
	r.Failures = []string{"first unexpected prefix"}
	hashWorkModelFinalize(t, &r, true, false, false)
	if err := hashWorkWrite(out, r); err != nil {
		t.Fatal(err)
	}
	prefix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved hashWorkReport
	if err := json.Unmarshal(prefix, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Complete || len(saved.Cases[0].Samples) != 3 || saved.Failures[0] != "first unexpected prefix" {
		t.Fatal("failing prefix not persisted")
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hashWorkWrite(out, r); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed descriptor write not refused", err)
	}
	if err := out.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("double close failure not exposed", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(prefix) {
		t.Fatal("failed write/close removed/changed prefix")
	}
	readonly, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := hashWorkWrite(readonly, r); err == nil {
		t.Fatal("read-only descriptor write accepted")
	}
	if err := readonly.Close(); err != nil {
		t.Fatal(err)
	}
	// Rewrite shorter JSON and verify truncate/sync; this is unit scratch only.
	rewrite, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	short := hashWorkReport{Stage: "unit-short"}
	if err := hashWorkWrite(rewrite, short); err != nil {
		t.Fatal(err)
	}
	if err := rewrite.Close(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal("trailing stale prefix", err)
	}
	// First-write refusal leaves an exclusive zero-byte scratch file. No injected
	// kernel Sync/Truncate/Close error or actual Host cleanup failure is claimed.
	empty := filepath.Join(dir, "first-write-refusal")
	first, _, err := hashWorkReserve(empty)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hashWorkWrite(first, r); err == nil {
		t.Fatal("first write accepted on closed descriptor")
	}
	info, err := os.Stat(empty)
	if err != nil || info.Size() != 0 {
		t.Fatal("first-write refusal did not preserve empty reservation")
	}
	t.Log("scope: descriptor/write refusal and synthetic incomplete cleanup only; no live Host Close fault or hard-kill durability claim")
}

func hashWorkMethodSource(t *testing.T, name string) (string, *ast.FuncDecl) {
	t.Helper()
	body, err := os.ReadFile("retained_hash_work_performance_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "retained_hash_work_performance_test.go", body, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == name {
			return string(body[fs.Position(f.Pos()).Offset:fs.Position(f.End()).Offset]), f
		}
	}
	t.Fatalf("function %s absent", name)
	return "", nil
}
func hashWorkMethodOrder(t *testing.T, source string, parts ...string) {
	t.Helper()
	last := -1
	for _, part := range parts {
		i := strings.Index(source, part)
		if i < 0 || i <= last {
			t.Fatalf("source ordering missing/reordered %q", part)
		}
		last = i
	}
}
func hashWorkMethodOptinExpr(t *testing.T, expr ast.Expr, values map[string]string) bool {
	t.Helper()
	b, ok := expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("unexpected optin expression %T", expr)
	}
	switch b.Op {
	case token.LAND:
		return hashWorkMethodOptinExpr(t, b.X, values) && hashWorkMethodOptinExpr(t, b.Y, values)
	case token.LOR:
		return hashWorkMethodOptinExpr(t, b.X, values) || hashWorkMethodOptinExpr(t, b.Y, values)
	case token.EQL:
		id, ok := b.X.(*ast.Ident)
		lit, lok := b.Y.(*ast.BasicLit)
		if !ok || !lok || lit.Value != `""` {
			t.Fatal("unexpected optin scalar predicate")
		}
		value, exists := values[id.Name]
		if !exists {
			t.Fatal("unrecognized optin variable")
		}
		return value == ""
	default:
		t.Fatal("unexpected optin operator")
	}
	return false
}
func TestHashWorkMethodSourceOptinsAndFailureCustody(t *testing.T) {
	source, fn := hashWorkMethodSource(t, "TestRetainedHashWorkOriginalHostNative")
	all, ok := fn.Body.List[1].(*ast.IfStmt)
	if !ok {
		t.Fatal("missing all-empty optin guard")
	}
	partial, ok := fn.Body.List[2].(*ast.IfStmt)
	if !ok {
		t.Fatal("missing partial optin guard")
	}
	hashWorkMethodOrder(t, source, "os.Getenv(\"SPEC_KITTY_WP13_GAPDBD\")", "t.Skip(", "t.Fatal(\"exact image", "hashWorkReserve(reportPath)", "defer func()", "hashWorkBindFile(binary)", "Start(t.Context(), config)")
	for mask := 0; mask < 8; mask++ {
		values := map[string]string{}
		for i, key := range []string{"binary", "reportPath", "custodyPath"} {
			values[key] = ""
			if mask&(1<<i) != 0 {
				values[key] = "present"
			}
		}
		skip := hashWorkMethodOptinExpr(t, all.Cond, values)
		refuse := hashWorkMethodOptinExpr(t, partial.Cond, values)
		if skip != (mask == 0) || (!skip && refuse) != (mask != 0 && mask != 7) {
			t.Fatal("partial optin source truth table", mask)
		}
	}
	hashWorkMethodOrder(t, source, `r.Stage = "pre_close_prefix"`, "hashWorkFinalize(&r, t.Failed(), false)", "hashWorkWrite(out, r)", "closeHost() // No") // final close of each sampled root is later in the source
	// Check the defer itself: no invocation of native/provider work is made here.
	start := strings.Index(source, "defer func() {")
	end := strings.Index(source[start:], "}()") + start
	deferred := source[start:end]
	hashWorkMethodOrder(t, deferred, `r.Stage = "pre_close_prefix"`, "hashWorkWrite(out, r)", "closeHost()", `r.Stage = "final"`, "hashWorkFinalize(&r, t.Failed(), true)", "out.Close()")
	hashWorkMethodOrder(t, source, "c.Samples = append(c.Samples, s)", "if !s.ExpectedOutcomeMet {\n\t\t\t\t\tfail(")
	hashWorkMethodOrder(t, source, "err := h.Close(context.Background())", "r.Roots[activeRoot].Closed = err == nil", `r.Roots[activeRoot].CloseError = err.Error()`, `t.Error(err)`, `h = nil`)
	reserve, _ := hashWorkMethodSource(t, "hashWorkReserve")
	hashWorkMethodOrder(t, reserve, "os.Lstat(parent)", "filepath.EvalSymlinks(parent)", "stat.Uid != uint32(os.Geteuid())", "os.O_EXCL", "out.Stat()", "actual.Nlink != 1", "errors.Join(err, out.Close())")
	for _, fragment := range []string{"!created.Mode().IsRegular()", "created.Mode().Perm() != 0600", "actual.Uid != uint32(os.Geteuid())"} {
		if !strings.Contains(reserve, fragment) {
			t.Fatal("missing descriptor validation", fragment)
		}
	}
	if strings.Contains(source, "RemoveAll(") || strings.Contains(source, "os.Remove(") || strings.Contains(reserve, "Remove(") {
		t.Fatal("evidence cleanup added")
	}
	t.Log("source AST/truth-table checks only: no native test selected, no provider or Host invoked; fstat uid/nlink and joined Close path are structural, not forced kernel faults")
}
