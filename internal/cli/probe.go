package cli

import (
	"errors"
	"io"
	"strings"
)

// ProbeOptionErrors checks option spelling and arity without executing a
// command. Startup refusal gates use it to preserve Click's parser errors.
// Conversion errors, missing required arguments and unknown commands are
// deliberately left to the gate, as in RC7's usage_errors_first tokenizer.
func ProbeOptionErrors(root *Command, argv []string) error {
	ctx := newContext(root, nil, IO{Stdout: io.Discard, Stderr: io.Discard})
	command, rest := root, argv
	for {
		next, sub, err := parseInto(ctx, command, rest)
		if err != nil {
			var usage *UsageError
			if errors.As(err, &usage) && (usage.NoContext || strings.HasPrefix(usage.Msg, "No such option:")) {
				usage.parsing = true
				return usage
			}
			return nil
		}
		if sub == "" || command.Find(sub) == nil {
			return nil
		}
		command = command.Find(sub)
		ctx = newContext(command, ctx, IO{Stdout: io.Discard, Stderr: io.Discard})
		rest = next
	}
}
