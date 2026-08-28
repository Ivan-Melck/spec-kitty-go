package controlstore

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
