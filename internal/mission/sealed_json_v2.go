package mission

import (
	"bytes"
	"encoding/json"
	"io"
)

// decodeCanonicalSealedV2 decodes reducer bytes after the control journal has
// established recursive canonical JSON. Typed membership and trailing-data
// checks remain mandatory; only duplicate normalization is not repeated.
func decodeCanonicalSealedV2(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return err
	}
	return nil
}
