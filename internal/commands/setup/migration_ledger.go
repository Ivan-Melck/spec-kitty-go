package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/migrationledger"
)

const migrationLedgerBackfillID = "4.0.0rc8_backfill_migration_ledger"

func migrationByID(id string) (migrationInfo, bool) {
	for _, migration := range migrationCatalog {
		if migration.ID == id {
			return migration, true
		}
	}
	return migrationInfo{}, false
}

func migrationRecord(m migrationInfo, result, appliedAt string) migrationledger.Record {
	if appliedAt == "" {
		appliedAt = clock.NowUTCISO()
	}
	identity := m.Identity
	if identity == "" {
		identity = m.ID
	}
	return migrationledger.Record{ID: m.ID, AppliedAt: appliedAt, Result: result, Identity: identity,
		ContentHash: m.ContentHash, CLIVersion: app.Version, CloneLocal: m.CloneLocal}
}

func readMigrationRecords(root string, cloneLocal bool) ([]migrationledger.Record, error) {
	records, err := (migrationledger.Ledger{Root: root}).Read(cloneLocal)
	if err != nil {
		return nil, err
	}
	var normalized []migrationledger.Record
	index := map[string]int{}
	for _, record := range records {
		if id, ok := kittify.LegacyMigrationID(record.ID); ok {
			record.ID = id
		}
		if i, ok := index[record.ID]; ok {
			normalized[i] = record
		} else {
			index[record.ID] = len(normalized)
			normalized = append(normalized, record)
		}
	}
	return normalized, nil
}

func migrationSettled(root string, m migrationInfo) bool {
	records, err := readMigrationRecords(root, m.CloneLocal)
	if err != nil {
		return false
	}
	for _, record := range records {
		if record.ID == m.ID && record.Result == "success" {
			return true
		}
	}
	return false
}

func cloneLocalEffectInPlace(root string, m migrationInfo) bool {
	if !gitx.IsRepo(root) {
		return false
	}
	impl := m.impl()
	if impl.detect == nil && impl.detectE == nil {
		return false
	}
	needed, err := m.detectErr(root)
	return err == nil && !needed
}

func seedMigrationLedger(root string) error { _, err := seedMigrationLedgerCount(root); return err }

func seedMigrationLedgerCount(root string) (int, error) {
	count := 0
	ledger := migrationledger.Ledger{Root: root}
	for _, cloneLocal := range []bool{false, true} {
		if _, err := ledger.Read(cloneLocal); err != nil {
			return 0, err
		}
	}
	err := dct.LegacyActivationScope([]string{root}, func() error {
		for _, m := range migrationCatalog {
			if migrationSettled(root, m) || m.CloneLocal && !cloneLocalEffectInPlace(root, m) {
				continue
			}
			if err := ledger.Append(migrationRecord(m, "success", "")); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func migrationAdoptionPlan(root string) ([]migrationledger.Record, error) {
	md := kittify.LoadMetadata(root)
	if md == nil {
		return nil, nil
	}
	tracked, err := readMigrationRecords(root, false)
	if err != nil {
		return nil, err
	}
	local, err := readMigrationRecords(root, true)
	if err != nil {
		return nil, err
	}
	recorded := map[string]bool{}
	trackedIDs, localIDs := map[string]bool{}, map[string]bool{}
	for _, pair := range []struct {
		records []migrationledger.Record
		ids     map[string]bool
	}{{tracked, trackedIDs}, {local, localIDs}} {
		for _, record := range pair.records {
			pair.ids[record.ID] = true
			recorded[record.ID] = true
		}
	}
	var legacy []kittify.MigrationRecord
	index := map[string]int{}
	for _, record := range md.Applied {
		if id, ok := kittify.LegacyMigrationID(record.ID); ok {
			record.ID = id
		}
		if i, ok := index[record.ID]; ok {
			if record.Result == "success" {
				legacy[i] = record
			}
		} else {
			index[record.ID] = len(legacy)
			legacy = append(legacy, record)
		}
	}
	var plan []migrationledger.Record
	for _, old := range legacy {
		recorded[old.ID] = true
		m, known := migrationByID(old.ID)
		if !known {
			m.ID, m.Identity = old.ID, old.ID
		}
		if m.CloneLocal {
			if localIDs[m.ID] || !cloneLocalEffectInPlace(root, m) {
				continue
			}
		} else if trackedIDs[m.ID] {
			continue
		}
		plan = append(plan, migrationRecord(m, old.Result, old.AppliedAt))
	}
	backfill, _ := migrationByID(migrationLedgerBackfillID)
	if !migrationSettled(root, backfill) {
		now := clock.NowUTCISO()
		for _, m := range migrationCatalog {
			if m.ID == migrationLedgerBackfillID || recorded[m.ID] {
				continue
			}
			beforeRecorded, validRecorded := versionCmp(m.TargetVersion, md.Version)
			beforeLedger, validLedger := versionCmp(m.TargetVersion, backfill.TargetVersion)
			if !validRecorded || !validLedger || beforeRecorded >= 0 || beforeLedger >= 0 {
				continue
			}
			if m.CloneLocal && !cloneLocalEffectInPlace(root, m) {
				continue
			}
			plan = append(plan, migrationRecord(m, "success", now))
		}
	}
	return plan, nil
}

func init() {
	migrationImpls[migrationLedgerBackfillID] = migrationImpl{
		noWorktrees: true,
		detectE:     func(root string) (bool, error) { plan, err := migrationAdoptionPlan(root); return len(plan) > 0, err },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			plan, err := migrationAdoptionPlan(root)
			if err != nil {
				return migrationResult{}, err
			}
			result := migrationResult{Success: true}
			if len(plan) == 0 {
				result.Changes = []string{"migration ledger already backfilled"}
				return result, nil
			}
			if dryRun {
				result.Changes = []string{fmt.Sprintf("Would backfill %d ledger record(s)", len(plan))}
				return result, nil
			}
			if err := (migrationledger.Ledger{Root: root}).AppendMany(plan); err != nil {
				return migrationResult{}, err
			}
			for _, record := range plan {
				partition := "tracked"
				if record.CloneLocal {
					partition = "clone-local"
				}
				result.Changes = append(result.Changes, fmt.Sprintf("Backfilled %s into the %s ledger", record.ID, partition))
			}
			return result, nil
		},
	}
}

func migrationLedgerAdopted(root string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(migrationledger.TrackedRelative)))
	return err == nil && st.Mode().IsRegular()
}

func migrationRepositoryProbe(root string) (bool, error) {
	result, err := gitx.Exec(root, nil, nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return false, err
	}
	if result.Code == 0 {
		return true, nil
	}
	if strings.Contains(result.Stderr, "not a git repository") {
		return false, nil
	}
	return false, fmt.Errorf("%s", strings.TrimSpace(result.Stderr))
}
