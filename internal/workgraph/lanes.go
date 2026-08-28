package workgraph

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// LaneV2 is one immutable execution-serialization component. Dependency lane
// identities point from blocked work to its prerequisite lane, matching the
// work-item dependency representation.
type LaneV2 struct {
	id                ids.LaneID
	workIDs           []ids.WorkID
	dependencyLaneIDs []ids.LaneID
}

func (lane LaneV2) ID() ids.LaneID { return lane.id }
func (lane LaneV2) WorkIDs() []ids.WorkID {
	return append([]ids.WorkID(nil), lane.workIDs...)
}
func (lane LaneV2) DependencyLaneIDs() []ids.LaneID {
	return append([]ids.LaneID(nil), lane.dependencyLaneIDs...)
}

// CollapseLanes turns provider-neutral work into deterministic connected
// overlap components. It fail-closes on invalid raw scope values even when a
// caller bypasses the broader ValidateDraft quality gate.
func CollapseLanes(observation DraftObservation) ([]LaneV2, error) {
	work := observation.Work()
	if len(work) == 0 {
		return nil, newError(KindMissingNode, "lane projection requires work", nil)
	}
	sort.Slice(work, func(i, j int) bool { return work[i].WorkID() < work[j].WorkID() })
	index := make(map[ids.WorkID]int, len(work))
	for i, item := range work {
		if _, duplicate := index[item.WorkID()]; duplicate {
			return nil, newError(KindAmbiguousID, "lane projection contains duplicate work identity", map[string]string{"work_id": item.WorkID().String()})
		}
		index[item.WorkID()] = i
		scopeKeys := make(map[string]struct{}, len(item.WriteScopes()))
		for _, scope := range item.WriteScopes() {
			candidate := NormalizedWriteScope(scope)
			if !validNormalizedScope(candidate) {
				return nil, newError(KindInvalidRevision, "lane projection contains an invalid write scope", map[string]string{"work_id": item.WorkID().String()})
			}
			key := normalizedScopeString(candidate)
			if _, duplicate := scopeKeys[key]; duplicate {
				return nil, newError(KindInvalidRevision, "lane projection contains a duplicate write scope", map[string]string{"work_id": item.WorkID().String()})
			}
			scopeKeys[key] = struct{}{}
		}
	}
	parent := make([]int, len(work))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(value int) int {
		if parent[value] != value {
			parent[value] = find(parent[value])
		}
		return parent[value]
	}
	join := func(left, right int) {
		left, right = find(left), find(right)
		if left == right {
			return
		}
		if left < right {
			parent[right] = left
		} else {
			parent[left] = right
		}
	}
	for left := range work {
		for right := left + 1; right < len(work); right++ {
			if workScopesOverlap(work[left].WriteScopes(), work[right].WriteScopes()) {
				join(left, right)
			}
		}
	}
	components := make(map[int][]ids.WorkID)
	for i, item := range work {
		root := find(i)
		components[root] = append(components[root], item.WorkID())
	}
	lanes := make([]LaneV2, 0, len(components))
	workLane := make(map[ids.WorkID]ids.LaneID, len(work))
	for _, members := range components {
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		laneID := stableLaneID(members)
		for _, workID := range members {
			workLane[workID] = laneID
		}
		lanes = append(lanes, LaneV2{id: laneID, workIDs: append([]ids.WorkID(nil), members...)})
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].id < lanes[j].id })
	edges := make(map[ids.LaneID]map[ids.LaneID]struct{}, len(lanes))
	workEdges := make(map[string][]string, len(work))
	workNodes := make([]string, 0, len(work))
	for _, item := range work {
		workNodes = append(workNodes, item.WorkID().String())
		blockedLane := workLane[item.WorkID()]
		for _, dependency := range item.Dependencies() {
			if _, exists := index[dependency]; !exists {
				return nil, newError(KindMissingNode, "dependency references missing work", map[string]string{"work_id": item.WorkID().String(), "dependency_work_id": dependency.String()})
			}
			workEdges[item.WorkID().String()] = append(workEdges[item.WorkID().String()], dependency.String())
			dependencyLane := workLane[dependency]
			if dependencyLane == blockedLane {
				continue
			}
			if edges[blockedLane] == nil {
				edges[blockedLane] = make(map[ids.LaneID]struct{})
			}
			edges[blockedLane][dependencyLane] = struct{}{}
		}
	}
	if cyclic(workNodes, workEdges) {
		return nil, newError(KindCycle, "work dependency graph contains a cycle", nil)
	}
	laneEdges := make(map[string][]string, len(lanes))
	for i := range lanes {
		for dependency := range edges[lanes[i].id] {
			lanes[i].dependencyLaneIDs = append(lanes[i].dependencyLaneIDs, dependency)
			laneEdges[lanes[i].id.String()] = append(laneEdges[lanes[i].id.String()], dependency.String())
		}
		sort.Slice(lanes[i].dependencyLaneIDs, func(left, right int) bool {
			return lanes[i].dependencyLaneIDs[left] < lanes[i].dependencyLaneIDs[right]
		})
	}
	if implicated, cycle := firstCycleNode(laneEdges); cycle {
		laneID := ids.LaneID(implicated)
		fields := map[string]string{"lane_id": implicated}
		for _, lane := range lanes {
			if lane.id == laneID {
				values := make([]string, 0, 3)
				length := 0
				for _, member := range lane.workIDs {
					additional := len(member.String())
					if len(values) != 0 {
						additional++
					}
					if len(values) == 3 || length+additional > 128 {
						break
					}
					values = append(values, member.String())
					length += additional
				}
				fields["work_ids"] = strings.Join(values, ",")
				break
			}
		}
		return nil, newError(KindLaneCycle, "dependency graph becomes cyclic after write-scope collapse", fields)
	}
	return lanes, nil
}

func workScopesOverlap(left, right []WriteScopeObservation) bool {
	for _, a := range left {
		if a.Access != ScopeWriteExclusive {
			continue
		}
		for _, b := range right {
			if b.Access != ScopeWriteExclusive {
				continue
			}
			if a.SerializationKey != "" && a.SerializationKey == b.SerializationKey {
				return true
			}
			if pathsOverlap(a.Path, b.Path) {
				return true
			}
		}
	}
	return false
}

func stableLaneID(workIDs []ids.WorkID) ids.LaneID {
	material := "spec-kitty/lane/v2\x00"
	for _, workID := range workIDs {
		material += workID.String() + "\x00"
	}
	sum := sha256.Sum256([]byte(material))
	return ids.LaneID(fmt.Sprintf("lane-%x", sum[:12]))
}
