package workspace

const (
	QuarantineLeaseExpired        = "lease generation expired"
	QuarantineRevisionInvalidated = "WorkRevision invalidated; workspace permanently quarantined"
)

func IsQuarantined(record Record) bool {
	return record.Status == "quarantined" && record.QuarantineReason != "" && !record.QuarantinedAt.IsZero()
}
