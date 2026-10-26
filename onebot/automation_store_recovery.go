package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func resolveAutomationRecovery(path string, ops automationPersistenceOps) error {
	path = filepath.Clean(path)
	ops = ops.normalized()
	rollbackPath, markerPath := automationRecoveryPaths(path)

	markerExists, err := inspectAutomationRecoveryFile(markerPath, "recovery marker")
	if err != nil {
		return err
	}
	rollbackExists, err := inspectAutomationRecoveryFile(rollbackPath, "rollback snapshot")
	if err != nil {
		return err
	}
	if !markerExists {
		if !rollbackExists {
			return nil
		}
		// Without a marker the rollback is normally an orphaned copy and the
		// snapshot is authoritative. With the rename-based protocol the
		// previous snapshot can also be sitting in the rollback file with the
		// marker not yet written; if no snapshot is present, restore it.
		snapshotExists, err := automationSnapshotExists(path)
		if err != nil {
			return err
		}
		if !snapshotExists {
			return restoreAutomationRollbackRename(path, ops)
		}
		if err := ops.before(automationPersistenceStepCleanupOrphanRollback); err != nil {
			return WrapAutomationPersistence(automationPersistenceStepCleanupOrphanRollback, err)
		}
		if err := os.Remove(rollbackPath); err != nil {
			return WrapAutomationPersistence(automationPersistenceStepCleanupOrphanRollback, err)
		}
		if err := ops.sync(automationPersistenceStepSyncRecoveryCleanup, filepath.Dir(path)); err != nil {
			return WrapAutomationPersistence(automationPersistenceStepSyncRecoveryCleanup, err)
		}
		return nil
	}
	if !rollbackExists {
		return automationUncertainPersistenceError(
			"resolve recovery transaction",
			errors.New("recovery marker exists without a rollback snapshot"),
		)
	}
	return restoreAutomationSnapshotFromRecovery(path, ops)
}

func inspectAutomationRecoveryFile(path, label string) (bool, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, WrapAutomationPersistence("inspect "+label, err)
	case info.Mode()&os.ModeSymlink != 0:
		return false, WrapAutomationPersistence("inspect "+label, errors.New(label+" must not be a symbolic link"))
	case !info.Mode().IsRegular():
		return false, WrapAutomationPersistence("inspect "+label, errors.New(label+" must be a regular file"))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return false, WrapAutomationPersistence("set "+label+" permissions", err)
	}
	return true, nil
}

func restoreAutomationSnapshotFromRecovery(path string, ops automationPersistenceOps) error {
	transaction, err := readAutomationRecoveryTransaction(path)
	if err != nil {
		return automationUncertainPersistenceError("read recovery transaction", err)
	}
	if err := ops.before(automationPersistenceStepRestoreSnapshot); err != nil {
		return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
	}

	parent := filepath.Dir(path)
	if transaction.snapshotExisted {
		temp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".restore-*")
		if err != nil {
			return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
		}
		tempPath := temp.Name()
		defer os.Remove(tempPath)
		if err := temp.Chmod(0o600); err != nil {
			_ = temp.Close()
			return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
		}
		if _, err := temp.Write(transaction.rollbackBytes); err != nil {
			_ = temp.Close()
			return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
		}
		if err := temp.Sync(); err != nil {
			_ = temp.Close()
			return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
		}
		if err := temp.Close(); err != nil {
			return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
		}
		if err := os.Rename(tempPath, path); err != nil {
			return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
		}
	} else {
		if err := removeAutomationSnapshotForRecovery(path); err != nil {
			return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
		}
	}
	if err := ops.sync(automationPersistenceStepSyncRestoredSnapshot, parent); err != nil {
		return automationUncertainPersistenceError(automationPersistenceStepSyncRestoredSnapshot, err)
	}
	if err := cleanupAutomationRecoveryArtifacts(path, ops); err != nil {
		return err
	}
	return nil
}

func readAutomationRecoveryTransaction(path string) (automationRecoveryTransaction, error) {
	rollbackPath, markerPath := automationRecoveryPaths(path)
	markerExists, err := inspectAutomationRecoveryFile(markerPath, "recovery marker")
	if err != nil {
		return automationRecoveryTransaction{}, err
	}
	rollbackExists, err := inspectAutomationRecoveryFile(rollbackPath, "rollback snapshot")
	if err != nil {
		return automationRecoveryTransaction{}, err
	}
	if !markerExists || !rollbackExists {
		return automationRecoveryTransaction{}, errors.New("recovery transaction is incomplete")
	}
	markerBytes, err := os.ReadFile(markerPath)
	if err != nil {
		return automationRecoveryTransaction{}, err
	}
	var marker automationRecoveryMarker
	if err := decodeAutomationJSONObject(
		"recovery_marker",
		markerBytes,
		&marker,
		"version",
		"snapshot_existed",
	); err != nil {
		return automationRecoveryTransaction{}, err
	}
	if marker.Version != automationRecoveryMarkerVersion {
		return automationRecoveryTransaction{}, errors.New("unsupported recovery marker version")
	}
	rollbackBytes, err := os.ReadFile(rollbackPath)
	if err != nil {
		return automationRecoveryTransaction{}, err
	}
	if marker.SnapshotExisted {
		if _, err := decodeAutomationState(rollbackBytes); err != nil {
			return automationRecoveryTransaction{}, fmt.Errorf("rollback snapshot is invalid: %w", err)
		}
	} else if len(rollbackBytes) != 0 {
		return automationRecoveryTransaction{}, errors.New("new-file rollback snapshot must be empty")
	}
	return automationRecoveryTransaction{
		markerBytes:     markerBytes,
		rollbackBytes:   rollbackBytes,
		snapshotExisted: marker.SnapshotExisted,
	}, nil
}

func removeAutomationSnapshotForRecovery(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		return errors.New("snapshot must not be a symbolic link during recovery")
	case !info.Mode().IsRegular():
		return errors.New("snapshot must be regular during recovery")
	default:
		return os.Remove(path)
	}
}

func cleanupAutomationRecoveryArtifacts(path string, ops automationPersistenceOps) error {
	rollbackPath, markerPath := automationRecoveryPaths(path)
	if err := ops.before(automationPersistenceStepCleanupRecovery); err != nil {
		return WrapAutomationPersistence(automationPersistenceStepCleanupRecovery, err)
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return WrapAutomationPersistence(automationPersistenceStepCleanupRecovery, err)
	}
	if err := os.Remove(rollbackPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return WrapAutomationPersistence(automationPersistenceStepCleanupRecovery, err)
	}
	if err := ops.sync(automationPersistenceStepSyncRecoveryCleanup, filepath.Dir(path)); err != nil {
		return WrapAutomationPersistence(automationPersistenceStepSyncRecoveryCleanup, err)
	}
	return nil
}

func writeAutomationDurableFileExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func automationUncertainPersistenceError(operation string, err error) error {
	return WrapAutomationPersistence(operation, errors.Join(errAutomationPersistenceUncertain, err))
}

// restoreAutomationRollbackRename restores a previous snapshot that was
// renamed aside as the rollback file before the recovery marker was written
// (rename-based rollback preparation window). The rollback content is
// validated before the rename back, mirroring the marker-based restore path.
// An empty rollback means the interrupted transaction was a first write
// (the snapshot never existed), so the placeholder is discarded instead.
func restoreAutomationRollbackRename(path string, ops automationPersistenceOps) error {
	rollbackPath, _ := automationRecoveryPaths(path)
	if err := ops.before(automationPersistenceStepRestoreSnapshot); err != nil {
		return automationUncertainPersistenceError(automationPersistenceStepRestoreSnapshot, err)
	}
	data, err := os.ReadFile(rollbackPath)
	if err != nil {
		return automationUncertainPersistenceError("read rollback snapshot", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if err := os.Remove(rollbackPath); err != nil {
			return automationUncertainPersistenceError("discard first-write rollback placeholder", err)
		}
		if err := ops.sync(automationPersistenceStepSyncRecoveryCleanup, filepath.Dir(path)); err != nil {
			return automationUncertainPersistenceError(automationPersistenceStepSyncRecoveryCleanup, err)
		}
		return nil
	}
	if _, err := decodeAutomationState(data); err != nil {
		return automationUncertainPersistenceError("validate rollback snapshot", err)
	}
	if err := os.Rename(rollbackPath, path); err != nil {
		return automationUncertainPersistenceError("restore previous snapshot", err)
	}
	if err := ops.sync(automationPersistenceStepSyncRestoredSnapshot, filepath.Dir(path)); err != nil {
		return automationUncertainPersistenceError(automationPersistenceStepSyncRestoredSnapshot, err)
	}
	return nil
}

// automationSnapshotExists reports whether the snapshot file exists and is a
// regular, non-symlink file, mirroring the checks of
// readPreviousAutomationSnapshot without reading its content.
func automationSnapshotExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, WrapAutomationPersistence("inspect previous snapshot", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, WrapAutomationPersistence(
			"inspect previous snapshot",
			errors.New("snapshot must not be a symbolic link"),
		)
	}
	if !info.Mode().IsRegular() {
		return false, WrapAutomationPersistence(
			"inspect previous snapshot",
			errors.New("snapshot must be a regular file"),
		)
	}
	return true, nil
}

func syncAutomationDirectory(path string) error {
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
