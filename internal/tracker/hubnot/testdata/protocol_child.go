// A disposable adversarial subprocess used only by transport tests.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	switch os.Args[1] {
	case "stdout":
		for {
			fmt.Print(strings.Repeat("x", 4096))
		}
	case "stderr":
		for {
			fmt.Fprint(os.Stderr, strings.Repeat("x", 4096))
		}
	case "sleep":
		time.Sleep(time.Hour)
	case "descendant":
		cmd := exec.Command(os.Args[0], "sleep")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		_ = os.WriteFile("descendant.pid", []byte(fmt.Sprint(cmd.Process.Pid)), 0600)
		time.Sleep(time.Hour)
	case "environment":
		for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "HN_SECRET", "DOLT_PASSWORD", "AWS_SECRET_ACCESS_KEY"} {
			if os.Getenv(k) != "" {
				os.Exit(2)
			}
		}
		fmt.Print("safe")
	case "identity", "issue":
		f, err := os.OpenFile("calls", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			panic(err)
		}
		_, _ = f.WriteString("call\n")
		_ = f.Close()
		args, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile("argv.json", args, 0600)
		input, _ := io.ReadAll(os.Stdin)
		_ = os.WriteFile("input.json", input, 0600)
		var scenario struct {
			Output, Stderr []byte
			Exit           int
		}
		raw, err := os.ReadFile("scenario.json")
		if err != nil {
			panic(err)
		}
		if err := json.Unmarshal(raw, &scenario); err != nil {
			panic(err)
		}
		_, _ = os.Stdout.Write(scenario.Output)
		_, _ = os.Stderr.Write(scenario.Stderr)
		os.Exit(scenario.Exit)
	}
}
