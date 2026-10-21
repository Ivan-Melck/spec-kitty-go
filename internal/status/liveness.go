package status

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// clockTicks is SC_CLK_TCK (100 on every supported Linux).
const clockTicks = 100.0

// IsProcessAlive is process_liveness.is_process_alive (Linux /proc probe).
func IsProcessAlive(pid int) bool {
	if pid < 0 {
		return false
	}
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return err == nil
}

// IsClaimingProcessAlive is process_liveness.is_claiming_process_alive: a
// present baseline must match the live process's creation time.
func IsClaimingProcessAlive(pid int, baseline string) bool {
	if baseline == "" {
		return IsProcessAlive(pid)
	}
	got, ok := CreationTimeBaseline(pid)
	return ok && got == baseline
}

// CreationTimeBaseline is process_liveness.capture_creation_time_baseline:
// str(psutil.Process(pid).create_time()) on Linux ("" , false when the
// process cannot be inspected).
func CreationTimeBaseline(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	s := string(b)
	r := strings.LastIndexByte(s, ')')
	if r < 0 || r+2 > len(s) {
		return "", false
	}
	fields := strings.Fields(s[r+2:])
	if len(fields) < 20 {
		return "", false
	}
	start, err := strconv.ParseFloat(fields[19], 64)
	if err != nil {
		return "", false
	}
	f, err := os.Open("/proc/stat")
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "btime") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return "", false
			}
			bt, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				return "", false
			}
			return jsonx.FormatFloat(start/clockTicks + bt), true
		}
	}
	return "", false
}
