package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	binary := flag.String("beads-bin", os.Getenv("BEADS_BIN"), "absolute path to the qualified bd executable")
	digest := flag.String("digest", os.Getenv("BEADS_DIGEST_SHA256"), "reviewed SHA-256 for the exact qualified executable")
	evidence := flag.String("evidence", "", "new absolute path for schema-1 qualification evidence")
	repository := flag.String("repository", "", "absolute clean path to the source repository")
	timeout := flag.Duration("timeout", 20*time.Minute, "bounded real-provider campaign deadline")
	keep := flag.Bool("keep-fixture", false, "retain the sensitive temporary fixture for local debugging")
	flag.Parse()
	if *repository == "" {
		working, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolve repository: unavailable")
			os.Exit(2)
		}
		*repository = filepath.Clean(working)
	}
	if *evidence == "" {
		fmt.Fprintln(os.Stderr, "-evidence is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := runQualification(ctx, options{
		BinaryPath: *binary, Digest: *digest, Evidence: *evidence,
		Repository: *repository, Timeout: *timeout, Keep: *keep,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "beads qualification: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("verdict=%s scenarios=%d/%d evidence=%s\n", result.Verdict, result.Scenarios.Passed, result.Scenarios.Expected, *evidence)
}
