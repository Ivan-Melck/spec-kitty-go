package architecture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"time"
)

const (
	goListOutputLimit = 32 << 20
	goListTimeout     = 30 * time.Second
)

// ImportKind distinguishes production dependency classes. Test-only imports
// remain visible in the graph but are not evaluated as production edges.
type ImportKind string

const (
	ImportModuleLocal     ImportKind = "module-local"
	ImportExternal        ImportKind = "external"
	ImportStandardLibrary ImportKind = "standard-library"
	ImportTestOnly        ImportKind = "test-only"
)

// Package is one production package in the main module.
type Package struct {
	ImportPath string
	Dir        string
}

// ImportEdge is one direct import from a main-module production package.
type ImportEdge struct {
	SourcePackage string
	TargetPackage string
	Kind          ImportKind
}

// Graph is the production import graph returned by Go tooling.
type Graph struct {
	ModulePath string
	Packages   []Package
	Edges      []ImportEdge
}

// LoadErrorKind makes package-load failures inspectable without parsing prose.
type LoadErrorKind string

const (
	LoadCommandFailed LoadErrorKind = "command_failed"
	LoadOutputLimit   LoadErrorKind = "output_limit"
	LoadInvalidOutput LoadErrorKind = "invalid_output"
	LoadEmptyGraph    LoadErrorKind = "empty_graph"
)

// LoadError is a bounded, typed failure from production graph loading.
type LoadError struct {
	Kind    LoadErrorKind
	Command string
	Stderr  string
	Cause   error
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("architecture graph load kind=%s command=%q stderr=%q: %v", e.Kind, e.Command, e.Stderr, e.Cause)
}

type commandResult struct {
	stdout   []byte
	stderr   []byte
	overflow bool
}

type graphCommandRunner func(context.Context, string, ...string) (commandResult, error)

// LoadGraph asks the Go tool for structured module and package metadata, then
// returns sorted direct production and test-only edges.
func LoadGraph(ctx context.Context, root string) (Graph, error) {
	return loadGraph(ctx, root, runGraphCommand)
}

func loadGraph(ctx context.Context, root string, run graphCommandRunner) (Graph, error) {
	ctx, cancel := context.WithTimeout(ctx, goListTimeout)
	defer cancel()

	moduleResult, err := run(ctx, root, "list", "-m", "-json")
	if err != nil {
		return Graph{}, commandLoadError("go list -m -json", moduleResult, err)
	}
	if moduleResult.overflow {
		return Graph{}, commandLoadError("go list -m -json", moduleResult, errors.New("bounded output exceeded"))
	}
	var module struct{ Path string }
	if err := json.Unmarshal(moduleResult.stdout, &module); err != nil || module.Path == "" {
		if err == nil {
			err = errors.New("module path is empty")
		}
		return Graph{}, &LoadError{Kind: LoadInvalidOutput, Command: "go list -m -json", Cause: err}
	}

	packageResult, err := run(ctx, root, "list", "-deps", "-json", "./...")
	if err != nil {
		return Graph{}, commandLoadError("go list -deps -json ./...", packageResult, err)
	}
	if packageResult.overflow {
		return Graph{}, commandLoadError("go list -deps -json ./...", packageResult, errors.New("bounded output exceeded"))
	}

	type moduleMetadata struct {
		Path string
		Main bool
	}
	type listedPackage struct {
		Dir          string
		ImportPath   string
		Imports      []string
		TestImports  []string
		XTestImports []string
		Standard     bool
		Module       *moduleMetadata
	}
	decoder := json.NewDecoder(bytes.NewReader(packageResult.stdout))
	listed := make(map[string]listedPackage)
	for {
		var item listedPackage
		if err := decoder.Decode(&item); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return Graph{}, &LoadError{Kind: LoadInvalidOutput, Command: "go list -deps -json ./...", Cause: err}
		}
		listed[item.ImportPath] = item
	}

	graph := Graph{ModulePath: module.Path}
	for _, item := range listed {
		if item.Module != nil && item.Module.Path == module.Path {
			graph.Packages = append(graph.Packages, Package{ImportPath: item.ImportPath, Dir: item.Dir})
		}
	}
	if len(graph.Packages) == 0 {
		return Graph{}, &LoadError{Kind: LoadEmptyGraph, Command: "go list -deps -json ./...", Cause: errors.New("main module has no production packages")}
	}
	sort.Slice(graph.Packages, func(i, j int) bool { return graph.Packages[i].ImportPath < graph.Packages[j].ImportPath })

	edges := make(map[ImportEdge]struct{})
	for _, source := range graph.Packages {
		item := listed[source.ImportPath]
		for _, target := range item.Imports {
			metadata := listed[target]
			targetModule := ""
			if metadata.Module != nil {
				targetModule = metadata.Module.Path
			}
			edges[ImportEdge{SourcePackage: source.ImportPath, TargetPackage: target, Kind: importKind(module.Path, metadata.Standard, targetModule)}] = struct{}{}
		}
		for _, target := range append(append([]string(nil), item.TestImports...), item.XTestImports...) {
			edges[ImportEdge{SourcePackage: source.ImportPath, TargetPackage: target, Kind: ImportTestOnly}] = struct{}{}
		}
	}
	for edge := range edges {
		graph.Edges = append(graph.Edges, edge)
	}
	sort.Slice(graph.Edges, func(i, j int) bool {
		left, right := graph.Edges[i], graph.Edges[j]
		if left.SourcePackage != right.SourcePackage {
			return left.SourcePackage < right.SourcePackage
		}
		if left.TargetPackage != right.TargetPackage {
			return left.TargetPackage < right.TargetPackage
		}
		return left.Kind < right.Kind
	})
	return graph, nil
}

func importKind(modulePath string, standard bool, targetModule string) ImportKind {
	if standard {
		return ImportStandardLibrary
	}
	if targetModule == modulePath {
		return ImportModuleLocal
	}
	return ImportExternal
}

func commandLoadError(command string, result commandResult, cause error) *LoadError {
	kind := LoadCommandFailed
	if result.overflow {
		kind = LoadOutputLimit
	}
	return &LoadError{Kind: kind, Command: command, Stderr: string(result.stderr), Cause: cause}
}

func runGraphCommand(ctx context.Context, root string, arguments ...string) (commandResult, error) {
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = root
	command.Env = os.Environ()
	stdout := &boundedBuffer{limit: goListOutputLimit}
	stderr := &boundedBuffer{limit: goListOutputLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	return commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), overflow: stdout.overflow || stderr.overflow}, err
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining < len(value) {
		b.overflow = true
		if remaining > 0 {
			_, _ = b.buffer.Write(value[:remaining])
		}
		return written, nil
	}
	_, _ = b.buffer.Write(value)
	return written, nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }
