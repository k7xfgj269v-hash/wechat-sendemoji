package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func writeAutomationSnapshotAtomic(path string, state AutomationState) error {
	return writeAutomationSnapshotAtomicWithOps(
		path,
		state,
		automationPersistenceOps{syncDirectory: syncAutomationDirectory},
	)
}

func writeAutomationSnapshotAtomicWithDirectorySync(
	path string,
	state AutomationState,
	syncDirectory func(string) error,
) error {
	return writeAutomationSnapshotAtomicWithOps(
		path,
		state,
		automationPersistenceOps{syncDirectory: syncDirectory},
	)
}

func writeAutomationSnapshotAtomicWithOps(
	path string,
	state AutomationState,
	ops automationPersistenceOps,
) error {
	normalizeAutomationOutboxChatTypes(&state)
	if !ops.skipPreValidate {
		if err := ValidateAutomationState(&state); err != nil {
			return err
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return WrapAutomationPersistence("encode snapshot", err)
	}
	data = append(data, '\n')
	return writeAutomationSnapshotBytesAtomic(path, data, ops)
}

func ensureAutomationParent(path string) error {
	return ensureAutomationDirectory(filepath.Dir(path))
}

func ensureAutomationDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return WrapAutomationPersistence("create parent directory", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return WrapAutomationPersistence("set parent directory permissions", err)
	}
	return nil
}

func writeAutomationSnapshotBytesAtomic(path string, data []byte, ops automationPersistenceOps) error {
	path = filepath.Clean(path)
	if err := ensureAutomationParent(path); err != nil {
		return err
	}
	ops = ops.normalized()
	if err := resolveAutomationRecovery(path, ops); err != nil {
		return err
	}
	parent := filepath.Dir(path)

	existed, err := automationSnapshotExists(path)
	if err != nil {
		return err
	}
	transaction, err := prepareAutomationRecovery(path, existed, ops)
	if err != nil {
		return err
	}

	temp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return abortAutomationSnapshotWrite(path, transaction, "create temporary snapshot", err, ops)
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
		return abortAutomationSnapshotWrite(path, transaction, "set temporary snapshot permissions", err, ops)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return abortAutomationSnapshotWrite(path, transaction, "write temporary snapshot", err, ops)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return abortAutomationSnapshotWrite(path, transaction, "sync temporary snapshot", err, ops)
	}
	if err := temp.Close(); err != nil {
		return abortAutomationSnapshotWrite(path, transaction, "close temporary snapshot", err, ops)
	}
	if err := ops.before(automationPersistenceStepReplaceSnapshot); err != nil {
		return abortAutomationSnapshotWrite(path, transaction, automationPersistenceStepReplaceSnapshot, err, ops)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return abortAutomationSnapshotWrite(path, transaction, automationPersistenceStepReplaceSnapshot, err, ops)
	}
	removeTemp = false
	if err := ops.sync(automationPersistenceStepSyncSnapshot, parent); err != nil {
		return abortAutomationSnapshotWrite(path, transaction, automationPersistenceStepSyncSnapshot, err, ops)
	}
	if err := commitAutomationRecovery(path, transaction, ops); err != nil {
		return err
	}
	return nil
}

func (ops automationPersistenceOps) normalized() automationPersistenceOps {
	if ops.syncDirectory == nil {
		ops.syncDirectory = syncAutomationDirectory
	}
	return ops
}

func (ops automationPersistenceOps) before(step string) error {
	if ops.beforeStep == nil {
		return nil
	}
	return ops.beforeStep(step)
}

func (ops automationPersistenceOps) sync(step, path string) error {
	ops = ops.normalized()
	if err := ops.before(step); err != nil {
		return err
	}
	return ops.syncDirectory(path)
}

func automationRecoveryPaths(path string) (string, string) {
	parent := filepath.Dir(path)
	base := filepath.Base(path)
	return filepath.Join(parent, "."+base+".rollback"), filepath.Join(parent, "."+base+".recovery")
}

func prepareAutomationRecovery(
	path string,
	existed bool,
	ops automationPersistenceOps,
) (automationRecoveryTransaction, error) {
	rollbackPath, markerPath := automationRecoveryPaths(path)
	markerBytes, err := json.Marshal(automationRecoveryMarker{
		Version:         automationRecoveryMarkerVersion,
		SnapshotExisted: existed,
	})
	if err != nil {
		return automationRecoveryTransaction{}, WrapAutomationPersistence("encode recovery marker", err)
	}
	markerBytes = append(markerBytes, '\n')
	transaction := automationRecoveryTransaction{
		markerBytes:      markerBytes,
		rollbackBytes:    nil,
		rollbackByRename: existed,
		snapshotExisted:  existed,
	}

	if err := ops.before(automationPersistenceStepPrepareRollback); err != nil {
		return automationRecoveryTransaction{}, WrapAutomationPersistence(automationPersistenceStepPrepareRollback, err)
	}
	if existed {
		// The previous snapshot is already a complete, validated file on
		// disk, so the rollback snapshot is created by renaming it aside in
		// O(1) instead of rewriting its full content.
		if err := os.Rename(path, rollbackPath); err != nil {
			return automationRecoveryTransaction{}, WrapAutomationPersistence(automationPersistenceStepPrepareRollback, err)
		}
	} else {
		if err := writeAutomationDurableFileExclusive(rollbackPath, nil); err != nil {
			return automationRecoveryTransaction{}, WrapAutomationPersistence(automationPersistenceStepPrepareRollback, err)
		}
	}
	if err := ops.before(automationPersistenceStepPrepareMarker); err != nil {
		return automationRecoveryTransaction{}, automationPrepareRollbackFailure(path, existed, ops, err)
	}
	if err := writeAutomationDurableFileExclusive(markerPath, transaction.markerBytes); err != nil {
		return automationRecoveryTransaction{}, automationPrepareRollbackFailure(path, existed, ops, err)
	}
	if err := ops.sync(automationPersistenceStepSyncRecovery, filepath.Dir(path)); err != nil {
		return automationRecoveryTransaction{}, automationPrepareRollbackFailure(path, existed, ops, err)
	}
	return transaction, nil
}

// automationPrepareRollbackFailure restores the renamed-away previous
// snapshot (or removes the empty rollback of a first write) after a
// preparation step fails, so an in-process failure leaves both memory and
// disk untouched. A process crash in the same window is handled by
// resolveAutomationRecovery, which restores a rollback snapshot that exists
// without a recovery marker when no snapshot is present.
func automationPrepareRollbackFailure(
	path string,
	existed bool,
	ops automationPersistenceOps,
	cause error,
) error {
	rollbackPath, markerPath := automationRecoveryPaths(path)
	if existed {
		if err := os.Rename(rollbackPath, path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return automationUncertainPersistenceError(
				"prepare rollback snapshot",
				errors.Join(cause, err),
			)
		}
	} else {
		if err := os.Remove(rollbackPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return automationUncertainPersistenceError(
				"prepare rollback snapshot",
				errors.Join(cause, err),
			)
		}
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return automationUncertainPersistenceError(
			"prepare rollback snapshot",
			errors.Join(cause, err),
		)
	}
	return WrapAutomationPersistence("prepare rollback snapshot", cause)
}

func commitAutomationRecovery(
	path string,
	transaction automationRecoveryTransaction,
	ops automationPersistenceOps,
) error {
	rollbackPath, markerPath := automationRecoveryPaths(path)
	parent := filepath.Dir(path)

	if err := ops.before(automationPersistenceStepRemoveRecoveryMarker); err != nil {
		return abortAutomationSnapshotWrite(path, transaction, automationPersistenceStepRemoveRecoveryMarker, err, ops)
	}
	if err := os.Remove(markerPath); err != nil {
		return abortAutomationSnapshotWrite(path, transaction, automationPersistenceStepRemoveRecoveryMarker, err, ops)
	}
	if err := ops.sync(automationPersistenceStepSyncMarkerRemoval, parent); err != nil {
		return rearmAndAbortAutomationSnapshotWrite(
			path,
			transaction,
			automationPersistenceStepSyncMarkerRemoval,
			err,
			ops,
		)
	}
	if err := ops.before(automationPersistenceStepRemoveRollback); err != nil {
		return rearmAndAbortAutomationSnapshotWrite(
			path,
			transaction,
			automationPersistenceStepRemoveRollback,
			err,
			ops,
		)
	}
	if err := os.Remove(rollbackPath); err != nil {
		return rearmAndAbortAutomationSnapshotWrite(
			path,
			transaction,
			automationPersistenceStepRemoveRollback,
			err,
			ops,
		)
	}
	if err := ops.sync(automationPersistenceStepSyncRollbackRemoval, parent); err != nil {
		if transaction.rollbackByRename {
			// The rollback was the previous snapshot renamed aside and has
			// already been unlinked, so the commit is final and nothing can
			// be rearmed or restored. Report the write as successful: memory
			// and disk stay consistent with the committed snapshot, and a
			// crash can leave at worst an orphan rollback file that
			// resolveAutomationRecovery removes on the next operation.
			return nil
		}
		return rearmAndAbortAutomationSnapshotWrite(
			path,
			transaction,
			automationPersistenceStepSyncRollbackRemoval,
			err,
			ops,
		)
	}
	return nil
}

func rearmAndAbortAutomationSnapshotWrite(
	path string,
	transaction automationRecoveryTransaction,
	operation string,
	cause error,
	ops automationPersistenceOps,
) error {
	if err := ensureAutomationRecoveryArmed(path, transaction, ops); err != nil {
		return automationUncertainPersistenceError(operation, errors.Join(cause, err))
	}
	return abortAutomationSnapshotWrite(path, transaction, operation, cause, ops)
}

func abortAutomationSnapshotWrite(
	path string,
	transaction automationRecoveryTransaction,
	operation string,
	cause error,
	ops automationPersistenceOps,
) error {
	if err := ensureAutomationRecoveryArmed(path, transaction, ops); err != nil {
		return automationUncertainPersistenceError(operation, errors.Join(cause, err))
	}
	if err := restoreAutomationSnapshotFromRecovery(path, ops); err != nil {
		if errors.Is(err, errAutomationPersistenceUncertain) {
			return automationUncertainPersistenceError(operation, errors.Join(cause, err))
		}
		return WrapAutomationPersistence(operation, errors.Join(cause, err))
	}
	return WrapAutomationPersistence(operation, cause)
}

func ensureAutomationRecoveryArmed(
	path string,
	transaction automationRecoveryTransaction,
	ops automationPersistenceOps,
) error {
	rollbackPath, markerPath := automationRecoveryPaths(path)
	if err := ensureAutomationRecoveryFile(rollbackPath, transaction.rollbackBytes, transaction.rollbackByRename); err != nil {
		return err
	}
	if err := ensureAutomationRecoveryFile(markerPath, transaction.markerBytes, false); err != nil {
		return err
	}
	return ops.sync(automationPersistenceStepSyncRecovery, filepath.Dir(path))
}

// ensureAutomationRecoveryFile verifies that a recovery artifact exists with
// the expected content. When the rollback snapshot is the renamed previous
// snapshot its content is not held in memory, so only existence, type and
// permissions are checked (byRename).
func ensureAutomationRecoveryFile(path string, want []byte, byRename bool) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if byRename {
			return errors.New("renamed rollback snapshot is missing")
		}
		return writeAutomationDurableFileExclusive(path, want)
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		return errors.New("recovery file must not be a symbolic link")
	case !info.Mode().IsRegular():
		return errors.New("recovery file must be regular")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if byRename {
		return nil
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("recovery file content does not match the active transaction")
	}
	return nil
}
