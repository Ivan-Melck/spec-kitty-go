package beads

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const observationEncodingVersion uint16 = 1

type observationDependency struct {
	Blocked  string `json:"blocked"`
	Blocking string `json:"blocking"`
	Kind     string `json:"kind"`
	EdgeID   string `json:"edge_id"`
}

type observationComment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	Marker    string `json:"marker"`
	CreatedAt string `json:"created_at"`
}

// catalogObservation contains only known recovery-relevant provider fields.
// Pointers distinguish a missing provider value from an explicitly empty one.
type catalogObservation struct {
	ExternalID       string
	Title            *string
	Description      *string
	DesignContext    *string
	Acceptance       *string
	Metadata         []byte
	Labels           []string
	Dependencies     []observationDependency
	Assignee         *string
	AssignmentMarker *string
	Comments         []observationComment
	UpdatedAt        *time.Time
}

type observationIdentity struct {
	Canonical []byte
	Digest    [sha256.Size]byte
	Revision  tracker.Revision
}

func deriveObservationIdentity(value catalogObservation) observationIdentity {
	var canonical bytes.Buffer
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], observationEncodingVersion)
	canonical.Write(version[:])
	writeCanonicalString(&canonical, "external_id", &value.ExternalID)
	writeCanonicalString(&canonical, "title", value.Title)
	writeCanonicalString(&canonical, "description", value.Description)
	writeCanonicalString(&canonical, "design_context", value.DesignContext)
	writeCanonicalString(&canonical, "acceptance", value.Acceptance)
	metadata := string(value.Metadata)
	if value.Metadata == nil {
		writeCanonicalString(&canonical, "metadata", nil)
	} else {
		writeCanonicalString(&canonical, "metadata", &metadata)
	}
	labels := append([]string(nil), value.Labels...)
	sort.Strings(labels)
	writeCanonicalStrings(&canonical, "labels", labels)
	dependencies := append([]observationDependency(nil), value.Dependencies...)
	sort.Slice(dependencies, func(i, j int) bool {
		left, right := dependencies[i], dependencies[j]
		if left.Blocked != right.Blocked {
			return left.Blocked < right.Blocked
		}
		if left.Blocking != right.Blocking {
			return left.Blocking < right.Blocking
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.EdgeID < right.EdgeID
	})
	writeCanonicalCount(&canonical, "dependencies", len(dependencies))
	for _, dependency := range dependencies {
		writeCanonicalString(&canonical, "blocked", &dependency.Blocked)
		writeCanonicalString(&canonical, "blocking", &dependency.Blocking)
		writeCanonicalString(&canonical, "kind", &dependency.Kind)
		writeCanonicalString(&canonical, "edge_id", &dependency.EdgeID)
	}
	writeCanonicalString(&canonical, "assignee", value.Assignee)
	writeCanonicalString(&canonical, "assignment_marker", value.AssignmentMarker)
	// Provider order is preserved for discussion because it is user-visible and
	// may carry operation chronology.
	writeCanonicalCount(&canonical, "comments", len(value.Comments))
	for _, comment := range value.Comments {
		writeCanonicalString(&canonical, "comment_id", &comment.ID)
		writeCanonicalString(&canonical, "comment_author", &comment.Author)
		writeCanonicalString(&canonical, "comment_body", &comment.Body)
		writeCanonicalString(&canonical, "comment_marker", &comment.Marker)
		writeCanonicalString(&canonical, "comment_created_at", &comment.CreatedAt)
	}
	if value.UpdatedAt == nil {
		writeCanonicalString(&canonical, "updated_at", nil)
	} else {
		normalized := value.UpdatedAt.UTC().Format(time.RFC3339Nano)
		writeCanonicalString(&canonical, "updated_at", &normalized)
	}
	digest := sha256.Sum256(canonical.Bytes())
	return observationIdentity{Canonical: canonical.Bytes(), Digest: digest, Revision: revisionFromDigest(digest)}
}

func writeCanonicalString(destination *bytes.Buffer, name string, value *string) {
	writeLengthPrefixed(destination, []byte(name))
	if value == nil {
		destination.WriteByte(0)
		return
	}
	destination.WriteByte(1)
	writeLengthPrefixed(destination, []byte(*value))
}

func writeCanonicalStrings(destination *bytes.Buffer, name string, values []string) {
	writeCanonicalCount(destination, name, len(values))
	for _, value := range values {
		writeLengthPrefixed(destination, []byte(value))
	}
}

func writeCanonicalCount(destination *bytes.Buffer, name string, count int) {
	writeLengthPrefixed(destination, []byte(name))
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(count))
	destination.Write(encoded[:])
}

func writeLengthPrefixed(destination *bytes.Buffer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	destination.Write(length[:])
	destination.Write(value)
}

// revisionFromDigest folds the first eight SHA-256 bytes in network byte
// order. Zero is reserved for "no observation", so that single prefix maps to
// one while the full digest remains available for collision diagnosis.
func revisionFromDigest(digest [sha256.Size]byte) tracker.Revision {
	value := binary.BigEndian.Uint64(digest[:8])
	if value == 0 {
		value = 1
	}
	return tracker.Revision(value)
}

func digestString(digest [sha256.Size]byte) string { return hex.EncodeToString(digest[:]) }
