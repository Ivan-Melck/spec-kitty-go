//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"testing"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// DRAFT PENDING DESIGN / UNRUN. Typed predicate inputs only, not a fabricated
// Host, lifecycle, provider, authenticated response or Native bootstrap arrival.
func expiryBootstrapDraftInputs() (sidecargapdb.SidecarProvenance, sidecargapdb.ExpiryTechnicalIdentity) {
	identity := sidecargapdb.ExpiryTechnicalIdentity{ExecutableSHA256: expiryExecutableSHA}
	p := sidecargapdb.SidecarProvenance{
		DatabaseID: "bootstrap-component-db", DataDirectory: "/component/bootstrap-root", SupervisorRunID: "bootstrap-component-supervisor",
		ExecutedImage: sidecargapdb.ExecutedImageProvenance{SHA256: expiryExecutableSHA},
		Process: sidecargapdb.ProcessIdentity{PID: 42001},
		Observation: sidecargapdb.CompositeObservation{Status: gapdbapi.StatusResult{
			DatabaseID: "bootstrap-component-db", OwnerPID: 42001, Lifecycle: gapdbapi.LifecycleReady,
		}},
	}
	return p, identity
}

func TestExpiryBootstrapDraftPredicateComponents(t *testing.T) {
	for _, name := range []string{"valid_control", "empty_database", "wrong_data_root", "wrong_supervisor", "wrong_executed_SHA", "not_ready", "owner_PID_disagreement", "database_disagreement"} {
		t.Run(name, func(t *testing.T) {
			p, identity := expiryBootstrapDraftInputs()
			if expiryBootstrapRefused(p, "/component/bootstrap-root", "bootstrap-component-supervisor", identity) { t.Fatal("valid predicate control refused") }
			switch name {
			case "empty_database":
				// Keep the separate database-agreement atom false: this input
				// isolates emptiness, not two independently credited refusals.
				p.DatabaseID, p.Observation.Status.DatabaseID = "", ""
			case "wrong_data_root": p.DataDirectory = "/component/other-root"
			case "wrong_supervisor": p.SupervisorRunID = "bootstrap-other-supervisor"
			case "wrong_executed_SHA": p.ExecutedImage.SHA256 = expiryInputSHA
			case "not_ready": p.Observation.Status.Lifecycle = gapdbapi.LifecycleState("not-ready")
			case "owner_PID_disagreement": p.Observation.Status.OwnerPID++
			case "database_disagreement": p.Observation.Status.DatabaseID = "bootstrap-other-db"
			}
			if got := expiryBootstrapRefused(p, "/component/bootstrap-root", "bootstrap-component-supervisor", identity); got != (name != "valid_control") {
				t.Fatalf("predicate component %s refused=%t", name, got)
			}
		})
	}
}

func TestExpiryBootstrapDraftInitialZeroComponents(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, name := range []string{"zero_control", "current_nonzero", "durable_nonzero", "record_count_nonzero"} {
			t.Run(name+map[bool]string{false:"/successor", true:"/initial"}[initial], func(t *testing.T) {
				p, _ := expiryBootstrapDraftInputs()
				if expiryBootstrapNotZero(initial, p) { t.Fatal("zero control refused") }
				switch name {
				case "current_nonzero": p.Observation.Status.CurrentRevision = 1
				case "durable_nonzero": p.Observation.Status.DurableThroughRevision = 1
				case "record_count_nonzero": p.Observation.Status.RecordCount = 1
				}
				if got := expiryBootstrapNotZero(initial, p); got != (initial && name != "zero_control") { t.Fatalf("initial=%t component=%s refused=%t", initial, name, got) }
				// durable=1,current=0 deliberately isolates the pure atom but
				// cannot pass earlier validateCompositeCore; never Native credit.
			})
		}
	}
}
