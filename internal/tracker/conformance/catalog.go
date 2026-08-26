package conformance

import (
	"context"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func catalogCases() []Case {
	return []Case{
		{ID: "catalog.read.complete", Supported: func(c tracker.ProviderCapabilities) bool { return c.CatalogRead == tracker.SupportSupported }, Exercise: exerciseCatalogRead},
		{ID: "catalog.create.replay", Supported: func(c tracker.ProviderCapabilities) bool { return c.CatalogCreate == tracker.SupportSupported }, Exercise: exerciseCatalogCreate},
		{ID: "catalog.update.guarantee", Supported: func(c tracker.ProviderCapabilities) bool { return c.CatalogUpdate != tracker.UpdateUnsupported }, Exercise: exerciseCatalogUpdate},
	}
}

func exerciseCatalogRead(ctx context.Context, fixture Fixture, supported bool) error {
	if fixture.Catalog == nil {
		return fmt.Errorf("catalog port is absent")
	}
	record, err := fixture.Catalog.ReadWork(ctx, fixture.Reference)
	if !supported {
		return expectKind(err, tracker.ErrorCapabilityUnsupported)
	}
	if err != nil {
		return err
	}
	if record.ExternalRef.ProjectID != fixture.Reference.ProjectID || record.ExternalRef.WorkID != fixture.Reference.WorkID || record.ExternalRef.ExternalID != fixture.Reference.ExternalID || record.Observation.Revision == 0 || record.Title == "" {
		return fmt.Errorf("incomplete catalog observation")
	}
	return nil
}

func exerciseCatalogCreate(ctx context.Context, fixture Fixture, supported bool) error {
	request := tracker.CreateWorkRequest{ProjectID: fixture.Reference.ProjectID, WorkID: ids.WorkID("work-conformance-create"), OperationID: ids.OperationID("operation-conformance-create"), Title: "portable create", MetadataSchema: 1}
	first, err := fixture.Catalog.CreateWork(ctx, request)
	if !supported {
		return expectKind(err, tracker.ErrorCapabilityUnsupported)
	}
	if err != nil {
		return err
	}
	second, err := fixture.Catalog.CreateWork(ctx, request)
	if err != nil {
		return err
	}
	if first.Record.ExternalRef.WorkID != request.WorkID || !second.Replayed || !second.Noop || first.Record.Observation.Revision != second.Record.Observation.Revision {
		return fmt.Errorf("create replay was not exact")
	}
	return nil
}

func exerciseCatalogUpdate(ctx context.Context, fixture Fixture, supported bool) error {
	record, readErr := fixture.Catalog.ReadWork(ctx, fixture.Reference)
	if readErr != nil && supported {
		return readErr
	}
	confirmed, confirmErr := fixture.Catalog.ReadWork(ctx, fixture.Reference)
	if confirmErr != nil && supported {
		return confirmErr
	}
	if supported && confirmed.Observation.Revision != record.Observation.Revision {
		return fmt.Errorf("catalog baseline changed during read-only confirmation")
	}
	record = confirmed
	request := tracker.UpdateWorkRequest{ExternalRef: fixture.Reference, OperationID: "operation-conformance-update", ExpectedObservation: record.Observation.Revision, RequiredGuard: tracker.UpdateUnguarded, Title: "portable update"}
	first, err := fixture.Catalog.UpdateWork(ctx, request)
	if !supported {
		return expectKind(err, tracker.ErrorCapabilityUnsupported, tracker.ErrorGuardedUpdateUnsupported)
	}
	if err != nil {
		return err
	}
	replayRequest := request
	replayRequest.ExpectedObservation = first.Record.Observation.Revision
	second, err := fixture.Catalog.UpdateWork(ctx, replayRequest)
	if err != nil {
		return err
	}
	if first.Record.Title != request.Title || !second.Replayed || !second.Noop {
		return fmt.Errorf("update observation or replay is incomplete")
	}
	return nil
}
