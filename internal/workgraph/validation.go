package workgraph

import (
	"path"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type FindingKind string
type FindingSeverity string
type FindingRecovery string

const (
	FindingMissingContext          FindingKind = "missing_context"
	FindingMalformedCriterion      FindingKind = "malformed_criterion"
	FindingInvalidBoundary         FindingKind = "invalid_boundary"
	FindingCriterionNotObservable  FindingKind = "criterion_not_observable"
	FindingWorkOverBroad           FindingKind = "work_over_broad"
	FindingUnrelatedOwnership      FindingKind = "unrelated_ownership"
	FindingMissingCoverage         FindingKind = "missing_coverage"
	FindingOrphanWork              FindingKind = "orphan_work"
	FindingUnknownScope            FindingKind = "unknown_scope"
	FindingScopeKindMismatch       FindingKind = "scope_kind_mismatch"
	FindingOwnershipConflict       FindingKind = "ownership_conflict"
	FindingUnknownDependency       FindingKind = "unknown_dependency"
	FindingSelfDependency          FindingKind = "self_dependency"
	FindingDuplicateDependency     FindingKind = "duplicate_dependency"
	FindingDependencyCycle         FindingKind = "dependency_cycle"
	FindingUnknownLane             FindingKind = "unknown_lane"
	FindingMissingLaneMembership   FindingKind = "missing_lane_membership"
	FindingDuplicateLaneMembership FindingKind = "duplicate_lane_membership"
	FindingLaneSelfEdge            FindingKind = "lane_self_edge"
	FindingLaneCycle               FindingKind = "lane_cycle"
	FindingOrderingConstraintLost  FindingKind = "ordering_constraint_lost"

	SeverityError            FindingSeverity = "error"
	RecoveryReviseWork       FindingRecovery = "revise_work"
	RecoveryReviseProjection FindingRecovery = "revise_lane_projection"
)

type Finding struct {
	Kind          FindingKind     `json:"kind"`
	Severity      FindingSeverity `json:"severity"`
	WorkID        ids.WorkID      `json:"work_id,omitempty"`
	RelatedWorkID ids.WorkID      `json:"related_work_id,omitempty"`
	Field         string          `json:"field"`
	Message       string          `json:"message"`
	Recovery      FindingRecovery `json:"recovery"`
}

type QualityPolicy struct {
	MaxCriteria         int
	MaxDependencies     int
	MaxWriteScopes      int
	MaxProvenance       int
	MaxOwnershipDomains int
}

func DefaultQualityPolicy() QualityPolicy {
	return QualityPolicy{MaxCriteria: 32, MaxDependencies: 32, MaxWriteScopes: 16, MaxProvenance: 64, MaxOwnershipDomains: 1}
}

func ValidateDraft(observation DraftObservation, policy QualityPolicy) []Finding {
	if policy.MaxCriteria <= 0 || policy.MaxDependencies <= 0 || policy.MaxWriteScopes <= 0 || policy.MaxProvenance <= 0 || policy.MaxOwnershipDomains <= 0 {
		policy = DefaultQualityPolicy()
	}
	work := observation.Work()
	workByID := make(map[ids.WorkID]WorkObservation, len(work))
	approved := make(map[string]ScopeKind, len(observation.ApprovedScope()))
	required := make(map[string]bool)
	covered := make(map[string]bool)
	for _, scope := range observation.ApprovedScope() {
		approved[scope.ID] = scope.Kind
		if scope.Kind == ScopeRequiredOutcome {
			required[scope.ID] = true
		}
	}
	findings := make([]Finding, 0)
	for _, item := range work {
		definition := item
		workByID[definition.WorkID()] = definition
		criteria, dependencies, scopes, provenance := definition.Criteria(), definition.Dependencies(), definition.WriteScopes(), definition.Provenance()
		if !boundedText(definition.Title(), 160) {
			findings = append(findings, finding(FindingMissingContext, definition.WorkID(), "objective", "work requires a bounded specific objective", RecoveryReviseWork))
		}
		if !boundedText(definition.Description(), 4096) {
			findings = append(findings, finding(FindingMissingContext, definition.WorkID(), "description", "work requires a bounded standalone description", RecoveryReviseWork))
		}
		if !boundedText(definition.DesignContext(), 4096) {
			findings = append(findings, finding(FindingMissingContext, definition.WorkID(), "design_context", "work requires bounded design context", RecoveryReviseWork))
		}
		criterionIDs := make(map[string]bool, len(criteria))
		if len(criteria) == 0 {
			findings = append(findings, finding(FindingMalformedCriterion, definition.WorkID(), "criteria", "work requires at least one structured criterion", RecoveryReviseWork))
		}
		for _, criterion := range criteria {
			validID := stableToken(criterion.ID)
			if !validID || criterionIDs[criterion.ID] {
				findings = append(findings, finding(FindingMalformedCriterion, definition.WorkID(), "criteria.id", "criterion identity must be unique and bounded", RecoveryReviseWork))
			} else {
				criterionIDs[criterion.ID] = true
			}
			validText := boundedText(criterion.Text, 1024)
			if !validText {
				findings = append(findings, finding(FindingMalformedCriterion, definition.WorkID(), "criteria.text", "criterion activity must be explicit and bounded", RecoveryReviseWork))
			}
			validOutcome := boundedText(criterion.Outcome, 1024)
			if !validOutcome {
				findings = append(findings, finding(FindingMalformedCriterion, definition.WorkID(), "criteria.outcome", "criterion outcome must be explicit and bounded", RecoveryReviseWork))
			}
			if !validVerifier(criterion.Verifier) {
				findings = append(findings, finding(FindingMalformedCriterion, definition.WorkID(), "criteria.verifier", "criterion verifier must use a supported kind", RecoveryReviseWork))
			}
			if criterion.Requirement != CriterionRequired && criterion.Requirement != CriterionOptional {
				findings = append(findings, finding(FindingMalformedCriterion, definition.WorkID(), "criteria.requirement", "criterion must explicitly be required or optional", RecoveryReviseWork))
			}
			if validText && validOutcome && strings.EqualFold(strings.TrimSpace(criterion.Text), strings.TrimSpace(criterion.Outcome)) {
				findings = append(findings, finding(FindingCriterionNotObservable, definition.WorkID(), "criteria.outcome", "criterion activity and observable outcome must be distinct", RecoveryReviseWork))
			}
		}
		if len(criteria) > policy.MaxCriteria || len(dependencies) > policy.MaxDependencies || len(scopes) > policy.MaxWriteScopes || len(provenance) > policy.MaxProvenance {
			findings = append(findings, finding(FindingWorkOverBroad, definition.WorkID(), "work", "work exceeds an explicit structural decomposition limit", RecoveryReviseWork))
		}
		domains := map[string]bool{}
		scopeKeys := make(map[string]bool, len(scopes))
		if len(scopes) == 0 {
			findings = append(findings, finding(FindingInvalidBoundary, definition.WorkID(), "write_scopes", "work requires at least one explicit ownership boundary", RecoveryReviseWork))
		}
		for _, scope := range scopes {
			validPath := validRepositoryPath(scope.Path)
			if !validPath {
				findings = append(findings, finding(FindingInvalidBoundary, definition.WorkID(), "write_scopes.path", "write scope path must be bounded and repository relative", RecoveryReviseWork))
			}
			validDomain := stableToken(scope.Domain)
			if !validDomain {
				findings = append(findings, finding(FindingInvalidBoundary, definition.WorkID(), "write_scopes.domain", "write scope domain must be a bounded stable identity", RecoveryReviseWork))
			}
			validAccess := scope.Access == ScopeReadOnly || scope.Access == ScopeWriteExclusive
			if !validAccess {
				findings = append(findings, finding(FindingInvalidBoundary, definition.WorkID(), "write_scopes.access", "write scope access must be explicit", RecoveryReviseWork))
			}
			if scope.SerializationKey != "" && !stableToken(scope.SerializationKey) {
				findings = append(findings, finding(FindingInvalidBoundary, definition.WorkID(), "write_scopes.serialization_key", "serialization identity must be bounded and stable", RecoveryReviseWork))
			}
			if validPath && validAccess {
				key := scope.Path + "\x00" + string(scope.Access)
				if scopeKeys[key] {
					findings = append(findings, finding(FindingInvalidBoundary, definition.WorkID(), "write_scopes", "write scope boundary occurs more than once", RecoveryReviseWork))
				}
				scopeKeys[key] = true
			}
			if validDomain && scope.Access == ScopeWriteExclusive {
				domains[scope.Domain] = true
			}
		}
		if len(domains) > policy.MaxOwnershipDomains && definition.CrossDomainReason() == "" {
			findings = append(findings, finding(FindingUnrelatedOwnership, definition.WorkID(), "write_scopes", "multiple ownership domains require an explicit reason", RecoveryReviseWork))
		}
		hasPlanConcern := false
		for _, reference := range provenance {
			if !stableToken(reference.ID) || !validScopeKind(reference.Kind) {
				findings = append(findings, finding(FindingUnknownScope, definition.WorkID(), "provenance", "work contains an invalid approved-scope reference", RecoveryReviseWork))
				continue
			}
			kind, exists := approved[reference.ID]
			if !exists {
				findings = append(findings, finding(FindingUnknownScope, definition.WorkID(), "provenance", "work references scope absent from the approved plan", RecoveryReviseWork))
				continue
			}
			if kind != reference.Kind {
				findings = append(findings, finding(FindingScopeKindMismatch, definition.WorkID(), "provenance.kind", "work changes the approved scope classification", RecoveryReviseWork))
				continue
			}
			covered[reference.ID] = true
			if kind == ScopePlanConcern {
				hasPlanConcern = true
			}
		}
		if !hasPlanConcern {
			findings = append(findings, finding(FindingOrphanWork, definition.WorkID(), "provenance", "work is not traced to an approved plan concern", RecoveryReviseWork))
		}
	}
	for scopeID := range required {
		if !covered[scopeID] {
			findings = append(findings, Finding{Kind: FindingMissingCoverage, Severity: SeverityError, Field: "approved_scope." + scopeID, Message: "required approved outcome has no work", Recovery: RecoveryReviseWork})
		}
	}
	findings = append(findings, validateOwnership(workByID)...)
	findings = append(findings, validateDependencies(workByID)...)
	return canonicalFindings(findings)
}

func ValidateLaneProjection(observation DraftObservation, projection LaneProjection) []Finding {
	workByID := make(map[ids.WorkID]WorkObservation)
	for _, item := range observation.Work() {
		workByID[item.WorkID()] = item
	}
	findings := make([]Finding, 0)
	laneOccurrences := make(map[string][]ProjectedLane)
	for _, lane := range projection.Lanes {
		if _, err := ids.ParseLaneID(lane.ID); err != nil {
			findings = append(findings, finding(FindingUnknownLane, "", "lanes.identity", "lane identity must be bounded and stable", RecoveryReviseProjection))
			continue
		}
		laneOccurrences[lane.ID] = append(laneOccurrences[lane.ID], lane)
	}
	laneByID := make(map[string]ProjectedLane)
	laneIDs := make([]string, 0, len(laneOccurrences))
	for laneID, occurrences := range laneOccurrences {
		laneIDs = append(laneIDs, laneID)
		if len(occurrences) != 1 {
			findings = append(findings, finding(FindingUnknownLane, "", "lanes.identity", "lane identity occurs more than once", RecoveryReviseProjection))
			continue
		}
		laneByID[laneID] = occurrences[0]
	}
	sort.Strings(laneIDs)
	membershipCounts := make(map[ids.WorkID]map[string]int)
	for _, laneID := range laneIDs {
		lane, usable := laneByID[laneID]
		if !usable {
			continue
		}
		members := append([]ids.WorkID(nil), lane.Members...)
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		for _, workID := range members {
			if _, err := ids.ParseWorkID(workID.String()); err != nil {
				findings = append(findings, finding(FindingUnknownLane, "", "lanes.members", "lane contains an invalid work identity", RecoveryReviseProjection))
				continue
			}
			if _, exists := workByID[workID]; !exists {
				findings = append(findings, finding(FindingUnknownLane, workID, "lanes.members", "lane contains an unknown work identity", RecoveryReviseProjection))
				continue
			}
			if membershipCounts[workID] == nil {
				membershipCounts[workID] = make(map[string]int)
			}
			membershipCounts[workID][laneID]++
		}
	}
	membership := make(map[ids.WorkID]string)
	for workID := range workByID {
		lanes := membershipCounts[workID]
		if len(lanes) == 0 {
			findings = append(findings, finding(FindingMissingLaneMembership, workID, "lanes", "work is absent from the lane projection", RecoveryReviseProjection))
			continue
		}
		total := 0
		oneLane := ""
		for laneID, count := range lanes {
			total += count
			oneLane = laneID
		}
		if len(lanes) != 1 || total != 1 {
			findings = append(findings, finding(FindingDuplicateLaneMembership, workID, "lanes.membership", "work occurs more than once in the lane projection", RecoveryReviseProjection))
			continue
		}
		membership[workID] = oneLane
	}
	edges := make(map[string]bool)
	adjacency := make(map[string][]string)
	edgeInput := append([]ProjectedLaneEdge(nil), projection.Edges...)
	sort.Slice(edgeInput, func(i, j int) bool {
		if edgeInput[i].BlockingLaneID == edgeInput[j].BlockingLaneID {
			return edgeInput[i].BlockedLaneID < edgeInput[j].BlockedLaneID
		}
		return edgeInput[i].BlockingLaneID < edgeInput[j].BlockingLaneID
	})
	for _, edge := range edgeInput {
		if _, err := ids.ParseLaneID(edge.BlockingLaneID); err != nil {
			findings = append(findings, finding(FindingUnknownLane, "", "edges.blocking_lane", "edge contains an invalid blocking lane identity", RecoveryReviseProjection))
			continue
		}
		if _, err := ids.ParseLaneID(edge.BlockedLaneID); err != nil {
			findings = append(findings, finding(FindingUnknownLane, "", "edges.blocked_lane", "edge contains an invalid blocked lane identity", RecoveryReviseProjection))
			continue
		}
		if _, exists := laneByID[edge.BlockingLaneID]; !exists {
			findings = append(findings, finding(FindingUnknownLane, "", "edges.blocking_lane", "edge references an unknown blocking lane", RecoveryReviseProjection))
			continue
		}
		if _, exists := laneByID[edge.BlockedLaneID]; !exists {
			findings = append(findings, finding(FindingUnknownLane, "", "edges.blocked_lane", "edge references an unknown blocked lane", RecoveryReviseProjection))
			continue
		}
		if edge.BlockingLaneID == edge.BlockedLaneID {
			findings = append(findings, finding(FindingLaneSelfEdge, "", "edges", "lane cannot depend on itself", RecoveryReviseProjection))
			continue
		}
		key := edge.BlockingLaneID + "\x00" + edge.BlockedLaneID
		if edges[key] {
			continue
		}
		edges[key] = true
		adjacency[edge.BlockingLaneID] = append(adjacency[edge.BlockingLaneID], edge.BlockedLaneID)
	}
	for workID, definition := range workByID {
		blockedLane := membership[workID]
		for _, dependency := range definition.Dependencies() {
			blockingLane := membership[dependency]
			if blockingLane != "" && blockedLane != "" && blockingLane != blockedLane && !edges[blockingLane+"\x00"+blockedLane] {
				findings = append(findings, finding(FindingOrderingConstraintLost, workID, "dependencies", "lane projection omits a work dependency ordering constraint", RecoveryReviseProjection))
			}
		}
	}
	if _, cyclic := firstCycleNode(adjacency); cyclic {
		findings = append(findings, finding(FindingLaneCycle, "", "edges", "lane projection contains a cycle", RecoveryReviseProjection))
	}
	return canonicalFindings(findings)
}

func validateOwnership(work map[ids.WorkID]WorkObservation) []Finding {
	idsSorted := make([]ids.WorkID, 0, len(work))
	for id := range work {
		idsSorted = append(idsSorted, id)
	}
	sort.Slice(idsSorted, func(i, j int) bool { return idsSorted[i] < idsSorted[j] })
	findings := []Finding{}
	for i, leftID := range idsSorted {
		for _, rightID := range idsSorted[i+1:] {
			for _, left := range work[leftID].WriteScopes() {
				for _, right := range work[rightID].WriteScopes() {
					if left.Access != ScopeWriteExclusive || right.Access != ScopeWriteExclusive || !pathsOverlap(left.Path, right.Path) {
						continue
					}
					if left.SerializationKey != "" && left.SerializationKey == right.SerializationKey {
						continue
					}
					item := finding(FindingOwnershipConflict, leftID, "write_scopes.overlap", "exclusive write scope overlaps independent work", RecoveryReviseWork)
					item.RelatedWorkID = rightID
					findings = append(findings, item)
				}
			}
		}
	}
	return findings
}

func validateDependencies(work map[ids.WorkID]WorkObservation) []Finding {
	nodes := make([]ids.WorkID, 0, len(work))
	edges := make([]DependencyEdge, 0)
	for workID, definition := range work {
		nodes = append(nodes, workID)
		for _, dependency := range definition.Dependencies() {
			edges = append(edges, DependencyEdge{BlockedWorkID: workID, BlockingWorkID: dependency})
		}
	}
	return ValidateDependencyGraph(nodes, edges)
}

// DependencyEdge is the raw pre-lane graph seam. It admits malformed input so
// each invalid edge shape yields stable identities before finalization.
type DependencyEdge struct {
	BlockedWorkID  ids.WorkID
	BlockingWorkID ids.WorkID
}

func ValidateDependencyGraph(nodes []ids.WorkID, edges []DependencyEdge) []Finding {
	known := make(map[ids.WorkID]bool, len(nodes))
	findings := make([]Finding, 0)
	for _, node := range nodes {
		if _, err := ids.ParseWorkID(node.String()); err != nil {
			findings = append(findings, finding(FindingUnknownDependency, "", "nodes", "node identity is invalid or duplicated", RecoveryReviseWork))
			continue
		}
		if known[node] {
			findings = append(findings, finding(FindingUnknownDependency, node, "nodes", "node identity is invalid or duplicated", RecoveryReviseWork))
			continue
		}
		known[node] = true
	}
	seen := make(map[string]bool, len(edges))
	adjacency := make(map[string][]string)
	for _, edge := range edges {
		field := "dependencies"
		_, blockedErr := ids.ParseWorkID(edge.BlockedWorkID.String())
		_, blockingErr := ids.ParseWorkID(edge.BlockingWorkID.String())
		if blockedErr != nil || blockingErr != nil {
			item := finding(FindingUnknownDependency, "", field, "dependency endpoint identity is invalid", RecoveryReviseWork)
			if blockedErr == nil {
				item.WorkID = edge.BlockedWorkID
			}
			if blockingErr == nil {
				item.RelatedWorkID = edge.BlockingWorkID
			}
			findings = append(findings, item)
			continue
		}
		key := edge.BlockingWorkID.String() + "\x00" + edge.BlockedWorkID.String()
		if seen[key] {
			item := finding(FindingDuplicateDependency, edge.BlockedWorkID, field, "dependency edge occurs more than once", RecoveryReviseWork)
			item.RelatedWorkID = edge.BlockingWorkID
			findings = append(findings, item)
			continue
		}
		seen[key] = true
		if edge.BlockedWorkID == edge.BlockingWorkID {
			item := finding(FindingSelfDependency, edge.BlockedWorkID, field, "work cannot depend on itself", RecoveryReviseWork)
			item.RelatedWorkID = edge.BlockingWorkID
			findings = append(findings, item)
			continue
		}
		if !known[edge.BlockedWorkID] || !known[edge.BlockingWorkID] {
			item := finding(FindingUnknownDependency, edge.BlockedWorkID, field, "dependency endpoint is absent from observed work", RecoveryReviseWork)
			item.RelatedWorkID = edge.BlockingWorkID
			findings = append(findings, item)
			continue
		}
		adjacency[edge.BlockingWorkID.String()] = append(adjacency[edge.BlockingWorkID.String()], edge.BlockedWorkID.String())
	}
	if node, cyclic := firstCycleNode(adjacency); cyclic {
		findings = append(findings, finding(FindingDependencyCycle, ids.WorkID(node), "dependencies", "work dependency graph contains a cycle", RecoveryReviseWork))
	}
	return canonicalFindings(findings)
}

func pathsOverlap(left, right string) bool {
	left = path.Clean(left)
	right = path.Clean(right)
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
func firstCycleNode(adjacency map[string][]string) (string, bool) {
	state := map[string]uint8{}
	stack := make([]string, 0)
	var visit func(string) (string, bool)
	visit = func(node string) (string, bool) {
		if state[node] == 1 {
			for _, candidate := range stack {
				if candidate == node {
					return node, true
				}
			}
			return node, true
		}
		if state[node] == 2 {
			return "", false
		}
		state[node] = 1
		stack = append(stack, node)
		nextNodes := append([]string(nil), adjacency[node]...)
		sort.Strings(nextNodes)
		for _, next := range nextNodes {
			if cycle, found := visit(next); found {
				return cycle, true
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = 2
		return "", false
	}
	nodes := make([]string, 0, len(adjacency))
	for node := range adjacency {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if cycle, found := visit(node); found {
			return cycle, true
		}
	}
	return "", false
}
func finding(kind FindingKind, workID ids.WorkID, field, message string, recovery FindingRecovery) Finding {
	return Finding{Kind: kind, Severity: SeverityError, WorkID: workID, Field: field, Message: message, Recovery: recovery}
}
func canonicalFindings(findings []Finding) []Finding {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].WorkID != findings[j].WorkID {
			return findings[i].WorkID < findings[j].WorkID
		}
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		if findings[i].Field != findings[j].Field {
			return findings[i].Field < findings[j].Field
		}
		if findings[i].RelatedWorkID != findings[j].RelatedWorkID {
			return findings[i].RelatedWorkID < findings[j].RelatedWorkID
		}
		if findings[i].Message != findings[j].Message {
			return findings[i].Message < findings[j].Message
		}
		return findings[i].Recovery < findings[j].Recovery
	})
	result := findings[:0]
	for _, item := range findings {
		if len(result) == 0 || result[len(result)-1] != item {
			result = append(result, item)
		}
	}
	return result
}

func boundedText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 && character != '\n' && character != '\t' {
			return false
		}
	}
	return true
}

func validVerifier(value string) bool {
	return value == "test" || value == "review" || value == "policy" || value == "human_decision"
}

func validScopeKind(kind ScopeKind) bool {
	return kind == ScopePlanConcern || kind == ScopeRequiredOutcome || kind == ScopeOptionalOutcome
}

func validRepositoryPath(value string) bool {
	return value != "" && len(value) <= 512 && !path.IsAbs(value) && path.Clean(value) == value && value != "." && !strings.HasPrefix(value, "../") && !strings.Contains(value, "\\")
}
