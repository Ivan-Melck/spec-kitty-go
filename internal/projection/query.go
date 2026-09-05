package projection

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type GetQueryV2 struct {
	Coordinate journalv2.AggregateCoordinate
	AtRevision uint64
}

func (a *AuthorityV2) Get(query GetQueryV2) (AggregateViewV2, error) {
	snapshot := a.current.Load()
	if snapshot == nil || query.AtRevision != 0 && query.AtRevision != snapshot.revision {
		return AggregateViewV2{}, &ErrorV2{ErrorStaleCursorV2, "revision"}
	}
	keyBytes, err := journalv2.CanonicalAggregateCoordinate(query.Coordinate)
	if err != nil {
		return AggregateViewV2{}, &ErrorV2{ErrorInvalidQueryV2, "coordinate"}
	}
	position, ok := snapshot.byCoordinate[string(keyBytes)]
	if !ok {
		return AggregateViewV2{}, &ErrorV2{ErrorNotFoundV2, "aggregate"}
	}
	return cloneViewV2(snapshot.views[position]), nil
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
	if query.Limit <= 0 || query.Limit > MaxQueryPageV2 || len(query.Family) > 64 || len(query.Cursor) > journalv2.MaxRecoveryCursorBytes {
		return ListResultV2{}, &ErrorV2{ErrorInvalidQueryV2, "budget"}
	}
	snapshot := a.current.Load()
	if snapshot == nil {
		return ListResultV2{}, &ErrorV2{ErrorProjectionCorruptV2, "snapshot"}
	}
	after := ""
	if query.Cursor != "" {
		cursor, err := decodeCursorV2(query.Cursor)
		if err != nil || cursor.DatabaseID != snapshot.databaseID || cursor.Revision != snapshot.revision || cursor.ProjectionDigest != snapshot.projectionDigest.String() || cursor.FilterDigest != listFilterDigestV2(query) {
			return ListResultV2{}, &ErrorV2{ErrorStaleCursorV2, "cursor"}
		}
		after = cursor.After
	}
	items := make([]AggregateViewV2, 0, query.Limit)
	total := 0
	next := ""
	for _, view := range snapshot.views {
		keyBytes, _ := journalv2.CanonicalAggregateCoordinate(view.Coordinate)
		key := string(keyBytes)
		if key <= after || !matchesListV2(view, query) {
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
	if len(items) < query.Limit || !hasMatchAfterV2(snapshot.views, query, next) {
		next = ""
	}
	result := ListResultV2{snapshot.databaseID, snapshot.revision, snapshot.epoch, items, ""}
	if next != "" {
		result.NextCursor = encodeCursorV2(listCursorV2{DatabaseID: snapshot.databaseID, Revision: snapshot.revision, ProjectionDigest: snapshot.projectionDigest.String(), FilterDigest: listFilterDigestV2(query), After: next})
	}
	return result, nil
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
	snapshot := a.current.Load()
	if snapshot == nil || missionID == "" || len(missionID) > journalv2.MaxIdentityBytes {
		return ProgressBaseV2{}, &ErrorV2{ErrorInvalidQueryV2, "mission_id"}
	}
	found := false
	for _, view := range snapshot.views {
		if view.Coordinate.Kind == "mission" && string(view.Coordinate.ID) == missionID && string(view.Coordinate.Scope.MissionID) == missionID {
			found = true
			break
		}
	}
	if !found {
		return ProgressBaseV2{}, &ErrorV2{ErrorNotFoundV2, "mission"}
	}
	return ProgressBaseV2{snapshot.databaseID, snapshot.revision, snapshot.epoch, missionID}, nil
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
