package workspace

import (
	"context"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type Store interface {
	BindRepository(context.Context, RepositoryBinding) error
	Repository(context.Context, ids.RepositoryID) (RepositoryBinding, error)
	WorkAuthority(context.Context, ids.WorkRevisionID, ids.WorkID) (WorkAuthority, error)
	ReserveWorkspace(context.Context, Reservation, string) (Record, error)
	Workspace(context.Context, ids.WorkspaceID) (Record, error)
	AdvanceWorkspace(context.Context, ids.WorkspaceID, Authority, Stage, Stage, Probe, string, time.Time) (Record, error)
	QuarantineWorkspace(context.Context, ids.WorkspaceID, Authority, string, time.Time) error
}

type GitWorkspace interface {
	Binding() RepositoryBinding
	WorkspacePath(string) (string, error)
	Create(context.Context, Record) error
	Probe(context.Context, Record) (Probe, error)
	PrepareDependencies(context.Context, Record, []Dependency) (string, error)
	TargetHead(context.Context) (string, error)
	WithTargetAuthority(context.Context, func(string) error) error
}

type Service struct {
	store  Store
	git    GitWorkspace
	faults faults.Hook
}

func NewService(store Store, git GitWorkspace) *Service {
	return &Service{store: store, git: git, faults: faults.Noop{}}
}

func (s *Service) WithFaults(hook faults.Hook) *Service {
	if hook == nil {
		hook = faults.Noop{}
	}
	s.faults = hook
	return s
}

func (s *Service) BindRepository(ctx context.Context, binding RepositoryBinding) error {
	if _, err := ids.ParseRepositoryID(binding.RepositoryID.String()); err != nil || binding.CommonDir == "" || binding.PrimaryPath == "" || binding.ManagedRoot == "" || binding.Primary.Head == "" || binding.Primary.IndexDigest == "" || binding.Primary.TreeDigest == "" {
		return &Error{Kind: KindInvalid, Message: "repository binding is incomplete", Cause: err}
	}
	return s.store.BindRepository(ctx, binding)
}

func (s *Service) EnsureReady(ctx context.Context, reservation Reservation) (Record, error) {
	if err := s.ValidateBinding(ctx, reservation); err != nil {
		return Record{}, err
	}
	if err := s.validateIdentity(ctx, reservation); err != nil {
		return Record{}, err
	}
	path, err := s.git.WorkspacePath(reservation.WorkspaceID.String())
	if err != nil {
		return Record{}, err
	}
	record, err := s.store.ReserveWorkspace(ctx, reservation, path)
	if err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "SR-05"); err != nil {
		return Record{}, err
	}
	for {
		switch record.Stage {
		case StageReserved:
			if err := s.git.Create(ctx, record); err != nil {
				return Record{}, err
			}
			if err := s.faults.Reach(ctx, "SR-06"); err != nil {
				return Record{}, err
			}
			probe, err := s.git.Probe(ctx, record)
			if err != nil {
				return Record{}, err
			}
			if !probe.Registered {
				return Record{}, notReady(record, probe, "Git registration")
			}
			record, err = s.store.AdvanceWorkspace(ctx, record.WorkspaceID, reservation.Authority, StageReserved, StageWorktreeCreated, probe, "", reservation.Now)
			if err != nil {
				return Record{}, err
			}
		case StageWorktreeCreated:
			probe, err := s.git.Probe(ctx, record)
			if err != nil {
				return Record{}, err
			}
			if !probe.Registered || probe.Head != record.PinnedBase {
				return Record{}, &Error{Kind: KindTargetMoved, Message: "workspace does not match its pinned base", Observed: map[string]string{"registered": fmt.Sprint(probe.Registered), "head": probe.Head}, Expected: map[string]string{"registered": "true", "head": record.PinnedBase}}
			}
			record, err = s.store.AdvanceWorkspace(ctx, record.WorkspaceID, reservation.Authority, StageWorktreeCreated, StageBaseVerified, probe, "", reservation.Now)
			if err != nil {
				return Record{}, err
			}
			if err := s.faults.Reach(ctx, "SR-07"); err != nil {
				return Record{}, err
			}
		case StageBaseVerified:
			preparedHead, err := s.git.PrepareDependencies(ctx, record, record.Dependencies)
			if err != nil {
				return Record{}, err
			}
			if err := s.faults.Reach(ctx, "SR-08"); err != nil {
				return Record{}, err
			}
			probe, err := s.git.Probe(ctx, record)
			if err != nil {
				return Record{}, err
			}
			if !probe.Registered || !probe.Clean || probe.Head != preparedHead {
				return Record{}, notReady(record, probe, "dependency-prepared head")
			}
			record, err = s.store.AdvanceWorkspace(ctx, record.WorkspaceID, reservation.Authority, StageBaseVerified, StageDependenciesPrepared, probe, preparedHead, reservation.Now)
			if err != nil {
				return Record{}, err
			}
		case StageDependenciesPrepared:
			probe, err := s.git.Probe(ctx, record)
			if err != nil {
				return Record{}, err
			}
			if !probe.Registered || !probe.Clean || probe.Branch != record.OwnedBranch || probe.Head != record.DependencyHead || probe.GitDir == "" || probe.IndexPath == "" {
				return Record{}, notReady(record, probe, "owned branch and isolated Git metadata")
			}
			record, err = s.store.AdvanceWorkspace(ctx, record.WorkspaceID, reservation.Authority, StageDependenciesPrepared, StageOwnershipVerified, probe, record.DependencyHead, reservation.Now)
			if err != nil {
				return Record{}, err
			}
		case StageOwnershipVerified:
			var published Record
			err := s.git.WithTargetAuthority(ctx, func(targetHead string) error {
				if targetHead != record.PinnedBase {
					return targetMoved(record, targetHead, "repository target moved before workspace publication")
				}
				probe, err := s.git.Probe(ctx, record)
				if err != nil {
					return err
				}
				if !readyProbeMatches(record, probe) {
					return notReady(record, probe, "final workspace readiness")
				}
				confirmedHead, err := s.git.TargetHead(ctx)
				if err != nil {
					return err
				}
				if confirmedHead != targetHead || confirmedHead != record.PinnedBase {
					return targetMoved(record, confirmedHead, "repository target moved in the final publication window")
				}
				published, err = s.store.AdvanceWorkspace(ctx, record.WorkspaceID, reservation.Authority, StageOwnershipVerified, StageReady, probe, record.DependencyHead, reservation.Now)
				if err != nil {
					return err
				}
				publishedHead, err := s.git.TargetHead(ctx)
				if err != nil {
					_ = s.store.QuarantineWorkspace(ctx, record.WorkspaceID, reservation.Authority, "target re-probe failed after ready publication", reservation.Now)
					return err
				}
				if publishedHead != confirmedHead {
					_ = s.store.QuarantineWorkspace(ctx, record.WorkspaceID, reservation.Authority, "target moved during ready publication", reservation.Now)
					return targetMoved(record, publishedHead, "repository target moved during ready publication")
				}
				return s.faults.Reach(ctx, "SR-09")
			})
			if err != nil {
				return Record{}, err
			}
			record = published
		case StageReady:
			if record.Status == "quarantined" {
				return Record{}, &Error{Kind: KindWorkspaceNotReady, Message: "ready workspace was quarantined after fact drift", Observed: map[string]string{"status": record.Status, "reason": record.QuarantineReason}, Expected: map[string]string{"status": "ready"}}
			}
			err := s.git.WithTargetAuthority(ctx, func(targetHead string) error {
				if targetHead != record.PinnedBase {
					_ = s.store.QuarantineWorkspace(ctx, record.WorkspaceID, reservation.Authority, "repository target drifted after ready publication", reservation.Now)
					return targetMoved(record, targetHead, "repository target moved after ready publication")
				}
				probe, err := s.git.Probe(ctx, record)
				if err != nil {
					_ = s.store.QuarantineWorkspace(ctx, record.WorkspaceID, reservation.Authority, "ready workspace probe failed", reservation.Now)
					return err
				}
				confirmedHead, err := s.git.TargetHead(ctx)
				if err != nil {
					_ = s.store.QuarantineWorkspace(ctx, record.WorkspaceID, reservation.Authority, "ready target re-probe failed", reservation.Now)
					return err
				}
				if confirmedHead != targetHead || !readyProbeMatches(record, probe) {
					_ = s.store.QuarantineWorkspace(ctx, record.WorkspaceID, reservation.Authority, "ready workspace facts drifted", reservation.Now)
					if confirmedHead != targetHead {
						return targetMoved(record, confirmedHead, "repository target moved during ready replay")
					}
					return notReady(record, probe, "replayed ready workspace")
				}
				return nil
			})
			if err != nil {
				return Record{}, err
			}
			return record, nil
		default:
			return Record{}, &Error{Kind: KindWorkspaceNotReady, Message: "workspace has an unknown preparation stage", Observed: map[string]string{"stage": string(record.Stage)}, Expected: map[string]string{"stage": string(StageReady)}}
		}
	}
}

func (s *Service) Validate(ctx context.Context, reservation Reservation) error {
	if err := s.authenticateBinding(ctx, reservation, true); err != nil {
		return err
	}
	if err := s.validateIdentity(ctx, reservation); err != nil {
		return err
	}
	targetHead, err := s.git.TargetHead(ctx)
	if err != nil {
		return err
	}
	if targetHead != reservation.PinnedBase {
		return targetMoved(Record{Reservation: reservation}, targetHead, "pinned base is not the repository target head")
	}
	return nil
}

// ValidateBinding authenticates the live canonical Git adapter against the
// repository facts persisted by the operation's original preflight. It never
// establishes or changes a binding, so exact replays cannot silently rebind a
// RepositoryID to another physical checkout.
func (s *Service) ValidateBinding(ctx context.Context, reservation Reservation) error {
	return s.authenticateBinding(ctx, reservation, false)
}

// ValidateRepositoryBinding authenticates a read-only or maintenance adapter
// against the durable canonical repository identity without requiring a lease
// token. Commands must call this before any cleanup mutation.
func (s *Service) ValidateRepositoryBinding(ctx context.Context, repositoryID ids.RepositoryID) error {
	live := s.git.Binding()
	if live.RepositoryID != repositoryID {
		return bindingMismatch(live, RepositoryBinding{RepositoryID: repositoryID}, "requested repository ID differs from the live canonical repository adapter")
	}
	durable, err := s.store.Repository(ctx, repositoryID)
	if err != nil {
		return err
	}
	if !sameCanonicalBinding(live, durable) {
		return bindingMismatch(live, durable, "live canonical repository facts do not match the durable repository binding")
	}
	return nil
}

func (s *Service) authenticateBinding(ctx context.Context, reservation Reservation, establish bool) error {
	if err := reservation.Validate(); err != nil {
		return err
	}
	live := s.git.Binding()
	if establish {
		if err := s.BindRepository(ctx, live); err != nil {
			return err
		}
	}
	return s.ValidateRepositoryBinding(ctx, reservation.RepositoryID)
}

func sameCanonicalBinding(left, right RepositoryBinding) bool {
	// Primary is a point-in-time integrity snapshot, not physical identity. Its
	// mutable HEAD is authenticated by TargetHead under target authority and by
	// the ready replay probes; treating it as identity would mask typed target
	// drift and prevent the required quarantine path.
	return left.RepositoryID == right.RepositoryID && left.ProjectID == right.ProjectID &&
		left.CommonDir == right.CommonDir && left.PrimaryPath == right.PrimaryPath &&
		left.ManagedRoot == right.ManagedRoot && left.TargetRef == right.TargetRef
}

func bindingMismatch(live, durable RepositoryBinding, message string) error {
	return &Error{
		Kind: KindBindingConflict, Message: message,
		Observed: bindingFacts(live),
		Expected: bindingFacts(durable),
	}
}

func bindingFacts(binding RepositoryBinding) map[string]string {
	return map[string]string{
		"repository_id":        binding.RepositoryID.String(),
		"project_id":           binding.ProjectID.String(),
		"common_dir":           binding.CommonDir,
		"primary_path":         binding.PrimaryPath,
		"managed_root":         binding.ManagedRoot,
		"target_ref":           binding.TargetRef,
		"primary_head":         binding.Primary.Head,
		"primary_index_digest": binding.Primary.IndexDigest,
		"primary_tree_digest":  binding.Primary.TreeDigest,
	}
}

func (s *Service) validateIdentity(ctx context.Context, reservation Reservation) error {
	if err := reservation.Validate(); err != nil {
		return err
	}
	binding, err := s.store.Repository(ctx, reservation.RepositoryID)
	if err != nil {
		return err
	}
	if binding.ProjectID != reservation.ProjectID {
		return &Error{Kind: KindInvalid, Message: "repository is not bound to the requested project", Observed: map[string]string{"project_id": reservation.ProjectID.String()}, Expected: map[string]string{"project_id": binding.ProjectID.String()}}
	}
	authority, err := s.store.WorkAuthority(ctx, reservation.WorkRevisionID, reservation.WorkID)
	if err != nil {
		return err
	}
	if authority.ProjectID != reservation.ProjectID || authority.MissionID != reservation.MissionID || authority.LaneID != reservation.LaneID {
		return &Error{Kind: KindInvalid, Message: "lane is not the finalized lane for this work revision", Observed: map[string]string{"project_id": reservation.ProjectID.String(), "mission_id": reservation.MissionID.String(), "lane_id": reservation.LaneID.String()}, Expected: map[string]string{"project_id": authority.ProjectID.String(), "mission_id": authority.MissionID.String(), "lane_id": authority.LaneID.String()}}
	}
	return nil
}

func readyProbeMatches(record Record, probe Probe) bool {
	return probe.Registered && probe.Clean && probe.Branch == record.OwnedBranch && probe.Head == record.DependencyHead && probe.GitDir == record.GitDir && probe.IndexPath == record.IndexPath
}

func targetMoved(record Record, observed, message string) error {
	return &Error{Kind: KindTargetMoved, Message: message, Observed: map[string]string{"target_head": observed}, Expected: map[string]string{"pinned_base": record.PinnedBase}}
}

func notReady(record Record, probe Probe, requirement string) error {
	return &Error{
		Kind: KindWorkspaceNotReady, Message: "workspace is not ready: " + requirement + " is unverified",
		Observed: map[string]string{"stage": string(record.Stage), "registered": fmt.Sprint(probe.Registered), "head": probe.Head, "branch": probe.Branch, "clean": fmt.Sprint(probe.Clean), "git_dir": probe.GitDir, "index_path": probe.IndexPath},
		Expected: map[string]string{"registered": "true", "head": record.DependencyHead, "branch": record.OwnedBranch, "clean": "true", "workspace": "verified ready"},
	}
}
