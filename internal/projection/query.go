package projection

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type GetQueryV2 struct {
	Coordinate journalv2.AggregateCoordinate
	AtRevision uint64
}

func (a *AuthorityV2) Get(query GetQueryV2) (AggregateViewV2, error) {
	snapshot := a.Snapshot()
	if snapshot == nil {
		return AggregateViewV2{}, &ErrorV2{ErrorProjectionCorruptV2, "snapshot"}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if snapshot.revision != a.revision || query.AtRevision != 0 && query.AtRevision != a.revision {
		return AggregateViewV2{}, &ErrorV2{ErrorStaleCursorV2, "revision"}
	}
	keyBytes, err := journalv2.CanonicalAggregateCoordinate(query.Coordinate)
	if err != nil {
		return AggregateViewV2{}, &ErrorV2{ErrorInvalidQueryV2, "coordinate"}
	}
	view, ok := a.views[string(keyBytes)]
	if !ok {
		return AggregateViewV2{}, &ErrorV2{ErrorNotFoundV2, "aggregate"}
	}
	return a.attributedViewLockedV2(view, snapshot.projectionDigest), nil
}

type ListQueryV2 struct {
	Family    string
	ProjectID journalv2.ProjectID
	MissionID journalv2.MissionID
	WorkID    journalv2.WorkID
	Limit     int
	Cursor    string
}
type ListResultV2 struct {
	DatabaseID     journalv2.DatabaseID     `json:"database_id"`
	Revision       uint64                   `json:"revision"`
	RuntimeEpochID journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	Items          []AggregateViewV2        `json:"items"`
	NextCursor     string                   `json:"next_cursor,omitempty"`
}

// QueryPortV2 is the authority-free surface handed to API, CLI, dashboard,
// and optional MCP consumers. It deliberately omits acknowledgement and
// restore methods so an observer cannot publish or reconstruct state.
type QueryPortV2 interface {
	Get(GetQueryV2) (AggregateViewV2, error)
	List(ListQueryV2) (ListResultV2, error)
	Watch(journalv2.DatabaseID, uint64) (*WatchV2, error)
	ProgressBase(string) (ProgressBaseV2, error)
}

func (a *AuthorityV2) List(query ListQueryV2) (ListResultV2, error) {
	if query.Limit <= 0 || query.Limit > MaxQueryPageV2 || !validListFamilyV2(query.Family) || len(query.Cursor) > journalv2.MaxRecoveryCursorBytes ||
		len(query.ProjectID) > journalv2.MaxIdentityBytes || len(query.MissionID) > journalv2.MaxIdentityBytes || len(query.WorkID) > journalv2.MaxIdentityBytes {
		return ListResultV2{}, &ErrorV2{ErrorInvalidQueryV2, "budget"}
	}
	snapshot := a.Snapshot()
	if snapshot == nil {
		return ListResultV2{}, &ErrorV2{ErrorProjectionCorruptV2, "snapshot"}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.revision == 0 || snapshot.revision != a.revision {
		return ListResultV2{}, &ErrorV2{ErrorProjectionCorruptV2, "snapshot"}
	}
	after := ""
	if query.Cursor != "" {
		cursor, err := decodeCursorV2(query.Cursor)
		if err != nil || cursor.DatabaseID != a.databaseID || cursor.Revision != a.revision || cursor.ProjectionDigest != a.liveToken.String() || cursor.FilterDigest != listFilterDigestV2(query) {
			return ListResultV2{}, &ErrorV2{ErrorStaleCursorV2, "cursor"}
		}
		after = cursor.After
	}
	items := make([]AggregateViewV2, 0, query.Limit)
	total := 0
	next := ""
	keys := a.candidateKeysLockedV2(query)
	matching := make([]AggregateViewV2, 0, len(keys))
	for key := range keys {
		view := a.views[key]
		view = a.attributedViewLockedV2(view, snapshot.projectionDigest)
		if matchesListV2(view, query) {
			matching = append(matching, view)
		}
	}
	sort.Slice(matching, func(i, j int) bool {
		left, _ := journalv2.CanonicalAggregateCoordinate(matching[i].Coordinate)
		right, _ := journalv2.CanonicalAggregateCoordinate(matching[j].Coordinate)
		return bytes.Compare(left, right) < 0
	})
	for _, view := range matching {
		keyBytes, _ := journalv2.CanonicalAggregateCoordinate(view.Coordinate)
		key := string(keyBytes)
		if key <= after {
			continue
		}
		if len(items) == query.Limit {
			break
		}
		encoded, _ := json.Marshal(view)
		if len(encoded) > MaxQueryResponseV2-total {
			return ListResultV2{}, &ErrorV2{ErrorInvalidQueryV2, "response_budget"}
		}
		total += len(encoded)
		items = append(items, cloneViewV2(view))
		next = key
	}
	if len(items) < query.Limit || !hasMatchAfterV2(matching, query, next) {
		next = ""
	}
	result := ListResultV2{a.databaseID, a.revision, a.epoch, items, ""}
	if next != "" {
		result.NextCursor = encodeCursorV2(listCursorV2{DatabaseID: a.databaseID, Revision: a.revision, ProjectionDigest: a.liveToken.String(), FilterDigest: listFilterDigestV2(query), After: next})
	}
	if encoded, err := json.Marshal(result); err != nil || len(encoded) > MaxQueryResponseV2 {
		return ListResultV2{}, &ErrorV2{ErrorInvalidQueryV2, "response_budget"}
	}
	return result, nil
}

func validListFamilyV2(family string) bool {
	if family == "" {
		return true
	}
	if len(family) > 64 {
		return false
	}
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == family {
			return true
		}
	}
	return false
}

// ProgressBaseV2 supplies only exact projection attribution. The GapDB
// composition adapter combines it with WP18's opaque ActionCensus; this lower
// layer deliberately cannot accept caller-authored readiness claims.
type ProgressBaseV2 struct {
	DatabaseID     journalv2.DatabaseID     `json:"database_id"`
	Revision       uint64                   `json:"revision"`
	RuntimeEpochID journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	MissionID      string                   `json:"mission_id"`
}

func (a *AuthorityV2) ProgressBase(missionID string) (ProgressBaseV2, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.revision == 0 || missionID == "" || len(missionID) > journalv2.MaxIdentityBytes {
		return ProgressBaseV2{}, &ErrorV2{ErrorInvalidQueryV2, "mission_id"}
	}
	found := false
	for key := range a.byMission[missionID] {
		view := a.views[key]
		if view.Coordinate.Kind == "mission" && string(view.Coordinate.ID) == missionID && string(view.Coordinate.Scope.MissionID) == missionID {
			found = true
			break
		}
	}
	if !found {
		return ProgressBaseV2{}, &ErrorV2{ErrorNotFoundV2, "mission"}
	}
	return ProgressBaseV2{a.databaseID, a.revision, a.epoch, missionID}, nil
}

func (a *AuthorityV2) candidateKeysLockedV2(query ListQueryV2) map[string]struct{} {
	if query.WorkID != "" {
		return a.byWork[string(query.WorkID)]
	}
	if query.MissionID != "" {
		return a.byMission[string(query.MissionID)]
	}
	if query.ProjectID != "" {
		return a.byProject[string(query.ProjectID)]
	}
	if query.Family != "" {
		return a.byFamily[query.Family]
	}
	all := make(map[string]struct{}, len(a.views))
	for key := range a.views {
		all[key] = struct{}{}
	}
	return all
}

func (a *AuthorityV2) attributedViewLockedV2(view AggregateViewV2, canonicalDigest journalv2.Digest) AggregateViewV2 {
	view.DatabaseID = a.databaseID
	view.ProjectionRevision = a.revision
	view.RuntimeEpochID = a.epoch
	view.ProjectionDigest = canonicalDigest.String()
	return cloneViewV2(view)
}

type listCursorV2 struct {
	DatabaseID       journalv2.DatabaseID `json:"database_id"`
	Revision         uint64               `json:"revision"`
	ProjectionDigest string               `json:"projection_digest"`
	FilterDigest     string               `json:"filter_digest"`
	After            string               `json:"after"`
	Seal             string               `json:"seal"`
}

func encodeCursorV2(cursor listCursorV2) string {
	cursor.Seal = cursorSealV2(cursor)
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeCursorV2(value string) (listCursorV2, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursorV2{}, err
	}
	var cursor listCursorV2
	if json.Unmarshal(raw, &cursor) != nil || cursor.Seal == "" || cursor.Seal != cursorSealV2(cursor) {
		return listCursorV2{}, &ErrorV2{ErrorStaleCursorV2, "cursor_seal"}
	}
	return cursor, nil
}
func cursorSealV2(cursor listCursorV2) string {
	cursor.Seal = ""
	raw, _ := json.Marshal(cursor)
	sum := sha256.Sum256(append([]byte("spec-kitty/projection-cursor/v2\x00"), raw...))
	return hex.EncodeToString(sum[:])
}
func listFilterDigestV2(query ListQueryV2) string {
	raw, _ := json.Marshal(struct {
		Family  string
		Project string
		Mission string
		Work    string
	}{query.Family, string(query.ProjectID), string(query.MissionID), string(query.WorkID)})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func matchesListV2(view AggregateViewV2, query ListQueryV2) bool {
	scope := view.Coordinate.Scope
	return (query.Family == "" || string(view.Coordinate.Kind) == query.Family) && (query.ProjectID == "" || scope.ProjectID == query.ProjectID) && (query.MissionID == "" || scope.MissionID == query.MissionID) && (query.WorkID == "" || scope.WorkID == query.WorkID)
}
func hasMatchAfterV2(views []AggregateViewV2, query ListQueryV2, after string) bool {
	if after == "" {
		return false
	}
	for _, view := range views {
		key, _ := journalv2.CanonicalAggregateCoordinate(view.Coordinate)
		if string(key) > after && matchesListV2(view, query) {
			return true
		}
	}
	return false
}
func cloneViewV2(view AggregateViewV2) AggregateViewV2 {
	view.CanonicalState = bytes.Clone(view.CanonicalState)
	return view
}
func cloneViewsV2(views []AggregateViewV2) []AggregateViewV2 {
	result := make([]AggregateViewV2, len(views))
	for i := range views {
		result[i] = cloneViewV2(views[i])
	}
	return result
}
