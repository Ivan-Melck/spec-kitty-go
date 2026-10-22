package cli

// Invoke parses argv against the leaf command cmd and runs it in-process,
// returning its error (nil, *ExitError, *UsageError, ...). It is the Go
// equivalent of upstream calling another typer command body directly with
// explicit keyword arguments (e.g. `charter activate --resynthesize` running
// `generate` and `synthesize`). No BeforeRun hook runs.
func Invoke(cmd *Command, argv []string, io_ IO) error {
	ctx := newContext(cmd, nil, io_)
	if _, _, err := parseInto(ctx, cmd, argv); err != nil {
		return err
	}
	if err := finalize(ctx, cmd); err != nil {
		return err
	}
	if cmd.Run == nil {
		return nil
	}
	return cmd.Run(ctx)
}
