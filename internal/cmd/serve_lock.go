package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// acquireServeLock creates the lock atomically (O_CREATE|O_EXCL) and writes
// our PID. If the file already exists, the recorded owner is probed: a live
// owner refuses the start, a dead or unreadable one means a stale lock that
// we take over.
func acquireServeLock(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	pid := strconv.Itoa(os.Getpid())
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		_, werr := f.WriteString(pid)
		return werr
	}
	if !os.IsExist(err) {
		return err
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		return rerr
	}
	oldPID, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if convErr == nil && processAlive(oldPID) {
		return fmt.Errorf("another owl-migrate serve is running (pid %d, lock %s); stop it or remove the lock file", oldPID, path)
	}
	return os.WriteFile(path, []byte(pid), 0644)
}

// releaseServeLock removes the lock only if it still contains our own PID,
// so a lock taken over by another instance is never deleted.
func releaseServeLock(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		return
	}
	os.Remove(path)
}

// processAlive probes the PID with signal 0 without delivering anything.
// EPERM means the process exists but belongs to another user.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
