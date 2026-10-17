//go:build spec_kitty_expiry_host_qualification && linux && amd64

// The fixed functional experiment is deliberately absent from ordinary builds.
// Build normally with go build: do not manufacture linked-client build metadata.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
)

func main() {
	// No scenario/provider/admission selectors or ordinary CLI dispatch.
	if len(os.Args) != 2 {
		os.Exit(2) // sole argument is the authentic M1 adoption-record path
	}
	adoption := os.Args[1]
	if !filepath.IsAbs(adoption) {
		os.Exit(2)
	}
	inputs := productionruntime.ExpiryHostInitialDiagnosticInputs{
		Image:              os.Getenv("SPEC_KITTY_EXPIRY_HOST_TEST_IMAGE"),
		InputRecord:        os.Getenv("SPEC_KITTY_EXPIRY_HOST_INPUT"),
		RunParent:          os.Getenv("SPEC_KITTY_EXPIRY_HOST_RUN_PARENT"),
		SourceReviewSHA256: os.Getenv("SPEC_KITTY_EXPIRY_HOST_SOURCE_REVIEW_SHA256"),
		M1AdoptionRecord:   adoption,
	}
	report, err := productionruntime.RunExpiryHostInitialDiagnostic(context.Background(), inputs)
	if writeErr := json.NewEncoder(os.Stdout).Encode(report); writeErr != nil {
		os.Exit(2)
	}
	if err != nil {
		os.Exit(1)
	}
	if !report.DiagnosticPassed || report.Qualified {
		os.Exit(1)
	}
}
