package m2

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const AttemptEventsName = "attempt-events.jsonl"

type Attempt struct {
	BundleDirectory string
	FinalBundle     string
	Directory       string
	FinalDirectory  string
	EventDirectory  string
	RunID           string
	Source          Source
	mu              sync.Mutex
	sequence        int
	sealed          bool
}

type AttemptEvent struct {
	SchemaVersion int       `json:"schema_version"`
	RunID         string    `json:"run_id"`
	SourceCommit  string    `json:"source_commit"`
	SourceTree    string    `json:"source_tree"`
	Sequence      int       `json:"sequence"`
	Stage         string    `json:"stage"`
	Outcome       string    `json:"outcome"`
	ObservedAt    time.Time `json:"observed_at"`
	Diagnostic    string    `json:"diagnostic,omitempty"`
}

type AttemptTerminal struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	SourceCommit  string `json:"source_commit"`
	SourceTree    string `json:"source_tree"`
	Verdict       string `json:"verdict"`
	EventsSHA256  string `json:"events_sha256"`
}

func ReserveAttempt(outputRoot, runID string, source Source) (*Attempt, error) {
	if !filepath.IsAbs(outputRoot) || !safeRunID(runID) {
		return nil, fmt.Errorf("absolute output root and safe RunID are required")
	}
	if err := validateSource(source); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(outputRoot, ".run-"+runID+".lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return nil, fmt.Errorf("RunID is already reserved: %w", err)
	}
	if _, err := lock.WriteString(source.Commit + " " + source.Tree + "\n"); err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := lock.Sync(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := lock.Close(); err != nil {
		return nil, err
	}
	stagingRoot := filepath.Join(outputRoot, ".attempts")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return nil, err
	}
	attempt := &Attempt{
		BundleDirectory: filepath.Join(stagingRoot, runID),
		FinalBundle:     filepath.Join(outputRoot, runID),
		Directory:       filepath.Join(stagingRoot, runID, "evidence"),
		FinalDirectory:  filepath.Join(outputRoot, runID, "evidence"),
		EventDirectory:  filepath.Join(stagingRoot, runID+".events"),
		RunID:           runID,
		Source:          source,
	}
	if err := os.Mkdir(attempt.BundleDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("reserve attempt staging directory: %w", err)
	}
	if err := os.Mkdir(attempt.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("reserve attempt evidence directory: %w", err)
	}
	if err := os.Mkdir(attempt.EventDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("reserve attempt event directory: %w", err)
	}
	if err := attempt.Record("reservation", "running", ""); err != nil {
		return nil, err
	}
	return attempt, nil
}

func (attempt *Attempt) Record(stage, outcome, diagnostic string) error {
	if attempt == nil {
		return fmt.Errorf("attempt is nil")
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if attempt.sealed {
		return fmt.Errorf("attempt is sealed")
	}
	if !safeToken(stage) || !validOutcome(outcome) {
		return fmt.Errorf("attempt event is invalid")
	}
	attempt.sequence++
	event := AttemptEvent{SchemaVersion: 1, RunID: attempt.RunID, SourceCommit: attempt.Source.Commit, SourceTree: attempt.Source.Tree, Sequence: attempt.sequence, Stage: stage, Outcome: outcome, ObservedAt: time.Now().UTC(), Diagnostic: boundedDiagnostic(diagnostic)}
	data, err := CanonicalJSON(event)
	if err != nil {
		return err
	}
	return writeCreateOnce(filepath.Join(attempt.EventDirectory, fmt.Sprintf("%06d.json", attempt.sequence)), data)
}

func (attempt *Attempt) Capture(path string, data []byte) error {
	if attempt == nil || !safeRelativePath(path) || path == ManifestName || path == CensusName || len(data) > maximumFile {
		return fmt.Errorf("attempt artifact path or size is invalid")
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if attempt.sealed {
		return fmt.Errorf("attempt is sealed")
	}
	return writeCreateOnce(filepath.Join(attempt.Directory, filepath.FromSlash(path)), data)
}

func (attempt *Attempt) TerminalBytes(verdict string, cause error) ([]byte, []byte, error) {
	if attempt == nil || !validTerminal(verdict) {
		return nil, nil, fmt.Errorf("terminal verdict is invalid")
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if attempt.sealed {
		return nil, nil, fmt.Errorf("attempt is sealed")
	}
	var events []byte
	for sequence := 1; sequence <= attempt.sequence; sequence++ {
		data, err := os.ReadFile(filepath.Join(attempt.EventDirectory, fmt.Sprintf("%06d.json", sequence)))
		if err != nil {
			return nil, nil, err
		}
		events = append(events, data...)
	}
	terminalEvent := AttemptEvent{SchemaVersion: 1, RunID: attempt.RunID, SourceCommit: attempt.Source.Commit, SourceTree: attempt.Source.Tree, Sequence: attempt.sequence + 1, Stage: "terminal", Outcome: verdict, ObservedAt: time.Now().UTC(), Diagnostic: boundedDiagnostic(errorText(cause))}
	data, err := CanonicalJSON(terminalEvent)
	if err != nil {
		return nil, nil, err
	}
	events = append(events, data...)
	terminal := AttemptTerminal{SchemaVersion: 1, RunID: attempt.RunID, SourceCommit: attempt.Source.Commit, SourceTree: attempt.Source.Tree, Verdict: verdict, EventsSHA256: SHA256(events)}
	terminalBytes, err := CanonicalJSON(terminal)
	return events, terminalBytes, err
}

func (attempt *Attempt) SealNonPass(verdict string, cause error) error {
	if verdict != "fail" && verdict != "incomplete" && verdict != "non_admissible" {
		return fmt.Errorf("non-pass verdict is invalid")
	}
	events, terminal, err := attempt.TerminalBytes(verdict, cause)
	if err != nil {
		return err
	}
	for _, path := range []string{ManifestName, CensusName, AttemptEventsName, "terminal.json"} {
		_ = os.Remove(filepath.Join(attempt.Directory, path))
	}
	if err := writeCreateOnce(filepath.Join(attempt.Directory, AttemptEventsName), events); err != nil {
		return err
	}
	if err := writeCreateOnce(filepath.Join(attempt.Directory, "terminal.json"), terminal); err != nil {
		return err
	}
	attempt.mu.Lock()
	attempt.sealed = true
	attempt.mu.Unlock()
	if err := os.Rename(attempt.BundleDirectory, attempt.FinalBundle); err != nil {
		return fmt.Errorf("publish non-pass attempt: %w", err)
	}
	_ = os.RemoveAll(attempt.EventDirectory)
	return nil
}

func (attempt *Attempt) PublishPass() error {
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if attempt.sealed {
		return fmt.Errorf("attempt is sealed")
	}
	if err := os.Rename(attempt.BundleDirectory, attempt.FinalBundle); err != nil {
		return fmt.Errorf("publish pass attempt: %w", err)
	}
	attempt.sealed = true
	_ = os.RemoveAll(attempt.EventDirectory)
	return nil
}

func VerifyNonPassAttempt(directory string) (AttemptTerminal, error) {
	var terminal AttemptTerminal
	events, err := os.ReadFile(filepath.Join(directory, AttemptEventsName))
	if err != nil {
		return terminal, err
	}
	data, err := os.ReadFile(filepath.Join(directory, "terminal.json"))
	if err != nil {
		return terminal, err
	}
	if err := DecodeStrict(data, &terminal); err != nil {
		return terminal, err
	}
	runID := filepath.Base(directory)
	if runID == "evidence" {
		runID = filepath.Base(filepath.Dir(directory))
	}
	if terminal.SchemaVersion != 1 || terminal.RunID != runID || !isHex(terminal.SourceCommit, 40) || !isHex(terminal.SourceTree, 40) || terminal.EventsSHA256 != SHA256(events) || !validTerminal(terminal.Verdict) {
		return terminal, fmt.Errorf("attempt terminal binding is invalid")
	}
	scanner := bufio.NewScanner(bytes.NewReader(events))
	sequence := 0
	last := AttemptEvent{}
	for scanner.Scan() {
		var event AttemptEvent
		if err := DecodeStrict(append(bytes.Clone(scanner.Bytes()), '\n'), &event); err != nil {
			return terminal, err
		}
		sequence++
		if event.RunID != terminal.RunID || event.SourceCommit != terminal.SourceCommit || event.SourceTree != terminal.SourceTree || event.Sequence != sequence {
			return terminal, fmt.Errorf("attempt event sequence or identity differs")
		}
		last = event
	}
	if err := scanner.Err(); err != nil {
		return terminal, err
	}
	if sequence == 0 || last.Stage != "terminal" || last.Outcome != terminal.Verdict {
		return terminal, fmt.Errorf("attempt terminal event is absent or differs")
	}
	return terminal, nil
}

func validOutcome(value string) bool {
	return value == "running" || value == "pass" || validTerminal(value)
}

func validTerminal(value string) bool {
	return value == "pass" || value == "fail" || value == "incomplete" || value == "non_admissible"
}

func safeRunID(value string) bool {
	if len(value) == 0 || len(value) > 128 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func safeToken(value string) bool { return safeRunID(value) }

func boundedDiagnostic(value string) string {
	value = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, value)
	if len(value) > 2048 {
		value = value[:2048]
	}
	return value
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
