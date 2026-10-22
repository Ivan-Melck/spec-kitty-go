package migrate

// pydLoc extends a pydantic location tuple. str(ValidationError) itself is
// rendered by the shared internal/pydantic formatter.
func pydLoc(loc []any, more ...any) []any {
	return append(append([]any{}, loc...), more...)
}
