//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func expiryTestBinding() AuthorityBindingV2 {
	return AuthorityBindingV2{qualification: authorityQualificationBinding{expiryInputSHA, expiryTupleSHA, expiryInventorySHA, expiryRecoveryContract},
		SchemaVersion: 1, HostAuthorityID: "host-expiry", DatabaseID: "database-expiry", CreatedOperationID: "operation-expiry-binding",
		SidecarSHA: expiryExecutableSHA, WireProtocol: 1, StorageProtocol: 1}
}

func TestExpiryHostBindingClosedMaximumAndComparability(t *testing.T) {
	binding := expiryTestBinding()
	binding.HostAuthorityID = journalv2.HostAuthorityID(strings.Repeat("H", 128))
	binding.DatabaseID = journalv2.DatabaseID(strings.Repeat("D", 128))
	binding.CreatedOperationID = strings.Repeat("O", 128)
	value, err := binding.canonical()
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != expiryBindingMaximum {
		t.Fatalf("maximum = %d", len(value))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 15 || fields["admitted_tuple_sha256"] != nil || fields["sidecar_sha256"] != nil {
		t.Fatal("not exact separate15-member schema1")
	}
	for _, field := range []string{"adopted", "production_admission", "release_ready"} {
		if !bytes.Equal(fields[field], []byte("false")) {
			t.Fatalf("%s is not false", field)
		}
	}
	if string(fields["schema_version"]) != "1" || string(fields["executable_sha256"]) != `"`+expiryExecutableSHA+`"` {
		t.Fatal("wrong schema/image")
	}
	clone := binding
	clone.qualification.inputRecordSHA256 = string(bytes.Clone([]byte(expiryInputSHA)))
	if clone != binding || !reflect.TypeOf(binding).Comparable() {
		t.Fatal("binding equality is not content-comparable")
	}
	if reflect.TypeOf(binding).Field(0).Name != "qualification" {
		t.Fatal("private variant must be FIRST")
	}
	_ = map[AuthorityBindingV2]bool{binding: true}
}

func TestExpiryHostBindingRejectsEveryPartialOrMixedVariant(t *testing.T) {
	mutations := map[string]func(*AuthorityBindingV2){
		"input":            func(b *AuthorityBindingV2) { b.qualification.inputRecordSHA256 = "" },
		"tuple":            func(b *AuthorityBindingV2) { b.qualification.technicalTupleSHA256 = expiryInputSHA },
		"inventory":        func(b *AuthorityBindingV2) { b.qualification.modifiedInventorySHA256 = "" },
		"reader":           func(b *AuthorityBindingV2) { b.qualification.recoveryContract = "optional" },
		"schema2":          func(b *AuthorityBindingV2) { b.SchemaVersion = 2 },
		"admission":        func(b *AuthorityBindingV2) { b.AdmittedTupleSHA = expiryTupleSHA },
		"image":            func(b *AuthorityBindingV2) { b.SidecarSHA = strings.Repeat("a", 64) },
		"wire":             func(b *AuthorityBindingV2) { b.WireProtocol = 2 },
		"storage":          func(b *AuthorityBindingV2) { b.StorageProtocol = 0 },
		"host_empty":       func(b *AuthorityBindingV2) { b.HostAuthorityID = "" },
		"host_long":        func(b *AuthorityBindingV2) { b.HostAuthorityID = journalv2.HostAuthorityID(strings.Repeat("H", 129)) },
		"host_escape":      func(b *AuthorityBindingV2) { b.HostAuthorityID = "host\"escape" },
		"db_empty":         func(b *AuthorityBindingV2) { b.DatabaseID = "" },
		"db_long":          func(b *AuthorityBindingV2) { b.DatabaseID = journalv2.DatabaseID(strings.Repeat("D", 129)) },
		"db_escape":        func(b *AuthorityBindingV2) { b.DatabaseID = "db\\escape" },
		"operation_empty":  func(b *AuthorityBindingV2) { b.CreatedOperationID = "" },
		"operation_long":   func(b *AuthorityBindingV2) { b.CreatedOperationID = strings.Repeat("O", 129) },
		"operation_escape": func(b *AuthorityBindingV2) { b.CreatedOperationID = "op\nescape" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b := expiryTestBinding()
			mutate(&b)
			if value, handled, err := qualificationCanonical(b); !handled || err == nil || value != nil {
				t.Fatal("marked error fell back or produced bytes")
			}
			if value, err := b.canonical(); err == nil || value != nil {
				t.Fatal("invalid marked binding accepted")
			}
		})
	}
}

func TestExpiryHostUnmarkedBindingPreservesEntireOrdinarySchema(t *testing.T) {
	b := AuthorityBindingV2{SchemaVersion: 2, HostAuthorityID: "host-ordinary", DatabaseID: "database-ordinary", AdmittedTupleSHA: strings.Repeat("a", 64), SidecarSHA: strings.Repeat("b", 64), WireProtocol: 1, StorageProtocol: 1, CreatedOperationID: "operation-binding"}
	if value, handled, err := qualificationCanonical(b); handled || err != nil || value != nil {
		t.Fatal("unmarked dispatch handled")
	}
	value, err := b.canonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"admitted_tuple_sha256":"` + strings.Repeat("a", 64) + `","created_operation_id":"operation-binding","database_id":"database-ordinary","host_authority_id":"host-ordinary","schema_version":2,"sidecar_sha256":"` + strings.Repeat("b", 64) + `","storage_protocol":1,"wire_protocol":1}`
	if string(value) != want {
		t.Fatalf("ordinary vector changed: %s", value)
	}
	marked := expiryTestBinding()
	raw, err := json.Marshal(marked)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("input_record")) || bytes.Contains(raw, []byte("qualification")) {
		t.Fatal("raw ordinary JSON leaked private field")
	}
}
