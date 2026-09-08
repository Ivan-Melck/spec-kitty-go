package m2

import (
	"bytes"
	"testing"
)

func TestDeterministicCycloneDXBOMIsByteIdentical(t *testing.T) {
	root := repositoryRoot(t)
	first, err := GenerateSBOM(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateSBOM(root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identical source produced different SBOM bytes")
	}
}
