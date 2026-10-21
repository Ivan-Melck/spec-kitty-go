package tracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// subprocessTimeout is SubprocessCommandRunner.timeout_seconds.
var subprocessTimeout = 30 * time.Second

// runCommand mirrors SubprocessCommandRunner.run (check=True,
// capture_output, text): a non-zero exit or timeout is a
// ConnectorRequestError; a missing executable is an uncaught
// FileNotFoundError.
func runCommand(command []string, cwd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), subprocessTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	if cwd != "" {
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			return "", &Crash{Type: "FileNotFoundError", Msg: fmt.Sprintf("[Errno 2] No such file or directory: %s", pyjson.ReprString(cwd))}
		}
		cmd.Dir = cwd
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	joined := strings.Join(command, " ")
	if ctx.Err() == context.DeadlineExceeded {
		return "", requestError("Command timed out: " + joined)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr := strings.TrimSpace(errb.String())
			if stderr == "" {
				reprs := make([]any, len(command))
				for i, c := range command {
					reprs[i] = c
				}
				code := ee.ExitCode()
				if ws, ok := ee.Sys().(interface{ Signal() syscall.Signal }); ok && code < 0 {
					sig := ws.Signal()
					stderr = fmt.Sprintf("Command '%s' died with <Signals.%s: %d>.", pyjson.Repr(reprs), signalName(sig), int(sig))
				} else {
					stderr = fmt.Sprintf("Command '%s' returned non-zero exit status %d.", pyjson.Repr(reprs), code)
				}
			}
			return "", requestError(fmt.Sprintf("Command failed: %s :: %s", joined, stderr))
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return "", &Crash{Type: "FileNotFoundError", Msg: fmt.Sprintf("[Errno 2] No such file or directory: %s", pyjson.ReprString(command[0]))}
		}
		if errors.Is(err, os.ErrPermission) {
			return "", &Crash{Type: "PermissionError", Msg: fmt.Sprintf("[Errno 13] Permission denied: %s", pyjson.ReprString(command[0]))}
		}
		return "", &Crash{Type: "OSError", Msg: err.Error()}
	}
	return out.String(), nil
}

func signalName(s syscall.Signal) string {
	names := map[int]string{1: "SIGHUP", 2: "SIGINT", 3: "SIGQUIT", 4: "SIGILL", 5: "SIGTRAP", 6: "SIGABRT", 7: "SIGBUS", 8: "SIGFPE",
		9: "SIGKILL", 10: "SIGUSR1", 11: "SIGSEGV", 12: "SIGUSR2", 13: "SIGPIPE", 14: "SIGALRM", 15: "SIGTERM"}
	if n, ok := names[int(s)]; ok {
		return n
	}
	return fmt.Sprintf("SIG%d", int(s))
}

// --- program gateway (specify_cli.tracker.gateway) ---

var (
	gatewayAllowed = map[string]bool{"list": true, "show": true, "create": true, "update": true, "dep add": true, "comments add": true}
	gatewayGlobal  = map[string]bool{"--actor": true, "--db": true, "-C": true, "--directory": true, "--dolt-auto-commit": true}
	gatewayFlags   = map[string]map[string]bool{
		"create": {"--assignee": true, "-a": true},
		"update": {"--assignee": true, "-a": true, "--status": true, "-s": true, "--claim": true},
	}
)

func canonicalizeArgv(command []string) []string {
	var out []string
	for _, a := range command {
		flag, value, sep := strings.Cut(a, "=")
		if sep && (strings.HasPrefix(flag, "--") || (strings.HasPrefix(flag, "-") && len([]rune(flag)) == 2)) {
			out = append(out, flag, value)
			continue
		}
		out = append(out, a)
	}
	return out
}

func subcommandPath(argv []string) []string {
	tokens := argv[1:]
	i := 0
	stop := false
	for i < len(tokens) {
		a := tokens[i]
		if !stop && a == "--" {
			stop = true
			i++
			continue
		}
		if !stop && strings.HasPrefix(a, "-") {
			if gatewayGlobal[a] {
				i += 2
			} else {
				i++
			}
			continue
		}
		break
	}
	if i >= len(tokens) {
		return nil
	}
	path := []string{tokens[i]}
	if (tokens[i] == "dep" || tokens[i] == "comments") && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
		path = append(path, tokens[i+1])
	}
	return path
}

func forbiddenReason(command []string) string {
	argv := canonicalizeArgv(command)
	path := subcommandPath(argv)
	key := strings.Join(path, " ")
	if !gatewayAllowed[key] {
		joined := key
		if len(path) == 0 {
			joined = "<none>"
		}
		return fmt.Sprintf("subcommand %s is not on the tracker gateway allow-list", pyjson.ReprString(joined))
	}
	if flags := gatewayFlags[key]; flags != nil {
		for _, a := range argv {
			if flags[a] {
				return fmt.Sprintf("forbidden lifecycle flag %s on allow-listed subcommand %s", pyjson.ReprString(a), pyjson.ReprString(key))
			}
		}
	}
	return ""
}

// gatewayRun mirrors GatewayCommandRunner.run for a fresh, repo-broad token
// whose scope always covers the connector's own execution context.
func gatewayRun(command []string, cwd string) (string, error) {
	if reason := forbiddenReason(command); reason != "" {
		reprs := make([]any, len(command))
		for i, c := range command {
			reprs[i] = c
		}
		return "", errorf("tracker program gateway refused command %s: %s (assign/close/approve/release and self-claim/review/publication bypass are host-owned, never tracker-sync-owned)", pyjson.Repr(reprs), reason)
	}
	return runCommand(command, cwd)
}

// getUser mirrors getpass.getuser().
func getUser() string {
	for _, k := range []string{"LOGNAME", "USER", "LNAME", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func (s *Service) buildConnector(rt *runtimeState) (Connector, error) {
	command := credStr(rt.creds, "command")
	cwd := credStr(rt.creds, "cwd")
	switch NormalizeProvider(rt.cfg.Provider) {
	case "beads":
		if command == "" {
			command = "bd"
		}
		actor := credStr(rt.creds, "actor")
		if actor == "" {
			actor = getUser()
		}
		if strings.TrimSpace(actor) == "" {
			return nil, &ValueError{"TrackerGatewayToken.actor must not be empty"}
		}
		if strings.TrimSpace(kittify.DeriveProjectSlug(s.Root)) == "" {
			return nil, &ValueError{"TrackerGatewayToken.repository must not be empty"}
		}
		return &BeadsConnector{Workspace: rt.cfg.Workspace, Command: command, Cwd: cwd, run: gatewayRun}, nil
	case "fp":
		if command == "" {
			command = "fp"
		}
		return &FPConnector{Workspace: rt.cfg.Workspace, Command: command, Cwd: cwd, run: runCommand}, nil
	}
	return nil, errorf("Unsupported provider '%s'. Supported: beads, fp", rt.cfg.Provider)
}

// parseJSONOutput mirrors BeadsConnector._parse_json.
func parseJSONOutput(output string) (any, error) {
	text := strings.TrimSpace(output)
	if text == "" {
		return &jsonx.Object{}, nil
	}
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, payloadError("Invalid JSON from bd: " + err.Error())
	}
	return v, nil
}
