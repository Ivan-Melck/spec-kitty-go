package hubnot

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"strings"
)

type workMapping struct {
	ProjectID ids.ProjectID
	WorkID    ids.WorkID
	CreatedBy ids.OperationID
}
type mappedIssue struct {
	Detail  issueDetail
	Content workContent
	Mapped  bool
}

func openingMapping(metadata map[string]string) (workMapping, bool, error) {
	owned := false
	for k := range metadata {
		owned = owned || strings.HasPrefix(k, ownedPrefix)
	}
	if !owned {
		return workMapping{}, false, nil
	}
	bad := func() (workMapping, bool, error) {
		return workMapping{}, true, protocolFailure(fmt.Errorf("invalid signed opening mapping"))
	}
	if metadata[ownedPrefix+"schema"] != codecSchema {
		return bad()
	}
	project, err := ids.ParseProjectID(metadata[ownedPrefix+"project"])
	if err != nil {
		return bad()
	}
	work, err := ids.ParseWorkID(metadata[ownedPrefix+"work"])
	if err != nil {
		return bad()
	}
	operation, err := ids.ParseOperationID(metadata[ownedPrefix+"created-by"])
	if err != nil {
		return bad()
	}
	return workMapping{project, work, operation}, true, nil
}
func mapDetail(d issueDetail, project ids.ProjectID) (mappedIssue, error) {
	if !validEventID(d.ID) || !validHex(d.Creator, 32) || d.OpeningMetadata == nil || d.HeadCount < 1 || d.Conflict != (d.HeadCount > 1) || d.Conflict != (d.State == nil) {
		return mappedIssue{}, protocolFailure(fmt.Errorf("contradictory detailed issue state"))
	}
	opening, mapped, err := openingMapping(d.OpeningMetadata)
	if err != nil {
		return mappedIssue{}, err
	}
	if mapped && opening.ProjectID != project {
		return mappedIssue{}, failure(tracker.ErrorWorkspaceMismatch, "reconcile_mapping", "signed mapping claims another project", nil)
	}
	if d.Conflict {
		return mappedIssue{}, failure(tracker.ErrorRevisionConflict, "reconcile_without_mutation", "inventory contains unresolved heads; supplier facts are incomplete", nil)
	}
	content, currentMapped, err := decodeWorkState(*d.State)
	if err != nil {
		return mappedIssue{}, err
	}
	if mapped != currentMapped || (mapped && (opening != (workMapping{content.ProjectID, content.WorkID, content.CreatedBy}))) {
		return mappedIssue{}, failure(tracker.ErrorAttributionLost, "reconcile_mapping", "current mapping disagrees with signed opening", nil)
	}
	state := cloneIssueState(*d.State)
	d.State = &state
	d.OpeningMetadata = cloneIssueState(issueState{Metadata: d.OpeningMetadata}).Metadata
	return mappedIssue{Detail: d, Content: cloneContent(content), Mapped: mapped}, nil
}
func (n mappedIssue) reference(revision tracker.Revision) tracker.ExternalWorkRef {
	return tracker.ExternalWorkRef{ProjectID: n.Content.ProjectID, WorkID: n.Content.WorkID, Provider: "hubnot", ExternalID: n.Detail.ID, MetadataSchema: 1, CreatedByOperation: n.Content.CreatedBy, LastObservation: revision}
}
func cloneMappedIssue(n mappedIssue) mappedIssue {
	n.Content = cloneContent(n.Content)
	if n.Detail.State != nil {
		s := cloneIssueState(*n.Detail.State)
		n.Detail.State = &s
	}
	n.Detail.OpeningMetadata = cloneIssueState(issueState{Metadata: n.Detail.OpeningMetadata}).Metadata
	return n
}
