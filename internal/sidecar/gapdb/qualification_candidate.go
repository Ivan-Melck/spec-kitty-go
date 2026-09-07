package gapdb

import storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"

const (
	M2QualificationCandidateRecordSHA256 = "0849525884de248ab1e544343a0e6d12cfa78c383767cc3115fd418676bed274"
	m2QualificationProgramID             = "production-runtime"
	m2QualificationMissionID             = "gapdb-only-production-authority-01M1GWDJ"
	m2QualificationState                 = "technical_candidate_requires_independent_review"
)

// VerifyM2QualificationCandidateAdmission verifies the exact technical
// candidate tuple. The resulting capability permits qualification execution;
// it is not an adoption or release-readiness decision.
func VerifyM2QualificationCandidateAdmission(recordPath string) (ExecutableAdmission, error) {
	if err := validateAbsolutePath(recordPath); err != nil {
		return ExecutableAdmission{}, failure(FailureInvalidConfiguration, "verify_m2_candidate", "candidate record path is not absolute and normalized", err)
	}
	if err := validateRecordPath(recordPath); err != nil {
		return ExecutableAdmission{}, failure(FailureUnsafePath, "verify_m2_candidate", "candidate record path ownership or mode is unsafe", err)
	}
	recordBytes, recordDigest, err := readExactNoFollow(recordPath, maximumAdoptionRecordSize)
	if err != nil || recordDigest != M2QualificationCandidateRecordSHA256 {
		return ExecutableAdmission{}, failure(FailureAdmissionMismatch, "verify_m2_candidate", "candidate record differs", err)
	}
	var record admissionRecord
	if !singleStrictJSONValue(recordBytes, &record) || !validM2QualificationCandidate(record) {
		return ExecutableAdmission{}, failure(FailureAdmissionMismatch, "verify_m2_candidate", "candidate coordinates differ", nil)
	}
	return ExecutableAdmission{
		decision:       "qualify",
		recordSHA256:   recordDigest,
		sourceRevision: storegapdb.QualificationClientSourceRevision,
		sourceTree:     storegapdb.QualificationClientSourceTree,
		moduleVersion:  storegapdb.QualificationClientModuleVersion,
		sidecarSHA256:  storegapdb.QualificationSidecarSHA256,
		executableSize: storegapdb.QualificationSidecarBytes,
	}, nil
}

func validM2QualificationCandidate(record admissionRecord) bool {
	return record.SchemaVersion == 1 && record.ProgramID == m2QualificationProgramID &&
		record.MissionID == m2QualificationMissionID && record.Decision == "qualify" &&
		record.QualificationState == m2QualificationState && !record.ReleaseReady && !record.Adopted &&
		record.GapDBTuple.SourceRevision == storegapdb.QualificationClientSourceRevision &&
		record.GapDBTuple.SourceTree == storegapdb.QualificationClientSourceTree &&
		record.GapDBTuple.ModuleVersion == storegapdb.QualificationClientModuleVersion &&
		record.GapDBTuple.Sidecar.Command == "gapdbd" &&
		record.GapDBTuple.Sidecar.SHA256 == storegapdb.QualificationSidecarSHA256 &&
		record.GapDBTuple.Sidecar.Size == storegapdb.QualificationSidecarBytes &&
		record.GapDBTuple.Protocol.WireVersion == storegapdb.AdmittedWireVersion &&
		record.GapDBTuple.Protocol.StorageVersion == storegapdb.AdmittedStorageVersion &&
		record.GapDBTuple.Platform.OS == "linux" && record.GapDBTuple.Platform.Arch == "amd64"
}
