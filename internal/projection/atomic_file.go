package projection

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

type fileStage string

const (
	stageCreate        fileStage = "create"
	stageChmod         fileStage = "chmod"
	stageWrite         fileStage = "write"
	stageFileSync      fileStage = "file_sync"
	stageClose         fileStage = "close"
	stageRename        fileStage = "rename"
	stageDirectorySync fileStage = "directory_sync"
)

type faultHook func(fileStage) error

// WriteAtomic safely replaces one repository-relative projection beneath an
// explicit trusted root. The destination directory must already exist.
func WriteAtomic(trustedRoot, destination string, content []byte, mode fs.FileMode) error {
	return writeAtomic(trustedRoot, destination, content, mode, nil)
}

func writeAtomic(trustedRoot, destination string, content []byte, mode fs.FileMode, hook faultHook) error {
	if !safeDestination(destination) || !safeMode(mode) {
		return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "destination")
	}
	rootInfo, err := os.Lstat(trustedRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "trusted_root")
	}
	root, err := os.OpenRoot(trustedRoot)
	if err != nil {
		return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "trusted_root")
	}
	defer root.Close()
	anchoredRootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(rootInfo, anchoredRootInfo) {
		return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "trusted_root")
	}

	parentName, base := path.Split(destination)
	parentName = strings.TrimSuffix(parentName, "/")
	if parentName == "" {
		parentName = "."
	}
	if err := validateParents(root, parentName); err != nil {
		return err
	}
	parent, err := root.OpenRoot(parentName)
	if err != nil {
		return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "destination_parent")
	}
	defer parent.Close()
	checkedParentInfo, checkErr := root.Lstat(parentName)
	anchoredParentInfo, anchorErr := parent.Stat(".")
	if checkErr != nil || anchorErr != nil || checkedParentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(checkedParentInfo, anchoredParentInfo) {
		return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "destination_parent")
	}
	if info, statErr := parent.Lstat(base); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "destination")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "destination")
	}

	if err := invoke(hook, stageCreate); err != nil {
		return projectionError(KindWriteFailure, RecoveryRetry, "create")
	}
	temporary, file, err := createTemporary(parent)
	if err != nil {
		return projectionError(KindWriteFailure, RecoveryRetry, "create")
	}
	renamed := false
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if !renamed {
			_ = parent.Remove(temporary)
		}
	}()
	if err := invoke(hook, stageChmod); err != nil || file.Chmod(mode) != nil {
		return projectionError(KindWriteFailure, RecoveryRetry, "chmod")
	}
	if err := invoke(hook, stageWrite); err != nil || writeComplete(file, content) != nil {
		return projectionError(KindWriteFailure, RecoveryRetry, "write")
	}
	if err := invoke(hook, stageFileSync); err != nil || file.Sync() != nil {
		return projectionError(KindWriteFailure, RecoveryRetry, "file_sync")
	}
	if err := invoke(hook, stageClose); err != nil || file.Close() != nil {
		return projectionError(KindWriteFailure, RecoveryRetry, "close")
	}
	closed = true
	if err := invoke(hook, stageRename); err != nil || parent.Rename(temporary, base) != nil {
		return projectionError(KindReplaceFailure, RecoveryRetry, "rename")
	}
	renamed = true
	if err := invoke(hook, stageDirectorySync); err != nil {
		return projectionError(KindReplaceFailure, RecoveryRetry, "directory_sync")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return projectionError(KindReplaceFailure, RecoveryRetry, "directory_sync")
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if !supportedDirectorySync(err) || closeErr != nil {
		return projectionError(KindReplaceFailure, RecoveryRetry, "directory_sync")
	}
	return nil
}

func safeDestination(value string) bool {
	return value != "" && value != "." && !filepath.IsAbs(value) && !strings.ContainsAny(value, "\\\x00") && fs.ValidPath(value) && path.Clean(value) == value
}

func safeMode(mode fs.FileMode) bool { return mode == 0o600 || mode == 0o644 }

func validateParents(root *os.Root, parent string) error {
	if parent == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(parent, "/") {
		if current == "" {
			current = component
		} else {
			current += "/" + component
		}
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return projectionError(KindUnsafeOutputPath, RecoveryCorrect, "destination_parent")
		}
	}
	return nil
}

func createTemporary(parent *os.Root) (string, *os.File, error) {
	for attempts := 0; attempts < 16; attempts++ {
		var entropy [16]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", nil, err
		}
		name := ".spec-kitty-projection-" + hex.EncodeToString(entropy[:]) + ".tmp"
		file, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, errors.New("temporary-name attempts exhausted")
}

func writeComplete(file *os.File, content []byte) error {
	for len(content) > 0 {
		written, err := file.Write(content)
		if err != nil {
			return err
		}
		if written <= 0 {
			return errors.New("short write")
		}
		content = content[written:]
	}
	return nil
}

func invoke(hook faultHook, stage fileStage) error {
	if hook == nil {
		return nil
	}
	return hook(stage)
}

func supportedDirectorySync(err error) bool {
	return err == nil || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)
}
