package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// acquireServeLock writes our PID to path, failing if another live process
// holds the lock. Stale locks (unreadable or dead PID) are taken over.
func acquireServeLock(path string) error {
	if data, err := os.ReadFile(path); err == nil {
		pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if convErr == nil && processAlive(pid) {
			return fmt.Errorf("another owl-migrate serve is running (pid %d, lock %s); stop it or remove the lock file", pid, path)
		}
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func releaseServeLock(path string) {
	os.Remove(path)
}

// processAlive probes the PID with signal 0 without delivering anything.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
