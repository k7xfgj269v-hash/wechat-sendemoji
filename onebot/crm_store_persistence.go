package main

import (
	"errors"
	"os"
	"path/filepath"
)

func ensureCRMParent(path string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return WrapCRMPersistence("create parent directory", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return WrapCRMPersistence("set parent directory permissions", err)
	}
	return nil
}

// crmRollbackPath returns the fixed path used to hold the previous snapshot
// aside during an atomic write (see writeSnapshotBytesAtomic). The fixed
// name does not collide with the temporary snapshot pattern (.<base>.tmp-*).
func crmRollbackPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".rollback")
}

func writeSnapshotBytesAtomic(path string, data []byte, syncDirectory func(string) error) error {
	path = filepath.Clean(path)
	if err := ensureCRMParent(path); err != nil {
		return err
	}
	if syncDirectory == nil {
		syncDirectory = syncCRMDirectory
	}
	parent := filepath.Dir(path)
	base := filepath.Base(path)
	rollbackPath := crmRollbackPath(path)

	// Move the previous snapshot aside by rename (O(1)) instead of reading
	// it back into memory, so a failed directory sync below can restore the
	// old snapshot without re-reading or rewriting its content. CRM has no
	// crash-recovery marker protocol; a stale rollback left by a crash is
	// simply replaced by the next rename and removed on the next success.
	existed := false
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return WrapCRMPersistence("inspect previous snapshot", errors.New("snapshot must be a regular file"))
		}
		existed = true
		if err := os.Rename(path, rollbackPath); err != nil {
			return WrapCRMPersistence("prepare rollback snapshot", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return WrapCRMPersistence("inspect previous snapshot", err)
	}

	// Any failure after the rename-aside must put the previous snapshot
	// back (or remove a first snapshot) so an in-process failure leaves
	// disk untouched, matching the previous restore semantics. The caller's
	// error is returned unchanged (with its specific operation wrap); only a
	// rollback failure joins both errors into the chain.
	restore := func(cause error) error {
		if err := restorePreviousSnapshot(path, rollbackPath, existed, syncDirectory); err != nil {
			return WrapCRMPersistence("sync snapshot directory and roll back", errors.Join(cause, err))
		}
		return cause
	}

	temp, err := os.CreateTemp(parent, "."+base+".tmp-*")
	if err != nil {
		return restore(WrapCRMPersistence("create temporary snapshot", err))
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return restore(WrapCRMPersistence("set temporary snapshot permissions", err))
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return restore(WrapCRMPersistence("write temporary snapshot", err))
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return restore(WrapCRMPersistence("sync temporary snapshot", err))
	}
	if err := temp.Close(); err != nil {
		return restore(WrapCRMPersistence("close temporary snapshot", err))
	}
	if err := os.Rename(tempPath, path); err != nil {
		return restore(WrapCRMPersistence("replace snapshot", err))
	}
	removeTemp = false
	if err := syncDirectory(parent); err != nil {
		return restore(WrapCRMPersistence("sync snapshot directory", err))
	}
	// The new snapshot is durable; the rollback copy is no longer needed.
	// Removal is best effort: a leftover rollback is harmless and is
	// replaced by the rename on the next write.
	_ = os.Remove(rollbackPath)
	return nil
}

// restorePreviousSnapshot restores the renamed-away previous snapshot after
// a failed write: the first-write case removes the newly written snapshot
// (existed == false), the replacement case renames the rollback file back
// over it (existed == true). The parent directory is synced afterwards so
// the restored state is durable.
func restorePreviousSnapshot(path, rollbackPath string, existed bool, syncDirectory func(string) error) error {
	parent := filepath.Dir(path)
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncDirectory(parent)
	}
	if err := os.Rename(rollbackPath, path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(parent)
}

// recoverCRMRollbackSnapshot restores a rollback snapshot left behind by a
// write that crashed between renaming the previous snapshot aside and
// replacing it (see writeSnapshotBytesAtomic). It reports whether a rollback
// existed and was moved back to path. The rollback must be a regular file,
// mirroring the guard used for the snapshot itself.
func recoverCRMRollbackSnapshot(path string) (bool, error) {
	rollbackPath := crmRollbackPath(path)
	info, err := os.Lstat(rollbackPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, WrapCRMPersistence("inspect rollback snapshot", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, WrapCRMPersistence("inspect rollback snapshot", errors.New("rollback snapshot must be a regular file"))
	}
	if err := os.Rename(rollbackPath, path); err != nil {
		return false, WrapCRMPersistence("restore rollback snapshot", err)
	}
	return true, nil
}

func syncCRMDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
