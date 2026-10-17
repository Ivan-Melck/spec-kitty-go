//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// These are kernel/component controls, NOT the authenticated TEST executable,
// an admitted provider, a genuine Host, or qualification evidence. Nothing in
// this file authorizes execution during the whole-area code-first phase.
type expiryDescriptorControlOutcome struct {
	Attempted bool `json:"attempted"`
	Error *string `json:"error"`
}
type expiryDescriptorControlLedger struct {
	mu sync.Mutex
	outcomes map[string]expiryDescriptorControlOutcome
}
func expiryDescriptorControlRecord(t *testing.T, l *expiryDescriptorControlLedger, name string, err error) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.outcomes == nil { l.outcomes = make(map[string]expiryDescriptorControlOutcome) }
	if _, exists := l.outcomes[name]; exists { t.Errorf("FIRST outcome overwritten: %s", name); return }
	o := expiryDescriptorControlOutcome{Attempted: true}
	if err != nil { s := err.Error(); o.Error = &s }
	l.outcomes[name] = o
}
func expiryDescriptorControlCleanupResult(t *testing.T, l *expiryDescriptorControlLedger, name string, err error) {
	t.Helper()
	expiryDescriptorControlRecord(t, l, name, err)
	if err != nil { t.Errorf("FIRST owned cleanup %s: %v", name, err) }
}
func expiryDescriptorControlLedgerNew(t *testing.T) *expiryDescriptorControlLedger {
	l := new(expiryDescriptorControlLedger)
	// Registered FIRST so the log runs LAST, including all actual cleanup results.
	t.Cleanup(func() { l.mu.Lock(); defer l.mu.Unlock(); b, err := json.Marshal(l.outcomes); if err != nil { t.Error(err); return }; t.Logf("FIRST kernel/component outcomes: %s", b) })
	return l
}
func expiryDescriptorControlOwnFile(t *testing.T, l *expiryDescriptorControlLedger, name string, f *os.File) func() {
	t.Helper()
	var once sync.Once
	close := func() { once.Do(func() { expiryDescriptorControlCleanupResult(t, l, name+".close", f.Close()) }) }
	t.Cleanup(close)
	return close
}
func expiryDescriptorControlRoot(t *testing.T, l *expiryDescriptorControlLedger) string {
	t.Helper()
	root, err := os.MkdirTemp("", "host-descriptor-control-residual-")
	expiryDescriptorControlRecord(t, l, "root.mkdir", err)
	if err != nil { t.Fatal(err) }
	t.Logf("PRESERVED fixture-only residual; manual retirement only: %s", root)
	return root
}
func expiryDescriptorControlStartTicks(t *testing.T, ledger *expiryDescriptorControlLedger, name string, pid int) uint64 {
	t.Helper()
	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	expiryDescriptorControlRecord(t, ledger, name+".stat.open", err); if err != nil { t.Fatal(err) }
	close := expiryDescriptorControlOwnFile(t, ledger, name+".stat", f)
	b, readErr := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	expiryDescriptorControlRecord(t, ledger, name+".stat.read", readErr); close()
	if readErr != nil || len(b) > 64<<10 { t.Fatal("bounded stat read", readErr) }
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 || i+2 >= len(b) { t.Fatal("malformed stat") }
	fields := strings.Fields(string(b[i+2:]))
	if len(fields) <= 19 { t.Fatal("short stat") }
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 { t.Fatal("invalid start ticks", err) }
	return ticks
}
func expiryDescriptorControlProcessImage(t *testing.T, ledger *expiryDescriptorControlLedger, pid int) (fileIdentity, string) {
	t.Helper()
	f, err := os.Open(fmt.Sprintf("/proc/%d/exe", pid))
	expiryDescriptorControlRecord(t, ledger, "self.proc-image.open", err); if err != nil { t.Fatal(err) }
	close := expiryDescriptorControlOwnFile(t, ledger, "self.proc-image", f)
	id, err := identityOf(f)
	if err != nil || id.size <= 0 || id.size > 64<<20 { t.Fatal("bounded image identity", err) }
	digest, err := digestFile(f, id.size)
	expiryDescriptorControlRecord(t, ledger, "self.proc-image.read-hash", err); close()
	if err != nil { t.Fatal(err) }
	return id, digest
}
func expiryDescriptorControlImage(t *testing.T, l *expiryDescriptorControlLedger, root string) *VerifiedExecutable {
	t.Helper()
	path, err := os.Executable()
	expiryDescriptorControlRecord(t, l, "self.executable-path", err)
	if err != nil { t.Fatal(err) }
	source, err := os.Open(path)
	expiryDescriptorControlRecord(t, l, "self.open", err)
	if err != nil { t.Fatal(err) }
	closeSource := expiryDescriptorControlOwnFile(t, l, "self", source)
	copy, err := os.OpenFile(filepath.Join(root, "own-test-ELF"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0700)
	expiryDescriptorControlRecord(t, l, "copy.open", err)
	if err != nil { t.Fatal(err) }
	closeCopy := expiryDescriptorControlOwnFile(t, l, "copy", copy)
	h := sha256.New()
	n, readErr := io.Copy(io.MultiWriter(copy, h), source)
	expiryDescriptorControlRecord(t, l, "self.read-copy", readErr)
	syncErr := copy.Sync()
	expiryDescriptorControlRecord(t, l, "copy.fsync", syncErr)
	closeSource(); closeCopy()
	if readErr != nil || syncErr != nil { t.Fatal(errors.Join(readErr, syncErr)) }
	r := new(expiryCleanupReport)
	v, err := verifyExecutableImage(copy.Name(), hex.EncodeToString(h.Sum(nil)), n, ExecutableProvenance{ContractRevision: expiryQualificationContract}, r)
	expiryDescriptorControlRecord(t, l, "image.verify", err)
	if err != nil { t.Fatal(err) }
	v.expiry.report = r
	v.expiry.identity = ExpiryTechnicalIdentity{InputRecordSHA256: "kernel-own-test-ELF-NOT-R", ExecutableSHA256: v.executed.SHA256, ExecutableBytes: v.executed.Size}
	// Register gate cleanup first: LIFO closes image/source before requesting
	// the gate's all-owned-descriptor completeness snapshot.
	t.Cleanup(func() { v.expiryCommandStarted(); expiryDescriptorControlCleanupResult(t, l, "gate.close", (&ExpiryQualification{state: v.expiry.gate}).Close()) })
	// Close source/image separately at the actual FIRST attempts, then clear
	// pointers so no later aggregate nil can repair a failed FIRST result.
	t.Cleanup(func() {
		if v.execution != nil { e := v.execution.Close(); r.imageClosed(e); expiryDescriptorControlCleanupResult(t, l, "verified.image.close", e); v.execution = nil }
		if v.source != nil { e := v.source.Close(); r.sourceClosed(e); expiryDescriptorControlCleanupResult(t, l, "verified.source.close", e); v.source = nil }
	})
	if err := v.makeExpiryPair(context.Background()); err != nil { t.Fatal(err) }
	return v
}

// Explicit, curated opt-in only. fixedCommand's environment is retained and
// exactly one test-only key appended; os.Environ and SPEC_* inheritance are absent.
const expiryDescriptorControlChildKey = "EXPIRY_DESCRIPTOR_CONTROL_CHILD"
type expiryDescriptorControlChildReceipt struct {
	Mode string `json:"mode"`
	GateEnvironment string `json:"gate_environment"`
	Close3 *expiryDescriptorControlOutcome `json:"close3"`
	Close4 *expiryDescriptorControlOutcome `json:"close4"`
	Reuse4 *expiryDescriptorControlOutcome `json:"reuse4"`
	DecoyClose *expiryDescriptorControlOutcome `json:"decoy_close"`
	PositiveProcOpen *expiryDescriptorControlOutcome `json:"positive_proc_open"`
	PositiveProcStat *expiryDescriptorControlOutcome `json:"positive_proc_stat"`
	PositiveProcClose *expiryDescriptorControlOutcome `json:"positive_proc_close"`
}
func expiryDescriptorControlOutcomeOf(err error) *expiryDescriptorControlOutcome {
	o := &expiryDescriptorControlOutcome{Attempted: true}
	if err != nil { s := err.Error(); o.Error = &s }
	return o
}
func expiryDescriptorControlChildCleanup(t *testing.T, name string, err error) {
	t.Helper()
	b, marshalErr := json.Marshal(map[string]any{"cleanup": name, "first": expiryDescriptorControlOutcomeOf(err)})
	if marshalErr != nil { t.Error(marshalErr) } else if _, writeErr := fmt.Fprintln(os.Stdout, string(b)); writeErr != nil { t.Error(writeErr) }
	if err != nil { t.Errorf("FIRST child-owned cleanup %s: %v", name, err) }
}
func TestExpiryDescriptorControlChild(t *testing.T) {
	mode := os.Getenv(expiryDescriptorControlChildKey)
	if mode == "" { return } // ordinary test enumeration starts no subprocess
	r := expiryDescriptorControlChildReceipt{Mode: mode, GateEnvironment: os.Getenv("SPEC_KITTY_EXPIRY_GATE_FD")}
	gate := os.NewFile(3, "owned-inherited-gate")
	image := os.NewFile(4, "owned-inherited-image")
	gateClosed, imageClosed := false, false
	defer func() {
		// Actual child-owned FIRST closes, never parent unknown integer cleanup.
		if !gateClosed { expiryDescriptorControlChildCleanup(t, "inherited-gate", gate.Close()) }
		if !imageClosed { expiryDescriptorControlChildCleanup(t, "inherited-image", image.Close()) }
	}()
	if mode == "positive" {
		domain, domainErr := unix.GetsockoptInt(3, unix.SOL_SOCKET, unix.SO_DOMAIN)
		kind, kindErr := unix.GetsockoptInt(3, unix.SOL_SOCKET, unix.SO_TYPE)
		seals, sealsErr := unix.FcntlInt(image.Fd(), unix.F_GET_SEALS, 0)
		fd4, imageErr := identityOf(image)
		exe, openErr := os.Open("/proc/self/exe")
		var executed fileIdentity
		var statErr, closeErr error
		r.PositiveProcOpen = expiryDescriptorControlOutcomeOf(openErr)
		if openErr == nil { executed, statErr = identityOf(exe); closeErr = exe.Close(); r.PositiveProcStat = expiryDescriptorControlOutcomeOf(statErr); r.PositiveProcClose = expiryDescriptorControlOutcomeOf(closeErr) }
		if domainErr != nil || kindErr != nil || sealsErr != nil || imageErr != nil || openErr != nil || statErr != nil || closeErr != nil || domain != unix.AF_UNIX || kind != unix.SOCK_STREAM || seals&requiredExecutionSeals != requiredExecutionSeals || fd4.device != executed.device || fd4.inode != executed.inode || r.GateEnvironment != "3" {
			t.Errorf("positive inherited AF_UNIX/sealed-image/exe control: domain=%d type=%d seals=%d errors=%v", domain, kind, seals, []error{domainErr, kindErr, sealsErr, imageErr, openErr, statErr, closeErr})
		}
	}
	switch mode {
	case "closed3": r.Close3 = expiryDescriptorControlOutcomeOf(gate.Close()); gateClosed = true
	case "closed4", "held-descriptor-reuse":
		// fd4 is ONLY the explicitly inherited fixture-owned child descriptor.
		r.Close4 = expiryDescriptorControlOutcomeOf(image.Close()); imageClosed = true
		if mode == "held-descriptor-reuse" {
			fd, err := unix.MemfdCreate("fixture-decoy", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
			if err == nil {
				_, err = unix.Write(fd, []byte("not-the-verified-image"))
				if fd != 4 {
					// Never overwrite an integer which may have been reused by the
					// runtime. Only a successful acquisition of exactly 4 owns it.
					r.DecoyClose = expiryDescriptorControlOutcomeOf(unix.Close(fd))
					err = errors.Join(err, errors.New("fixture did not acquire owned reuse of fd4"))
				} else { defer func() { expiryDescriptorControlChildCleanup(t, "reused-owned-fd4", unix.Close(fd)) }() }
			}
			r.Reuse4 = expiryDescriptorControlOutcomeOf(err)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil { t.Error(err); return }
	var release [1]byte
	_, err := io.ReadFull(os.Stdin, release[:])
	// EOF is fixture teardown, NEVER H/R/A or expiry evidence.
	if err != nil && !errors.Is(err, io.EOF) { t.Error(err) }
}

type expiryDescriptorControlStarted struct {
	command *exec.Cmd
	lifecycle *Lifecycle
	startErr error
	receipt expiryDescriptorControlChildReceipt
}
func expiryDescriptorControlStart(t *testing.T, ledger *expiryDescriptorControlLedger, v *VerifiedExecutable, mode string, mutate func(*exec.Cmd)) *expiryDescriptorControlStarted {
	t.Helper()
	cmd, err := v.command([]string{"-test.run=^TestExpiryDescriptorControlChild$"}, []string{"IGNORED=1"})
	expiryDescriptorControlRecord(t, ledger, "command", err)
	if err != nil { t.Fatal(err) }
	cmd.Env = append(cmd.Env, expiryDescriptorControlChildKey+"="+mode)
	inRead, inWrite, err := os.Pipe()
	if err != nil { t.Fatal(err) }
	closeInRead := expiryDescriptorControlOwnFile(t, ledger, "stdin.child-original", inRead)
	closeInWrite := expiryDescriptorControlOwnFile(t, ledger, "stdin.parent", inWrite)
	outRead, outWrite, err := os.Pipe()
	if err != nil { t.Fatal(err) }
	closeOutRead := expiryDescriptorControlOwnFile(t, ledger, "stdout.parent", outRead)
	closeOutWrite := expiryDescriptorControlOwnFile(t, ledger, "stdout.child-original", outWrite)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inRead, outWrite, outWrite
	if mutate != nil { mutate(cmd) }
	result := &expiryDescriptorControlStarted{command: cmd}
	result.startErr = cmd.Start()
	expiryDescriptorControlRecord(t, ledger, "Cmd.Start", result.startErr)
	v.expiryCommandStarted() // BOTH Start outcomes; never demand exit if not started
	closeInRead(); closeOutWrite()
	if result.startErr != nil { closeInWrite(); closeOutRead(); return result }
	t.Cleanup(func() {
		closeInWrite() // owned input EOF, no signal and no unknown-process cleanup
		deadlineErr := outRead.SetReadDeadline(time.Now().Add(5*time.Second))
		expiryDescriptorControlRecord(t, ledger, "child.cleanup-read-deadline", deadlineErr)
		tail, readErr := io.ReadAll(io.LimitReader(outRead, (64<<10)+1))
		expiryDescriptorControlRecord(t, ledger, "child.cleanup-read", readErr)
		t.Logf("actual child cleanup/exit transcript: %s", tail)
		if readErr != nil || len(tail) > 64<<10 { t.Errorf("child cleanup transcript incomplete: bytes=%d error=%v", len(tail), readErr) }
		exitErr := cmd.Wait()
		expiryDescriptorControlRecord(t, ledger, "child.wait-exit", exitErr)
		if exitErr != nil { t.Errorf("FIRST fixture child exit: %v", exitErr) }
		closeOutRead()
	})
	deadlineErr := outRead.SetReadDeadline(time.Now().Add(5*time.Second))
	expiryDescriptorControlRecord(t, ledger, "stdout.deadline", deadlineErr)
	readErr := json.NewDecoder(outRead).Decode(&result.receipt)
	expiryDescriptorControlRecord(t, ledger, "child.ready-read", readErr)
	if readErr != nil { t.Fatal(readErr) }
	if result.receipt.Mode != mode { t.Fatal("wrong curated child receipt", result.receipt) }
	receiptBytes, marshalErr := json.Marshal(result.receipt)
	if marshalErr != nil { t.Fatal(marshalErr) }; t.Logf("actual FIRST child receipt: %s", receiptBytes)
	for name, outcome := range map[string]*expiryDescriptorControlOutcome{"close3": result.receipt.Close3, "close4": result.receipt.Close4, "reuse4": result.receipt.Reuse4, "decoy-close": result.receipt.DecoyClose, "positive-proc-open": result.receipt.PositiveProcOpen, "positive-proc-stat": result.receipt.PositiveProcStat, "positive-proc-close": result.receipt.PositiveProcClose} {
		if outcome != nil && outcome.Error != nil { t.Errorf("child FIRST %s: %s", name, *outcome.Error) }
	}
	pid := cmd.Process.Pid
	ticks := expiryDescriptorControlStartTicks(t, ledger, "child", pid)
	p := ProcessIdentity{PID: pid, StartTicks: ticks, Device: v.executed.Device, Inode: v.executed.Inode, Size: v.executed.Size, SHA256: v.executed.SHA256}
	l := &Lifecycle{command: cmd, process: p, expiry: lifecycleExpiryState{gate: v.expiry.gate, report: v.expiry.report}, provenance: SidecarProvenance{Executable: ExecutableProvenance{ContractRevision: expiryQualificationContract}}}
	v.expiry.gate.lifecycle.Store(l)
	result.lifecycle = l
	return result
}

func expiryDescriptorControlPositive(t *testing.T) {
	t.Helper()
	ledger := expiryDescriptorControlLedgerNew(t)
	v := expiryDescriptorControlImage(t, ledger, expiryDescriptorControlRoot(t, ledger))
	started := expiryDescriptorControlStart(t, ledger, v, "positive", nil)
	if started.startErr != nil { t.Fatal(started.startErr) }
	if started.receipt.GateEnvironment != "3" { t.Fatal("fixed environment not inherited") }
	if err := started.lifecycle.checkExpiryStartedProcess(v.executed); err != nil { t.Fatal("positive /proc/PID/exe and start bracket", err) }
	if err := started.lifecycle.expiryStarted(v); err != nil { t.Fatal("positive child fd3 socket/fd4 sealed own-ELF", err) }
	if v.expiry.gate.phase != expiryStarted { t.Fatal("positive startup not recorded") }
}

func TestExpiryDescriptorControlOwnELFThenIndependentMutants(t *testing.T) {
	// This positive MUST complete before any mutant is attempted in a future run.
	if !t.Run("positive-first", expiryDescriptorControlPositive) { return }
	for _, mode := range []string{"reversed-ExtraFiles", "exec-fd3", "wrong4", "closed4", "unsealed4", "raw-source", "held-descriptor-reuse", "process-start-substitution", "process-image-substitution", "closed-held-execution", "wrong-held-identity", "closed3", "wrong3-regular", "wrong3-TCP", "wrong3-Unix"} {
		t.Run(mode, func(t *testing.T) {
			ledger := expiryDescriptorControlLedgerNew(t)
			v := expiryDescriptorControlImage(t, ledger, expiryDescriptorControlRoot(t, ledger))
			if mode == "closed-held-execution" {
				err := v.execution.Close(); v.expiry.report.imageClosed(err); expiryDescriptorControlCleanupResult(t, ledger, "verified.image.close", err)
				if _, err := v.command(nil, nil); err == nil { t.Error("closed held execution accepted") }
				v.execution = nil
				return
			}
			if mode == "wrong-held-identity" { v.executeIdentity.inode++; if _, err := v.command(nil, nil); err == nil { t.Error("wrong held identity accepted") }; return }
			var substitute *os.File
			if mode == "wrong3-regular" { substitute = v.source }
			if mode == "wrong3-TCP" {
				fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, unix.IPPROTO_TCP)
				if err != nil { t.Fatal(err) }
				substitute = os.NewFile(uintptr(fd), "wrong-AF_INET-TCP-gate")
				expiryDescriptorControlOwnFile(t, ledger, "TCP.child-substitute", substitute)
			}
			if mode == "wrong3-Unix" {
				// Correct domain/type is insufficient: fd3 must be the ORIGINAL
				// acquired gate, not another otherwise-valid Unix stream socket.
				pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
				if err != nil { t.Fatal(err) }
				substitute = os.NewFile(uintptr(pair[0]), "wrong-original-Unix-gate")
				peer := os.NewFile(uintptr(pair[1]), "wrong-original-Unix-peer")
				expiryDescriptorControlOwnFile(t, ledger, "Unix.child-substitute", substitute)
				expiryDescriptorControlOwnFile(t, ledger, "Unix.peer-substitute", peer)
				original, originalErr := identityOf(v.expiry.child)
				other, otherErr := identityOf(substitute)
				if originalErr != nil || otherErr != nil || original.device == other.device && original.inode == other.inode {
					t.Fatal("Unix substitute lacks an independent acquired identity", originalErr, otherErr)
				}
			}
			if mode == "wrong4" || mode == "unsealed4" {
				fd, err := unix.MemfdCreate("own-ELF-unsealed-control", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
				if err != nil { t.Fatal(err) }
				substitute = os.NewFile(uintptr(fd), "own-ELF-unsealed")
				expiryDescriptorControlOwnFile(t, ledger, "substitute", substitute)
				_, seekErr := v.source.Seek(0, io.SeekStart)
				expiryDescriptorControlRecord(t, ledger, "substitute.source-seek", seekErr)
				_, copyErr := io.Copy(substitute, v.source)
				expiryDescriptorControlRecord(t, ledger, "substitute.source-read", copyErr)
				if seekErr != nil || copyErr != nil { t.Fatal(errors.Join(seekErr, copyErr)) }
				if mode == "wrong4" { if _, err := unix.FcntlInt(substitute.Fd(), unix.F_ADD_SEALS, requiredExecutionSeals); err != nil { t.Fatal(err) } }
			}
			started := expiryDescriptorControlStart(t, ledger, v, mode, func(cmd *exec.Cmd) {
				switch mode {
				case "reversed-ExtraFiles": cmd.ExtraFiles[0], cmd.ExtraFiles[1] = cmd.ExtraFiles[1], cmd.ExtraFiles[0]
				case "exec-fd3": cmd.Path = "/proc/self/fd/3"; cmd.Args[0] = cmd.Path
				case "wrong4", "unsealed4": cmd.ExtraFiles[1] = substitute
				case "wrong3-regular", "wrong3-TCP", "wrong3-Unix": cmd.ExtraFiles[0] = substitute
				case "raw-source": cmd.ExtraFiles[1] = v.source
				}
			})
			if started.startErr != nil {
				if mode != "reversed-ExtraFiles" && mode != "exec-fd3" { t.Errorf("earlier FIRST Start refusal, not startup-bracket credit: %v", started.startErr) }
				return
			}
			if mode == "reversed-ExtraFiles" || mode == "exec-fd3" { t.Error("socket unexpectedly executable"); return }
			if mode == "process-start-substitution" { started.lifecycle.process.StartTicks++ }
			if mode == "process-image-substitution" { started.lifecycle.process.Inode++ }
			err := started.lifecycle.expiryStarted(v)
			expiryDescriptorControlRecord(t, ledger, "expiryStarted", err)
			if err == nil { t.Error("mutant accepted by actual startup verification") }
		})
	}
}

func TestExpiryDescriptorControlEachIncompleteSealSet(t *testing.T) {
	// Each proper subset is independent: no combined clause or silent cap.
	for mask := 0; mask < 16; mask++ {
		if mask == 15 { continue }
		t.Run(fmt.Sprintf("required-seal-subset-%02d", mask), func(t *testing.T) {
			ledger := expiryDescriptorControlLedgerNew(t)
			fd, err := unix.MemfdCreate("incomplete-seals-control", unix.MFD_ALLOW_SEALING|unix.MFD_CLOEXEC)
			if err != nil { t.Fatal(err) }
			f := os.NewFile(uintptr(fd), "incomplete-seals")
			expiryDescriptorControlOwnFile(t, ledger, "memfd", f)
			b := []byte("kernel-only-seal-subset")
			_, err = f.Write(b); expiryDescriptorControlRecord(t, ledger, "memfd.write", err); if err != nil { t.Fatal(err) }
			bits := []int{unix.F_SEAL_WRITE, unix.F_SEAL_GROW, unix.F_SEAL_SHRINK, unix.F_SEAL_SEAL}
			seals := 0
			for i, bit := range bits { if mask&(1<<i) != 0 { seals |= bit } }
			_, err = unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, seals)
			expiryDescriptorControlRecord(t, ledger, "memfd.seal", err); if err != nil { t.Fatal(err) }
			id, err := identityOf(f); if err != nil { t.Fatal(err) }
			h := sha256.Sum256(b)
			expected := ExecutedImageProvenance{Device: id.device, Inode: id.inode, Size: id.size, Mode: id.mode, SHA256: hex.EncodeToString(h[:]), Seals: seals}
			if err := verifySealedExecution(f, expected); err == nil { t.Fatal("incomplete seals accepted even with otherwise exact expected identity") }
		})
	}
}

func TestExpiryDescriptorControlAlteredSeals(t *testing.T) {
	ledger := expiryDescriptorControlLedgerNew(t)
	fd, err := unix.MemfdCreate("altered-seals-control", unix.MFD_ALLOW_SEALING|unix.MFD_CLOEXEC)
	if err != nil { t.Fatal(err) }
	f := os.NewFile(uintptr(fd), "altered-seals")
	expiryDescriptorControlOwnFile(t, ledger, "altered.memfd", f)
	_, err = f.Write([]byte("altered")); expiryDescriptorControlRecord(t, ledger, "altered.write", err); if err != nil { t.Fatal(err) }
	_, err = unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, requiredExecutionSeals|unix.F_SEAL_FUTURE_WRITE)
	expiryDescriptorControlRecord(t, ledger, "altered.seal", err); if err != nil { t.Fatal(err) }
	id, err := identityOf(f); if err != nil { t.Fatal(err) }
	h := sha256.Sum256([]byte("altered"))
	expected := ExecutedImageProvenance{Device: id.device, Inode: id.inode, Size: id.size, Mode: id.mode, SHA256: hex.EncodeToString(h[:]), Seals: requiredExecutionSeals}
	if err := verifySealedExecution(f, expected); err == nil { t.Fatal("altered exact seal set accepted") }
}

func TestExpiryDescriptorControlParent4Decoy(t *testing.T) {
	if os.Getenv(expiryDescriptorControlChildKey) == "parent4-decoy" {
		// This descriptor was explicitly mapped by this test's parent. No unknown
		// integer4 is inspected, closed, replaced or assumed free in the parent.
		ledger := expiryDescriptorControlLedgerNew(t)
		f := os.NewFile(4, "fixture-owned-parent4-decoy")
		expiryDescriptorControlOwnFile(t, ledger, "decoy-parent.inherited4", f)
		expiryDescriptorControlOwnFile(t, ledger, "decoy-parent.inherited3", os.NewFile(3, "fixture-owned-unused3"))
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() { t.Fatal("fixture-owned decoy missing", err) }
		expiryDescriptorControlPositive(t)
		return
	}
	ledger := expiryDescriptorControlLedgerNew(t)
	root := expiryDescriptorControlRoot(t, ledger)
	decoy, err := os.OpenFile(filepath.Join(root, "decoy"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil { t.Fatal(err) }
	expiryDescriptorControlOwnFile(t, ledger, "parent.decoy", decoy)
	unused, err := os.Open("/dev/null"); if err != nil { t.Fatal(err) }
	expiryDescriptorControlOwnFile(t, ledger, "parent.fd3", unused)
	path, err := os.Executable(); if err != nil { t.Fatal(err) }
	cmd := exec.Command(path, "-test.run=^TestExpiryDescriptorControlParent4Decoy$")
	cmd.Env = []string{"LANG=C", "TZ=UTC", expiryDescriptorControlChildKey+"=parent4-decoy"}
	cmd.ExtraFiles = []*os.File{unused, decoy}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err = cmd.Start(); expiryDescriptorControlRecord(t, ledger, "decoy-parent.Start", err)
	if err != nil { t.Fatal(err) }
	err = cmd.Wait(); expiryDescriptorControlRecord(t, ledger, "decoy-parent.wait-exit", err)
	if err != nil { t.Fatal(err) }
}

func TestExpiryDescriptorControlCommandAndProcessSourceGuards(t *testing.T) {
	ledger := expiryDescriptorControlLedgerNew(t)
	v := expiryDescriptorControlImage(t, ledger, expiryDescriptorControlRoot(t, ledger))
	cmd, err := v.command(nil, []string{"SPEC_KITTY_EXPIRY_GATE_FD=4", "SPEC_UNRELATED=forbidden"})
	if err != nil { t.Fatal(err) }
	if cmd.Path != "/proc/self/fd/4" || !reflect.DeepEqual(cmd.ExtraFiles, []*os.File{v.expiry.child, v.execution}) || !reflect.DeepEqual(cmd.Env, []string{"LANG=C", "TZ=UTC", "SPEC_KITTY_EXPIRY_GATE_FD=3"}) { t.Fatal("wrong fixed fd/env/custody") }
	for _, file := range []string{"provenance.go", "expiry_qualification_launch.go"} {
		f, err := os.Open(file); if err != nil { t.Fatal(err) }
		close := expiryDescriptorControlOwnFile(t, ledger, file, f)
		b, readErr := io.ReadAll(f); expiryDescriptorControlRecord(t, ledger, file+".read", readErr); close()
		if readErr != nil { t.Fatal(readErr) }
		s := string(b)
		if file == "provenance.go" {
			_, s, _ = strings.Cut(s, "func (v *VerifiedExecutable) command(")
			a, z := strings.Index(s, "v.checkCommandImage()"), strings.Index(s, "v.fixedCommand(")
			if a < 0 || z <= a { t.Fatal("shared image recheck not before fixed construction") }
		} else {
			_, s, _ = strings.Cut(s, "func (l *Lifecycle) expiryStarted(")
			s, _, _ = strings.Cut(s, "func (l *Lifecycle) checkExpiryStartedProcess(")
			first, last := strings.Index(s, "l.checkExpiryStartedProcess(v.executed)"), strings.LastIndex(s, "l.checkExpiryStartedProcess(v.executed)")
			fd4, fd3 := strings.Index(s, "unix.Open(fmt.Sprintf(\"/proc/%d/fd/4\""), strings.Index(s, "unix.Stat(fmt.Sprintf(\"/proc/%d/fd/3\"")
			if first < 0 || fd4 <= first || fd3 <= fd4 || last <= fd3 { t.Fatal("process/image checks no longer bracket original child fd4/fd3") }
			if strings.Contains(s, "SO_PEERCRED") || strings.Contains(s, "unix.Close(4)") { t.Fatal("creator credentials or unknown parent integer4 used as child authority") }
		}
	}
}

func TestExpiryDescriptorControlHeldSourceAndIndependentImageInputs(t *testing.T) {
	if !t.Run("positive-first", expiryDescriptorControlPositive) { return }
	for _, mode := range []string{"held-path-replacement", "digest-mismatch", "size-mismatch", "mode-mismatch", "hardlink-mismatch", "source-inplace-mutation", "sealed-inplace-write", "held-descriptor-reuse-before-command"} {
		t.Run(mode, func(t *testing.T) {
			ledger := expiryDescriptorControlLedgerNew(t)
			root := expiryDescriptorControlRoot(t, ledger)
			v := expiryDescriptorControlImage(t, ledger, root)
			path := filepath.Join(root, "own-test-ELF") // known fixture-owned path, never os.NewFile's diagnostic Name
			digest, size := v.executed.SHA256, v.executed.Size
			switch mode {
			case "held-path-replacement":
				if err := os.Rename(path, path+".original"); err != nil { t.Fatal(err) }
				f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700); if err != nil { t.Fatal(err) }
				close := expiryDescriptorControlOwnFile(t, ledger, "replacement-source", f)
				_, err = f.Write([]byte("not-an-executable")); expiryDescriptorControlRecord(t, ledger, "replacement-source.write", err); if err != nil { t.Fatal(err) }
				err = f.Sync(); expiryDescriptorControlRecord(t, ledger, "replacement-source.fsync", err); close(); if err != nil { t.Fatal(err) }
				started := expiryDescriptorControlStart(t, ledger, v, "positive", nil)
				if started.startErr != nil { t.Fatal(started.startErr) }
				if err := started.lifecycle.expiryStarted(v); err != nil { t.Fatal("pathname replacement executed something other than verified held image", err) }
				return
			case "digest-mismatch": digest = strings.Repeat("0", 64)
			case "size-mismatch": size++
			case "mode-mismatch": if err := os.Chmod(path, 0777); err != nil { t.Fatal(err) }
			case "hardlink-mismatch": if err := os.Link(path, path+".fixture-hardlink"); err != nil { t.Fatal(err) }
			case "source-inplace-mutation":
				f, err := os.OpenFile(path, os.O_WRONLY, 0); if err != nil { t.Fatal(err) }
				close := expiryDescriptorControlOwnFile(t, ledger, "source-mutator", f)
				_, err = f.WriteAt([]byte("changed"), 0); expiryDescriptorControlRecord(t, ledger, "source-mutator.write", err); if err != nil { t.Fatal(err) }
				err = f.Sync(); expiryDescriptorControlRecord(t, ledger, "source-mutator.fsync", err); close(); if err != nil { t.Fatal(err) }
			case "sealed-inplace-write":
				command, commandErr := v.command(nil, nil)
				expiryDescriptorControlRecord(t, ledger, "sealed-mutation.baseline-command", commandErr)
				if commandErr != nil || command == nil { t.Fatal("same-fixture baseline command refused", commandErr) }
				expectedIdentity, expectedImage := v.executeIdentity, v.executed
				before, beforeErr := identityOf(v.execution)
				expiryDescriptorControlRecord(t, ledger, "sealed-mutation.before-stat", beforeErr)
				if beforeErr != nil || !sameExecutableIdentity(before, expectedIdentity) { t.Fatal("held-image baseline differs", beforeErr) }
				n, writeErr := unix.Pwrite(int(v.execution.Fd()), []byte("changed"), 0)
				expiryDescriptorControlRecord(t, ledger, "sealed-mutation.write", writeErr)
				after, afterErr := identityOf(v.execution)
				expiryDescriptorControlRecord(t, ledger, "sealed-mutation.after-stat", afterErr)
				sealedErr := verifySealedExecution(v.execution, expectedImage)
				expiryDescriptorControlRecord(t, ledger, "sealed-mutation.seals-and-digest", sealedErr)
				command, commandErr = v.command(nil, nil)
				expiryDescriptorControlRecord(t, ledger, "sealed-mutation.after-command", commandErr)
				t.Logf("actual sealed write count=%d err=%v before=%+v after=%+v original=%+v seals/digest=%v", n, writeErr, before, after, expectedIdentity, sealedErr)
				if n > 0 || !errors.Is(writeErr, unix.EPERM) { t.Fatal("sealed held image accepted in-place mutation", n, writeErr) }
				beforeCore, afterCore := before, after
				beforeCore.mtimeNsec, beforeCore.ctimeNsec = 0, 0
				afterCore.mtimeNsec, afterCore.ctimeNsec = 0, 0
				if afterErr != nil || beforeCore != afterCore || sealedErr != nil || v.executeIdentity != expectedIdentity || v.executed != expectedImage {
					t.Fatal("sealed content/seals/non-time identity changed or original expectation refreshed", afterErr, sealedErr)
				}
				if after.mtimeNsec != expectedIdentity.mtimeNsec {
					// Linux may update timestamps even when the sealed write fails.
					// That must still refuse the original metadata-bound command.
					var refusal *Error
					if command != nil || !errors.As(commandErr, &refusal) || refusal.Kind != FailureIdentityUnprovable || refusal.Operation != "exec" || refusal.Reason != "verified descriptor identity changed" {
						t.Fatal("changed mtime did not refuse original identity", commandErr)
					}
				} else if commandErr != nil || command == nil {
					t.Fatal("unchanged command identity refused after failed write", commandErr)
				}
				return
			case "held-descriptor-reuse-before-command":
				err := v.execution.Close(); v.expiry.report.imageClosed(err); expiryDescriptorControlCleanupResult(t, ledger, "verified.image.close", err)
				v.execution = nil
				fd, err := unix.MemfdCreate("held-descriptor-reuse-before-command", unix.MFD_ALLOW_SEALING|unix.MFD_CLOEXEC); if err != nil { t.Fatal(err) }
				f := os.NewFile(uintptr(fd), "owned-reused-image-substitute")
				expiryDescriptorControlOwnFile(t, ledger, "held-substitute", f)
				v.execution = f
				_, commandErr := v.command(nil, nil)
				v.execution = nil // fixture owns f; product must not double-close it
				if commandErr == nil { t.Fatal("reused held image descriptor accepted") }
				return
			}
			refusalReport := new(expiryCleanupReport)
			other, err := verifyExecutableImage(path, digest, size, ExecutableProvenance{ContractRevision: expiryQualificationContract}, refusalReport)
			expiryDescriptorControlRecord(t, ledger, "actual.image-refusal", err)
			expiryDescriptorControlCleanupResult(t, ledger, "actual.image-refusal.cleanup-report", refusalReport.cleanupError())
			if other != nil { expiryDescriptorControlCleanupResult(t, ledger, "unexpected.verified.close", other.Close()) }
			if err == nil { t.Fatal("image-input mutation accepted", mode) }
		})
	}
}

// Compile-time reminder that only the real UnixConn is the fixed parent type;
// socket file mode alone is not an AF_UNIX credential (TCP control is separate).
var _ net.Conn = (*net.UnixConn)(nil)
