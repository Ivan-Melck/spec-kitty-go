package controlstore

import (
	"context"
	"time"
)

// Settings is the observable local-control-store contract. One store is owned
// by one machine/filesystem authority; remote processes must use a service
// boundary rather than opening the database. WAL coordinates concurrent local
// connections, while ForeignKeys keeps aggregate authority fail-closed.
//
// Domain persistence ports intentionally remain consumer-owned. Composition
// adapters assert those interfaces where they are implemented instead of
// making this provider-contract package depend on mission or artifact law.
type Settings struct {
	JournalMode string
	ForeignKeys bool
	BusyTimeout int
}

type OutboxDeliveryStatus string

const (
	OutboxPending   OutboxDeliveryStatus = "pending"
	OutboxClaimed   OutboxDeliveryStatus = "claimed"
	OutboxRetryable OutboxDeliveryStatus = "retryable"
	OutboxDelivered OutboxDeliveryStatus = "delivered"
)

// OutboxEnvelope separates immutable event identity from mutable delivery
// evidence. PayloadDigest is evidence, never an artifact body or transcript.
type OutboxEnvelope struct {
	Sequence       int64
	EventID        string
	EventKind      string
	PayloadDigest  string
	CreatedAt      time.Time
	DeliveryStatus OutboxDeliveryStatus
	AttemptCount   uint64
	AvailableAt    time.Time
	ClaimID        string
	ClaimedAt      time.Time
	LastAttemptAt  time.Time
	DeliveredAt    time.Time
}

// OutboxStore is the narrow local delivery port. Claim, Retry, and Deliver are
// compare-and-set operations over one durable envelope.
type OutboxStore interface {
	ClaimOutbox(context.Context, string, time.Time) (OutboxEnvelope, bool, error)
	RetryOutbox(context.Context, int64, string, time.Time, time.Time) (OutboxEnvelope, error)
	DeliverOutbox(context.Context, int64, string, time.Time) (OutboxEnvelope, error)
}
