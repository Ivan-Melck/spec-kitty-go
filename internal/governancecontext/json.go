package governancecontext

import (
	"encoding/json"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type commitmentJSON struct {
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}

func (c Commitment) MarshalJSON() ([]byte, error) {
	return json.Marshal(commitmentJSON{Algorithm: c.Algorithm(), Digest: c.Digest()})
}

func (c *Commitment) UnmarshalJSON(data []byte) error {
	var wire commitmentJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	value, err := NewCommitment(wire.Algorithm, wire.Digest)
	if err != nil {
		return err
	}
	*c = value
	return nil
}

type entryJSON struct {
	Kind       EntryKind  `json:"kind"`
	Ref        string     `json:"ref"`
	Revision   string     `json:"revision"`
	Commitment Commitment `json:"commitment"`
}

func (e Entry) MarshalJSON() ([]byte, error) {
	return json.Marshal(entryJSON{
		Kind:       e.Kind(),
		Ref:        e.Ref(),
		Revision:   e.Revision(),
		Commitment: e.Commitment(),
	})
}

func (e *Entry) UnmarshalJSON(data []byte) error {
	var wire entryJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	value, err := NewEntry(wire.Kind, wire.Ref, wire.Revision, wire.Commitment)
	if err != nil {
		return err
	}
	*e = value
	return nil
}

type contextJSON struct {
	ID            ids.GovernanceContextID `json:"id"`
	SchemaVersion uint16                  `json:"schema_version"`
	Entries       []Entry                 `json:"entries"`
	Aggregate     Commitment              `json:"aggregate"`
	CreatedAt     time.Time               `json:"created_at"`
}

func (c Context) MarshalJSON() ([]byte, error) {
	return json.Marshal(contextJSON{
		ID:            c.ID(),
		SchemaVersion: c.SchemaVersion(),
		Entries:       c.Entries(),
		Aggregate:     c.Aggregate(),
		CreatedAt:     c.CreatedAt(),
	})
}

func (c *Context) UnmarshalJSON(data []byte) error {
	var wire contextJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	value, err := RestoreContext(wire.ID, wire.SchemaVersion, wire.Entries, wire.Aggregate, wire.CreatedAt)
	if err != nil {
		return err
	}
	*c = value
	return nil
}
