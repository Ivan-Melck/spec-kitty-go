package helpercmd

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/spec-kitty/go-kitty3/internal/architecture"
)

// runArchitectureCheck is the live composition-root entry point for the
// permanent dependency and reachability guard.
func runArchitectureCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("architecture-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var root string
	flags.StringVar(&root, "root", ".", "Go module root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "architecture-check accepts no positional arguments")
		return 2
	}
	graph, err := architecture.LoadGraph(ctx, root)
	if err != nil {
		fmt.Fprintf(stderr, "architecture check: %v\n", err)
		return 1
	}
	policy, err := architecture.DefaultPolicy(graph.ModulePath)
	if err != nil {
		fmt.Fprintf(stderr, "architecture check: %v\n", err)
		return 1
	}
	if err := policy.Check(graph); err != nil {
		fmt.Fprintf(stderr, "architecture check: %v\n", err)
		return 1
	}
	if err := architecture.CheckReachability(root, graph.ModulePath, architecture.DefaultReachabilityTargets(graph.ModulePath)); err != nil {
		fmt.Fprintf(stderr, "architecture check: %v\n", err)
		return 1
	}
	if err := architecture.CheckDeferredRuntimeSurfaces(root, graph.ModulePath); err != nil {
		fmt.Fprintf(stderr, "architecture check: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "architecture check passed: module=%s packages=%d edges=%d\n", graph.ModulePath, len(graph.Packages), len(graph.Edges))
	return 0
}
