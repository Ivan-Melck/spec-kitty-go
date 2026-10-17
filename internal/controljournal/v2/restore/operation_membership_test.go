package restore

import "testing"

func TestRestoreOperationMembershipIsDefensiveAndBoundToSeal(t *testing.T) {
	input, _, _ := qualificationRestoreFixture(t, 1, 2, false)
	result, err := RestoreV2(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	memberships := result.OperationMembership()
	if len(memberships) < 2 {
		t.Fatal("fixture did not restore multiple validated operation bindings")
	}
	for i, membership := range memberships {
		if membership.OperationID() == "" || membership.Scope().Validate() != nil || i > 0 && memberships[i-1].OperationID() >= membership.OperationID() {
			t.Fatal("restored operation membership is invalid or nondeterministically ordered")
		}
	}
	original := memberships[0]
	memberships[0] = memberships[1]
	scope := original.Scope()
	scope.ProjectID = "project-tampered"
	if result.OperationMembership()[0] != original {
		t.Fatal("query-facing membership exposed mutable restore evidence")
	}
	if _, err := result.FreshEpochAdmissionRequestV2(); err != nil {
		t.Fatalf("copy-only observation invalidated the original restore: %v", err)
	}
	for _, change := range []string{"scope", "identity", "omission", "order"} {
		t.Run(change, func(t *testing.T) {
			tampered := result
			tampered.memberships = result.OperationMembership()
			switch change {
			case "scope":
				tampered.memberships[0].scope = scope
			case "identity":
				tampered.memberships[0].operationID = "operation-tampered"
			case "omission":
				tampered.memberships = tampered.memberships[1:]
			case "order":
				tampered.memberships[0], tampered.memberships[1] = tampered.memberships[1], tampered.memberships[0]
			}
			if _, err := tampered.FreshEpochAdmissionRequestV2(); err == nil {
				t.Fatal("altered operation membership retained a valid restore seal")
			}
		})
	}
}
