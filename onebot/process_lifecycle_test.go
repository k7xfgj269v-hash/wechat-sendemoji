package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestAcquireProcessLockConcurrent(t *testing.T) {
	directory := processLifecycleTestDirectory(t)
	path := filepath.Join(directory, "process.lock")

	const contenders = 16
	start := make(chan struct{})
	type result struct {
		lock *ProcessLock
		err  error
	}
	results := make(chan result, contenders)
	for range contenders {
		go func() {
			<-start
			lock, err := AcquireProcessLock(path)
			results <- result{lock: lock, err: err}
		}()
	}
	close(start)

	var held *ProcessLock
	duplicates := 0
	for range contenders {
		result := <-results
		switch {
		case result.err == nil:
			if held != nil {
				_ = result.lock.Close()
				t.Fatal("multiple concurrent callers acquired the same process lock")
			}
			held = result.lock
		case errors.Is(result.err, ErrProcessAlreadyRunning):
			duplicates++
		default:
			t.Fatalf("AcquireProcessLock() error = %v", result.err)
		}
	}
	if held == nil || duplicates != contenders-1 {
		t.Fatalf("successful locks = %v, duplicates = %d", held != nil, duplicates)
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("lock mode = %v, want regular 0600", info.Mode())
	}
	if uid, ok := agentFileOwner(info); !ok || uid != uint32(os.Geteuid()) {
		t.Fatalf("lock owner = %d, %v", uid, ok)
	}

	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	reacquired, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatalf("AcquireProcessLock() after Close error = %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProcessLockCloseConcurrent(t *testing.T) {
	path := filepath.Join(processLifecycleTestDirectory(t), "process.lock")
	lock, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}

	const closers = 32
	start := make(chan struct{})
	results := make(chan error, closers)
	var wg sync.WaitGroup
	wg.Add(closers)
	for range closers {
		go func() {
			defer wg.Done()
			<-start
			results <- lock.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("ProcessLock.Close() error = %v", err)
		}
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("second ProcessLock.Close() error = %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("ProcessLock.Close() removed the lock file: %v", err)
	}

	reacquired, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatalf("AcquireProcessLock() after concurrent Close error = %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireProcessLockSafety(t *testing.T) {
	t.Run("path", func(t *testing.T) {
		directory := processLifecycleTestDirectory(t)
		for name, path := range map[string]string{
			"empty":    "",
			"relative": "process.lock",
			"unclean":  directory + "/child/../process.lock",
			"NUL":      filepath.Join(directory, "process\x00.lock"),
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := AcquireProcessLock(path); !errors.Is(err, ErrUnsafeRuntimePath) {
					t.Fatalf("AcquireProcessLock(%q) error = %v", path, err)
				}
			})
		}
	})

	t.Run("parent permissions", func(t *testing.T) {
		directory := processLifecycleTestDirectory(t)
		if err := os.Chmod(directory, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireProcessLock(filepath.Join(directory, "process.lock")); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("AcquireProcessLock() error = %v", err)
		}
	})

	t.Run("parent symlink", func(t *testing.T) {
		root := processLifecycleTestDirectory(t)
		target := processLifecycleTestDirectory(t)
		parent := filepath.Join(root, "linked")
		if err := os.Symlink(target, parent); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireProcessLock(filepath.Join(parent, "process.lock")); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("AcquireProcessLock() error = %v", err)
		}
	})

	t.Run("lock symlink", func(t *testing.T) {
		directory := processLifecycleTestDirectory(t)
		target := filepath.Join(directory, "target")
		if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "process.lock")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireProcessLock(path); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("AcquireProcessLock() error = %v", err)
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "keep" {
			t.Fatalf("symlink target changed: %q, %v", data, err)
		}
	})

	t.Run("lock mode", func(t *testing.T) {
		directory := processLifecycleTestDirectory(t)
		path := filepath.Join(directory, "process.lock")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0640); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireProcessLock(path); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("AcquireProcessLock() error = %v", err)
		}
		if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0640 {
			t.Fatalf("unsafe lock changed: %v, %v", info, err)
		}
	})

	t.Run("lock owner", func(t *testing.T) {
		directory := processLifecycleTestDirectory(t)
		path := filepath.Join(directory, "process.lock")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		owner := func(info os.FileInfo) (uint32, bool) {
			uid, ok := agentFileOwner(info)
			if ok && info.Mode().IsRegular() {
				return uid + 1, true
			}
			return uid, ok
		}
		if _, err := acquireProcessLock(path, uint32(os.Geteuid()), owner); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("acquireProcessLock() error = %v", err)
		}
	})
}

func TestPrepareUnixSocketState(t *testing.T) {
	uid := uint32(os.Geteuid())
	root := processLifecycleTestDirectory(t)

	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(processLifecycleChildDirectory(t, root, "m"), "s")
		if err := PrepareUnixSocket(path, uid); err != nil {
			t.Fatalf("PrepareUnixSocket() error = %v", err)
		}
	})

	t.Run("stale", func(t *testing.T) {
		path := processLifecycleStaleSocket(t, processLifecycleChildDirectory(t, root, "s"))
		if err := PrepareUnixSocket(path, uid); err != nil {
			t.Fatalf("PrepareUnixSocket() error = %v", err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale socket still exists: %v", err)
		}
	})
}

func TestPrepareUnixSocketActive(t *testing.T) {
	uid := uint32(os.Geteuid())
	path := filepath.Join(processLifecycleTestDirectory(t), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := PrepareUnixSocket(path, uid); !errors.Is(err, ErrProcessAlreadyRunning) {
		t.Fatalf("PrepareUnixSocket() error = %v", err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("active socket was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("active socket was replaced")
	}
}

func TestPrepareUnixSocketDanger(t *testing.T) {
	uid := uint32(os.Geteuid())
	root := processLifecycleTestDirectory(t)

	t.Run("parent permissions", func(t *testing.T) {
		path := processLifecycleStaleSocket(t, processLifecycleChildDirectory(t, root, "p"))
		if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := PrepareUnixSocket(path, uid); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("PrepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, path)
	})

	t.Run("parent symlink", func(t *testing.T) {
		target := processLifecycleChildDirectory(t, root, "t")
		targetPath := filepath.Join(target, "s")
		processLifecycleCreateStaleSocket(t, targetPath)
		parent := filepath.Join(root, "l")
		if err := os.Symlink(target, parent); err != nil {
			t.Fatal(err)
		}
		if err := PrepareUnixSocket(filepath.Join(parent, "s"), uid); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("PrepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, targetPath)
	})

	t.Run("symlink", func(t *testing.T) {
		directory := processLifecycleChildDirectory(t, root, "y")
		target := filepath.Join(directory, "target")
		if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "s")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if err := PrepareUnixSocket(path, uid); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("PrepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, path)
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "keep" {
			t.Fatalf("symlink target changed: %q, %v", data, err)
		}
	})

	t.Run("regular file", func(t *testing.T) {
		directory := processLifecycleChildDirectory(t, root, "r")
		path := filepath.Join(directory, "s")
		if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := PrepareUnixSocket(path, uid); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("PrepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, path)
	})

	t.Run("permissions", func(t *testing.T) {
		path := processLifecycleStaleSocket(t, processLifecycleChildDirectory(t, root, "o"))
		if err := os.Chmod(path, 0660); err != nil {
			t.Fatal(err)
		}
		if err := PrepareUnixSocket(path, uid); !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("PrepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, path)
	})

	t.Run("owner", func(t *testing.T) {
		path := processLifecycleStaleSocket(t, processLifecycleChildDirectory(t, root, "u"))
		owner := func(info os.FileInfo) (uint32, bool) {
			fileUID, ok := agentFileOwner(info)
			if ok && info.Mode()&os.ModeSocket != 0 {
				return fileUID + 1, true
			}
			return fileUID, ok
		}
		err := prepareUnixSocket(path, uid, owner, dialProcessLifecycleUnix)
		if !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("prepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, path)
	})

	t.Run("timeout", func(t *testing.T) {
		path := processLifecycleStaleSocket(t, processLifecycleChildDirectory(t, root, "d"))
		err := prepareUnixSocket(
			path,
			uid,
			agentFileOwner,
			func(string, time.Duration) (net.Conn, error) {
				return nil, context.DeadlineExceeded
			},
		)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("prepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, path)
	})

	t.Run("other connect error", func(t *testing.T) {
		path := processLifecycleStaleSocket(t, processLifecycleChildDirectory(t, root, "e"))
		err := prepareUnixSocket(
			path,
			uid,
			agentFileOwner,
			func(string, time.Duration) (net.Conn, error) {
				return nil, unix.EACCES
			},
		)
		if !errors.Is(err, unix.EACCES) {
			t.Fatalf("prepareUnixSocket() error = %v", err)
		}
		processLifecycleAssertExists(t, path)
	})

	t.Run("replacement", func(t *testing.T) {
		path := processLifecycleStaleSocket(t, processLifecycleChildDirectory(t, root, "x"))
		err := prepareUnixSocket(
			path,
			uid,
			agentFileOwner,
			func(string, time.Duration) (net.Conn, error) {
				if err := os.Remove(path); err != nil {
					return nil, err
				}
				if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
					return nil, err
				}
				return nil, unix.ECONNREFUSED
			},
		)
		if !errors.Is(err, ErrUnsafeRuntimePath) {
			t.Fatalf("prepareUnixSocket() error = %v", err)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != "replacement" {
			t.Fatalf("replacement changed: %q, %v", data, readErr)
		}
	})
}

func processLifecycleTestDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func processLifecycleChildDirectory(t *testing.T, parent string, name string) string {
	t.Helper()
	directory := filepath.Join(parent, name)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func processLifecycleStaleSocket(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "s")
	processLifecycleCreateStaleSocket(t, path)
	return path
}

func processLifecycleCreateStaleSocket(t *testing.T, path string) {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func processLifecycleAssertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s was removed: %v", path, err)
	}
}
