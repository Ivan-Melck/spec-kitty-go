package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type FailureMode string

const (
	FailureUnavailable        FailureMode = "unavailable"
	FailureTimeoutBeforeApply FailureMode = "timeout_before_apply"
	FailureTimeoutAfterApply  FailureMode = "timeout_after_apply"
	FailureRevisionConflict   FailureMode = "revision_conflict"
)

type WorkSeed struct {
	Key          string
	Title        string
	Description  string
	Criteria     []string
	Dependencies []string
	Assignee     string
}

type HistoryEntry struct {
	Kind     string           `json:"kind"`
	WorkKey  string           `json:"work_key"`
	Value    string           `json:"value,omitempty"`
	Marker   string           `json:"marker,omitempty"`
	Revision tracker.Revision `json:"revision"`
}

type Store struct {
	path     string
	lockPath string
}

type diskState struct {
	Version              uint16                         `json:"version"`
	Works                map[string]*diskWork           `json:"works"`
	Failures             []FailureMode                  `json:"failures,omitempty"`
	Compensated          map[string]tracker.Revision    `json:"compensated,omitempty"`
	History              []HistoryEntry                 `json:"history,omitempty"`
	DefinitionOperations map[string]definitionOperation `json:"definition_operations,omitempty"`
	DefinitionRequests   map[string]string              `json:"definition_requests,omitempty"`
}

type definitionOperation struct {
	Digest  string `json:"digest"`
	WorkKey string `json:"work_key"`
}

type diskWork struct {
	Key              string            `json:"key"`
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	Criteria         []string          `json:"criteria,omitempty"`
	Dependencies     []string          `json:"dependencies,omitempty"`
	Assignee         string            `json:"assignee,omitempty"`
	AssignmentMarker string            `json:"assignment_marker,omitempty"`
	Comments         []tracker.Comment `json:"comments,omitempty"`
	Revision         tracker.Revision  `json:"revision"`
	Definition       *diskDefinition   `json:"definition,omitempty"`
}

type diskDefinition struct {
	ProjectID          ids.ProjectID           `json:"project_id"`
	WorkID             ids.WorkID              `json:"work_id"`
	Title              string                  `json:"title"`
	Description        string                  `json:"description"`
	DesignContext      string                  `json:"design_context"`
	CrossDomainReason  string                  `json:"cross_domain_reason,omitempty"`
	Criteria           []tracker.WorkCriterion `json:"criteria"`
	Dependencies       []ids.WorkID            `json:"dependencies,omitempty"`
	WriteScopes        []tracker.WriteScope    `json:"write_scopes"`
	Provenance         []tracker.ScopeRef      `json:"provenance"`
	MetadataSchema     uint16                  `json:"metadata_schema"`
	ProviderMetadata   json.RawMessage         `json:"provider_metadata,omitempty"`
	CreatedByOperation ids.OperationID         `json:"created_by_operation"`
	ObservedAt         time.Time               `json:"observed_at"`
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("canonicalize fake tracker store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("create fake tracker directory: %w", err)
	}
	store := &Store{path: abs, lockPath: abs + ".lock"}
	if err := store.update(context.Background(), func(state *diskState) error { return nil }); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Seed(ctx context.Context, seed WorkSeed) error {
	if seed.Key == "" || seed.Title == "" {
		return &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work key and title are required"}
	}
	return s.update(ctx, func(state *diskState) error {
		if _, exists := state.Works[seed.Key]; exists {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "work already exists"}
		}
		state.Works[seed.Key] = &diskWork{Key: seed.Key, Title: seed.Title, Description: seed.Description, Criteria: clone(seed.Criteria), Dependencies: clone(seed.Dependencies), Assignee: seed.Assignee, Revision: 1}
		state.History = append(state.History, HistoryEntry{Kind: "seeded", WorkKey: seed.Key, Value: seed.Assignee, Revision: 1})
		return nil
	})
}

func (s *Store) Inject(ctx context.Context, failure FailureMode) error {
	switch failure {
	case FailureUnavailable, FailureTimeoutBeforeApply, FailureTimeoutAfterApply, FailureRevisionConflict:
	default:
		return &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "unknown failure mode"}
	}
	return s.update(ctx, func(state *diskState) error {
		state.Failures = append(state.Failures, failure)
		return nil
	})
}

func (s *Store) HumanAssign(ctx context.Context, workKey, assignee string) error {
	return s.update(ctx, func(state *diskState) error {
		work, ok := state.Works[workKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		work.Revision++
		work.Assignee = assignee
		work.AssignmentMarker = ""
		state.History = append(state.History, HistoryEntry{Kind: "human_assignment", WorkKey: workKey, Value: assignee, Revision: work.Revision})
		return nil
	})
}

// HumanEditTitle simulates an unattributed concurrent provider writer. It is
// intentionally explicit so guarded-update tests never rely on timing.
func (s *Store) HumanEditTitle(ctx context.Context, workKey, title string) error {
	if title == "" {
		return &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "title is required"}
	}
	return s.update(ctx, func(state *diskState) error {
		work, ok := state.Works[workKey]
		if !ok || work.Definition == nil {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work definition does not exist"}
		}
		work.Revision++
		work.Title = title
		work.Definition.Title = title
		work.Definition.ObservedAt = time.Now().UTC()
		state.History = append(state.History, HistoryEntry{Kind: "human_definition_edit", WorkKey: workKey, Value: title, Revision: work.Revision})
		return nil
	})
}

func (s *Store) History(ctx context.Context) ([]HistoryEntry, error) {
	var history []HistoryEntry
	err := s.read(ctx, func(state diskState) error {
		history = append(history, state.History...)
		return nil
	})
	return history, err
}

func (s *Store) read(ctx context.Context, fn func(diskState) error) error {
	lock, err := s.lock(ctx, syscall.LOCK_SH)
	if err != nil {
		return err
	}
	defer unlock(lock)
	state, err := s.load()
	if err != nil {
		return err
	}
	return fn(state)
}

func (s *Store) update(ctx context.Context, fn func(*diskState) error) error {
	lock, err := s.lock(ctx, syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock(lock)
	state, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(&state); err != nil {
		return err
	}
	return s.save(state)
}

func (s *Store) lock(ctx context.Context, mode int) (*os.File, error) {
	file, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open fake tracker lock: %w", err)
	}
	for {
		if err := syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB); err == nil {
			return file, nil
		} else if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = file.Close()
			return nil, fmt.Errorf("lock fake tracker store: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		default:
			syscall.Nanosleep(&syscall.Timespec{Nsec: 1_000_000}, nil)
		}
	}
}

func unlock(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (s *Store) load() (diskState, error) {
	state := diskState{Version: 1, Works: map[string]*diskWork{}, Compensated: map[string]tracker.Revision{}, DefinitionOperations: map[string]definitionOperation{}, DefinitionRequests: map[string]string{}}
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("open fake tracker store: %w", err)
	}
	defer file.Close()
	if err := json.NewDecoder(file).Decode(&state); err != nil && err != io.EOF {
		return state, fmt.Errorf("decode fake tracker store: %w", err)
	}
	if state.Version != 1 {
		return state, fmt.Errorf("unsupported fake tracker store version %d", state.Version)
	}
	if state.Works == nil {
		state.Works = map[string]*diskWork{}
	}
	if state.Compensated == nil {
		state.Compensated = map[string]tracker.Revision{}
	}
	if state.DefinitionOperations == nil {
		state.DefinitionOperations = map[string]definitionOperation{}
	}
	if state.DefinitionRequests == nil {
		state.DefinitionRequests = map[string]string{}
	}
	return state, nil
}

func (s *Store) save(state diskState) error {
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".tracker-*.tmp")
	if err != nil {
		return fmt.Errorf("create fake tracker temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(state); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode fake tracker store: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync fake tracker store: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close fake tracker store: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace fake tracker store: %w", err)
	}
	directory, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return fmt.Errorf("open fake tracker directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync fake tracker directory: %w", err)
	}
	return nil
}

func clone(values []string) []string { return append([]string(nil), values...) }
