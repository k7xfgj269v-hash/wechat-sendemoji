package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var ErrProcessAlreadyRunning = errors.New("process already running")
var ErrUnsafeRuntimePath = errors.New("unsafe runtime path")

const processLifecycleSocketProbeTimeout = 200 * time.Millisecond

type processLifecycleOwnerFunc func(os.FileInfo) (uint32, bool)
type processLifecycleDialFunc func(string, time.Duration) (net.Conn, error)

type ProcessLock struct {
	file *os.File

	closeOnce sync.Once
	closeErr  error
}

func AcquireProcessLock(path string) (*ProcessLock, error) {
	return acquireProcessLock(path, uint32(os.Geteuid()), agentFileOwner)
}

func acquireProcessLock(
	path string,
	uid uint32,
	owner processLifecycleOwnerFunc,
) (*ProcessLock, error) {
	parent, _, err := openProcessLifecycleParent(path, uid, owner)
	if err != nil {
		return nil, err
	}
	defer parent.Close()

	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	fd, err := unix.Openat(
		int(parent.Fd()),
		filepath.Base(path),
		flags|unix.O_CREAT|unix.O_EXCL,
		0600,
	)
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(
			int(parent.Fd()),
			filepath.Base(path),
			flags,
			0,
		)
	}
	if err != nil {
		return nil, ErrUnsafeRuntimePath
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("process lock descriptor is unavailable")
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !validProcessLifecycleFile(info, 0600, uid, owner, 0) {
		_ = file.Close()
		return nil, ErrUnsafeRuntimePath
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrProcessAlreadyRunning
		}
		return nil, err
	}

	pathInfo, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, pathInfo) ||
		!validProcessLifecycleFile(pathInfo, 0600, uid, owner, 0) {
		_ = file.Close()
		return nil, ErrUnsafeRuntimePath
	}
	return &ProcessLock{file: file}, nil
}

func (l *ProcessLock) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		if l.file != nil {
			l.closeErr = l.file.Close()
		}
	})
	return l.closeErr
}

func PrepareUnixSocket(path string, uid uint32) error {
	return prepareUnixSocket(path, uid, agentFileOwner, dialProcessLifecycleUnix)
}

func prepareUnixSocket(
	path string,
	uid uint32,
	owner processLifecycleOwnerFunc,
	dial processLifecycleDialFunc,
) error {
	if len([]byte(path)) > agentMaxUnixSocketPathBytes {
		return ErrUnsafeRuntimePath
	}
	parent, parentInfo, err := openProcessLifecycleParent(path, uid, owner)
	if err != nil {
		return err
	}
	defer parent.Close()

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !validProcessLifecycleFile(info, 0600, uid, owner, os.ModeSocket) {
		return ErrUnsafeRuntimePath
	}

	conn, err := dial(path, processLifecycleSocketProbeTimeout)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		return ErrProcessAlreadyRunning
	}
	if !errors.Is(err, unix.ECONNREFUSED) {
		return err
	}

	currentParent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !os.SameFile(parentInfo, currentParent) ||
		!validProcessLifecycleParent(currentParent, uid, owner) {
		return ErrUnsafeRuntimePath
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) ||
		!validProcessLifecycleFile(current, 0600, uid, owner, os.ModeSocket) {
		return ErrUnsafeRuntimePath
	}
	return os.Remove(path)
}

func dialProcessLifecycleUnix(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}

func openProcessLifecycleParent(
	path string,
	uid uint32,
	owner processLifecycleOwnerFunc,
) (*os.File, os.FileInfo, error) {
	if !validProcessLifecyclePath(path) {
		return nil, nil, ErrUnsafeRuntimePath
	}
	parentPath := filepath.Dir(path)
	fd, err := unix.Open(
		parentPath,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, nil, ErrUnsafeRuntimePath
	}
	parent := os.NewFile(uintptr(fd), parentPath)
	if parent == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("runtime parent descriptor is unavailable")
	}
	info, err := parent.Stat()
	if err != nil {
		_ = parent.Close()
		return nil, nil, err
	}
	if !validProcessLifecycleParent(info, uid, owner) {
		_ = parent.Close()
		return nil, nil, ErrUnsafeRuntimePath
	}
	return parent, info, nil
}

func validProcessLifecyclePath(path string) bool {
	return path != "" &&
		strings.IndexByte(path, 0) < 0 &&
		filepath.IsAbs(path) &&
		filepath.Clean(path) == path &&
		filepath.Dir(path) != path
}

func validProcessLifecycleParent(
	info os.FileInfo,
	uid uint32,
	owner processLifecycleOwnerFunc,
) bool {
	if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0700 {
		return false
	}
	fileUID, ok := owner(info)
	return ok && fileUID == uid
}

func validProcessLifecycleFile(
	info os.FileInfo,
	permissions os.FileMode,
	uid uint32,
	owner processLifecycleOwnerFunc,
	fileType os.FileMode,
) bool {
	if info == nil || info.Mode()&os.ModeType != fileType ||
		info.Mode().Perm() != permissions {
		return false
	}
	fileUID, ok := owner(info)
	return ok && fileUID == uid
}
