package retainedkernel

// All new state/wrappers are OWNED/UNQUALIFIED. Site is a fixed private literal;
// no fd, callback, admission authority or retry escapes.
type retainedCloseError struct {
	site string
	cause error
}

// Preserve even the sole-Close raw syscall diagnostic and classification.
func (e *retainedCloseError) Error() string { return e.cause.Error() }
func (e *retainedCloseError) Unwrap() error { return e.cause }

type retainedCustodyError struct {
	primary error
	custody error
}
func (e *retainedCustodyError) Error() string { return e.primary.Error() }
func (e *retainedCustodyError) Unwrap() []error { return []error{e.primary, e.custody} }

func joinRetainedCustody(primary, custody error) error {
	if custody == nil { return primary }
	if primary == nil { return custody }
	return &retainedCustodyError{primary: primary, custody: custody}
}
func retainedCloseResult(primary error, site string, closeErr error) error {
	if closeErr == nil { return primary }
	return joinRetainedCustody(primary, &retainedCloseError{site: site, cause: closeErr})
}
