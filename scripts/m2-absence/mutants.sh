#!/bin/sh
set -eu

repository=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)
cd -- "$repository"

exec go test ./internal/architecture -count=1 \
  -run '^(TestM2AbsenceMutants|TestM2AbsenceSymlinkAndHistoricalDiscrimination|TestM2RetiredBlobCensusIsClosed|TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed)$'
