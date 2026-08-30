// Package gapdb owns the Linux lifecycle of the exact GapDB sidecar admitted
// by M1. It deliberately does not discover executables or services.
package gapdb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"golang.org/x/sys/unix"
)

const (
	admittedExecutableSize = int64(5_300_621)
	maxDiagnosticBytes     = 2 << 10
	maxEvidencePathBytes   = 4 << 10
	contractRevision       = "gapdb-sidecar-lifecycle/v1"
)

// FailureKind is the closed, operator-safe lifecycle diagnostic vocabulary.
type FailureKind string

const (
	FailureInvalidConfiguration FailureKind = "invalid_configuration"
	FailureUnsafePath           FailureKind = "unsafe_path"
	FailureAdmissionMismatch    FailureKind = "admission_mismatch"
	FailureIdentityUnprovable   FailureKind = "executable_identity_unprovable"
	FailureStart                FailureKind = "start_failed"
	FailureReadiness            FailureKind = "readiness_refused"
	FailureOwnership            FailureKind = "ownership_indeterminate"
	FailureProtocol             FailureKind = "protocol_refused"
	FailureCleanup              FailureKind = "cleanup_refused"
)

// Error contains bounded facts only. It never includes executable bytes,
// environment values, protocol transcripts, or provider error prose.
type Error struct {
	Kind      FailureKind
	Operation string
	Reason    string
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := fmt.Sprintf("gapdb sidecar %s: %s", bounded(e.Operation, 64), e.Kind)
	if e.Reason != "" {
		message += " reason=" + bounded(e.Reason, 256)
	}
	return bounded(message, maxDiagnosticBytes)
}

func (e *Error) Unwrap() error { return e.Cause }

// IsFailure reports whether err carries the requested stable failure kind.
func IsFailure(err error, kind FailureKind) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Kind == kind
}

func failure(kind FailureKind, operation, reason string, cause error) error {
	return &Error{Kind: kind, Operation: operation, Reason: bounded(reason, 256), Cause: cause}
}

// VerifyConfig names both trusted inputs. Neither value may be relative or
// discovered through PATH.
type VerifyConfig struct {
	ExecutablePath      string
	AdmissionRecordPath string
}

// ExecutableProvenance is the bounded host proof bound to an open executable.
type ExecutableProvenance struct {
	ExecutablePath        string
	SHA256                string
	Size                  int64
	Device                uint64
	Inode                 uint64
	OwnerUID              uint32
	OwnerGID              uint32
	Mode                  uint32
	SourceRevision        string
	SourceTree            string
	ModuleVersion         string
	AdmissionRecordSHA256 string
	ContractRevision      string
}

// VerifiedExecutable holds the exact descriptor whose bytes were verified.
// It is consumed by Lifecycle.Start and is never resolved through PATH.
type VerifiedExecutable struct {
	file       *os.File
	provenance ExecutableProvenance
	identity   fileIdentity
}

type fileIdentity struct {
	device    uint64
	inode     uint64
	size      int64
	mode      uint32
	uid       uint32
	gid       uint32
	nlink     uint64
	mtimeNsec int64
	ctimeNsec int64
}

func identityFromInfo(info os.FileInfo) (fileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, errors.New("filesystem identity is unavailable")
	}
	return fileIdentity{
		device: uint64(stat.Dev), inode: stat.Ino, size: stat.Size, mode: stat.Mode,
		uid: stat.Uid, gid: stat.Gid, nlink: uint64(stat.Nlink),
		mtimeNsec: stat.Mtim.Sec*1_000_000_000 + stat.Mtim.Nsec,
		ctimeNsec: stat.Ctim.Sec*1_000_000_000 + stat.Ctim.Nsec,
	}, nil
}

type admissionRecord struct {
	SchemaVersion uint64 `json:"schema_version"`
	Decision      string `json:"decision"`
	GapDBTuple    struct {
		SourceRevision string `json:"source_revision"`
		SourceTree     string `json:"source_tree"`
		ModuleVersion  string `json:"module_version"`
		Sidecar        struct {
			Command string `json:"command"`
			SHA256  string `json:"sha256"`
			Size    int64  `json:"size_bytes"`
		} `json:"sidecar"`
		Protocol struct {
			WireVersion    uint64 `json:"wire_version"`
			StorageVersion uint64 `json:"storage_version"`
		} `json:"protocol"`
		Platform struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
		} `json:"platform"`
	} `json:"gapdb_tuple"`
}

// VerifyExecutable validates the admission record and executable from held
// descriptors. Every component is checked without accepting symlinks.
func VerifyExecutable(config VerifyConfig) (*VerifiedExecutable, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, failure(FailureIdentityUnprovable, "verify", "platform is not admitted", nil)
	}
	if err := validateAbsolutePath(config.ExecutablePath); err != nil {
		return nil, failure(FailureInvalidConfiguration, "verify", "executable path is not absolute and normalized", err)
	}
	if err := validateAbsolutePath(config.AdmissionRecordPath); err != nil {
		return nil, failure(FailureInvalidConfiguration, "verify", "admission path is not absolute and normalized", err)
	}
	if err := validatePathChain(config.ExecutablePath, true); err != nil {
		return nil, failure(FailureUnsafePath, "verify", "executable path ownership or mode is unsafe", err)
	}
	if err := validateRecordPath(config.AdmissionRecordPath); err != nil {
		return nil, failure(FailureUnsafePath, "verify", "admission path ownership or mode is unsafe", err)
	}

	recordBytes, recordDigest, err := readExactNoFollow(config.AdmissionRecordPath, 64<<10)
	if err != nil {
		return nil, failure(FailureAdmissionMismatch, "verify", "admission record cannot be verified", err)
	}
	if recordDigest != storegapdb.AdmittedTupleSHA256 {
		return nil, failure(FailureAdmissionMismatch, "verify", "admission record digest differs", nil)
	}
	var record admissionRecord
	if err := json.Unmarshal(recordBytes, &record); err != nil {
		return nil, failure(FailureAdmissionMismatch, "verify", "admission record is malformed", err)
	}
	if err := validateAdmission(record); err != nil {
		return nil, failure(FailureAdmissionMismatch, "verify", "admission tuple differs", err)
	}

	fd, err := openNoSymlinks(config.ExecutablePath)
	if err != nil {
		return nil, failure(FailureUnsafePath, "verify", "executable cannot be opened without following links", err)
	}
	file := os.NewFile(uintptr(fd), "admitted-gapdbd")
	verified := &VerifiedExecutable{file: file}
	verified.identity, err = identityOf(file)
	if err != nil {
		_ = file.Close()
		return nil, failure(FailureIdentityUnprovable, "verify", "executable identity cannot be read", err)
	}
	if err := validateExecutableIdentity(verified.identity); err != nil {
		_ = file.Close()
		return nil, failure(FailureUnsafePath, "verify", "executable metadata is not admitted", err)
	}
	digest, err := digestFile(file, admittedExecutableSize)
	if err != nil {
		_ = file.Close()
		return nil, failure(FailureAdmissionMismatch, "verify", "executable cannot be hashed", err)
	}
	if digest != storegapdb.AdmittedSidecarSHA256 {
		_ = file.Close()
		return nil, failure(FailureAdmissionMismatch, "verify", "executable digest differs", nil)
	}
	verified.provenance = ExecutableProvenance{
		ExecutablePath: bounded(config.ExecutablePath, maxEvidencePathBytes), SHA256: digest,
		Size: verified.identity.size, Device: verified.identity.device, Inode: verified.identity.inode,
		OwnerUID: verified.identity.uid, OwnerGID: verified.identity.gid, Mode: verified.identity.mode,
		SourceRevision: record.GapDBTuple.SourceRevision, SourceTree: record.GapDBTuple.SourceTree,
		ModuleVersion: record.GapDBTuple.ModuleVersion, AdmissionRecordSHA256: recordDigest,
		ContractRevision: contractRevision,
	}
	return verified, nil
}

func validateRecordPath(path string) error {
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("admission path contains a symlink")
		}
		last := index == len(components)-1
		if (!last && !info.IsDir()) || (last && !info.Mode().IsRegular()) {
			return errors.New("admission path type differs")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			return errors.New("admission path owner differs")
		}
	}
	return nil
}

// Provenance returns a value copy; it exposes no descriptor capability.
func (v *VerifiedExecutable) Provenance() ExecutableProvenance {
	if v == nil {
		return ExecutableProvenance{}
	}
	return v.provenance
}

// Close releases an unused verified capability. Start consumes it regardless
// of whether process creation succeeds.
func (v *VerifiedExecutable) Close() error {
	if v == nil || v.file == nil {
		return nil
	}
	err := v.file.Close()
	v.file = nil
	return err
}

func (v *VerifiedExecutable) command(arguments, environment []string) (*exec.Cmd, error) {
	if v == nil || v.file == nil {
		return nil, failure(FailureIdentityUnprovable, "exec", "verified descriptor is absent", nil)
	}
	current, err := identityOf(v.file)
	if err != nil || !sameExecutableIdentity(current, v.identity) {
		return nil, failure(FailureIdentityUnprovable, "exec", "verified descriptor identity changed", err)
	}
	digest, err := digestFile(v.file, admittedExecutableSize)
	if err != nil || digest != v.provenance.SHA256 {
		return nil, failure(FailureIdentityUnprovable, "exec", "verified descriptor bytes changed", err)
	}
	// ExtraFiles maps the held file to child fd 3. The kernel resolves this
	// descriptor path during exec, binding executed bytes to the verified file
	// even if the configured pathname is concurrently renamed or replaced.
	command := exec.Command("/proc/self/fd/3", arguments...)
	command.ExtraFiles = []*os.File{v.file}
	command.Env = append([]string(nil), environment...)
	return command, nil
}

func sameExecutableIdentity(first, second fileIdentity) bool {
	return first.device == second.device && first.inode == second.inode && first.size == second.size &&
		first.mode == second.mode && first.uid == second.uid && first.gid == second.gid &&
		first.nlink == second.nlink && first.mtimeNsec == second.mtimeNsec
}

func validateAdmission(record admissionRecord) error {
	if record.SchemaVersion != 1 || record.Decision != "admitted" ||
		record.GapDBTuple.SourceRevision != storegapdb.AdmittedSourceRevision ||
		record.GapDBTuple.SourceTree != storegapdb.AdmittedSourceTree ||
		record.GapDBTuple.ModuleVersion != storegapdb.AdmittedModuleVersion ||
		record.GapDBTuple.Sidecar.Command != "gapdbd" ||
		record.GapDBTuple.Sidecar.SHA256 != storegapdb.AdmittedSidecarSHA256 ||
		record.GapDBTuple.Sidecar.Size != admittedExecutableSize ||
		record.GapDBTuple.Protocol.WireVersion != storegapdb.AdmittedWireVersion ||
		record.GapDBTuple.Protocol.StorageVersion != storegapdb.AdmittedStorageVersion ||
		record.GapDBTuple.Platform.OS != "linux" || record.GapDBTuple.Platform.Arch != "amd64" {
		return errors.New("record fields do not equal the pinned tuple")
	}
	return nil
}

func validateAbsolutePath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("path must be an absolute clean path")
	}
	if len(path) > maxEvidencePathBytes {
		return errors.New("path exceeds evidence bound")
	}
	return nil
}

func validatePathChain(path string, executable bool) error {
	if err := validateAbsolutePath(path); err != nil {
		return err
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink component")
		}
		last := index == len(components)-1
		if !last && !info.IsDir() {
			return errors.New("non-directory ancestor")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			return errors.New("unexpected path owner")
		}
		writable := info.Mode().Perm()&0o022 != 0
		// A sticky root-owned temporary directory is an isolation boundary: an
		// unprivileged peer cannot rename or remove another owner's child. All
		// descendants still have to be owner-only and symlink-free.
		stickyTempBoundary := info.IsDir() && info.Mode()&os.ModeSticky != 0 && stat.Uid == 0
		if writable && !stickyTempBoundary {
			return errors.New("group/world-writable path component")
		}
		if last && executable && !info.Mode().IsRegular() {
			return errors.New("executable is not regular")
		}
	}
	return nil
}

func validateExecutableIdentity(identity fileIdentity) error {
	if identity.size != admittedExecutableSize || identity.nlink != 1 {
		return errors.New("size or hard-link count differs")
	}
	if identity.mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFREG) || identity.mode&0o111 == 0 || identity.mode&0o022 != 0 {
		return errors.New("file type or mode is unsafe")
	}
	if identity.uid != 0 && identity.uid != uint32(os.Geteuid()) {
		return errors.New("file owner differs")
	}
	return nil
}

func identityOf(file *os.File) (fileIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return fileIdentity{}, err
	}
	return fileIdentity{
		device: uint64(stat.Dev), inode: stat.Ino, size: stat.Size, mode: stat.Mode,
		uid: stat.Uid, gid: stat.Gid, nlink: uint64(stat.Nlink),
		mtimeNsec: stat.Mtim.Sec*1_000_000_000 + stat.Mtim.Nsec,
		ctimeNsec: stat.Ctim.Sec*1_000_000_000 + stat.Ctim.Nsec,
	}, nil
}

func digestFile(file *os.File, maximum int64) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil {
		return "", err
	}
	if written != maximum {
		return "", errors.New("file size differs during hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readExactNoFollow(path string, maximum int64) ([]byte, string, error) {
	fd, err := openNoSymlinks(path)
	if err != nil {
		return nil, "", err
	}
	file := os.NewFile(uintptr(fd), "verified-record")
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(encoded)) > maximum {
		return nil, "", errors.New("record exceeds bound or cannot be read")
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func bounded(value string, maximum int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func openNoSymlinks(path string) (int, error) {
	return unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS),
	})
}
