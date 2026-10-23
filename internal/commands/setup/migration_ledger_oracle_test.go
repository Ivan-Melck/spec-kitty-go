package setup_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestMigrationLedgerMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/config.yaml", "agents:\n  available: []\n") })
	p.Compare(true, "migrate", "ledger", "--json")
	p.Compare(true, "migrate", "ledger")
	p.CompareTree(".kittify", true)
	p.Both(func(s *testkit.Sandbox) {
		// A tracked row cannot settle clone-local work. A clone-local row
		// with a stale hash is reported as drift without being reselected.
		s.WriteFile(".kittify/migration-ledger.jsonl", "not json\n{}\n"+
			`{"id":"4.0.0rc7_exclude_spec_kitty","applied_at":"2026-10-23T12:00:00+00:00","result":"success","identity":"4.0.0rc7_exclude_spec_kitty","content_hash":"sha256:previous","cli_version":"4.0.0rc8"}`+"\n")
		s.WriteFile(".kittify/migrations/clone-local.jsonl",
			`{"id":"4.0.0rc7_exclude_spec_kitty","applied_at":"2026-10-23T12:00:00+00:00","result":"success","identity":"4.0.0rc7_exclude_spec_kitty","content_hash":"sha256:previous","cli_version":"4.0.0rc8","clone_local":true}`+"\n")
	})
	p.Compare(true, "migrate", "ledger", "--json")
	p.Compare(true, "migrate", "ledger")
	p.CompareTree(".kittify", true)
}
