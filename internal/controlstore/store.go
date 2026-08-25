package controlstore

import (
	"context"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type Settings struct {
	JournalMode string
	ForeignKeys bool
	BusyTimeout int
}

// GovernanceContextReader is the narrow portable resolution contract exposed
// by a control-store adapter. Run admission persistence remains atomic through
// execution.AuthorityRepository.Claim, whose StartRequest owns the complete
// validated context.
type GovernanceContextReader interface {
	GovernanceContext(context.Context, ids.GovernanceContextID) (governancecontext.Context, error)
}

var ErrGovernanceContextConflict = governancecontext.ErrIdentityConflict

type GovernanceContextConflictError = governancecontext.IdentityConflictError
