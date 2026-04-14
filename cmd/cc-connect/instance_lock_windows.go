//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateFileW  = kernel32.NewProc("CreateFileW")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
)

const (
	GENERIC_READ    = 0x80000000
	GENERIC_WRITE   = 0x40000000
	FILE_SHARE_READ = 0x00000001
	CREATE_ALWAYS   = 2
	OPEN_ALWAYS     = 4
	FILE_ATTRIBUTE_NORMAL = 0x80
	LOCKFILE_EXCLUSIVE_LOCK = 0x00000002
	LOCKFILE_FAIL_IMMEDIATELY = 0x00000001
	ERROR_SHARING_VIOLATION = 32
)

// InstanceLock provides a file-based exclusive lock to prevent multiple
// cc-connect instances with the same config from running simultaneously.
// On Windows, this uses LockFileEx for proper cross-process locking.
type InstanceLock struct {
	handle  syscall.Handle
	path    string
	acquired bool
}

// AcquireInstanceLock attempts to acquire an exclusive lock for the given config path.
// If another instance is already running with the same config, it returns an error
// containing the PID of the existing instance.
func AcquireInstanceLock(configPath string) (*InstanceLock, error) {
	configDir := filepath.Dir(configPath)
	configBase := filepath.Base(configPath)
	lockName := fmt.Sprintf(".%s.lock", configBase)
	lockPath := filepath.Join(configDir, lockName)

	// Ensure directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create config directory: %w", err)
	}

	lockPathPtr, err := syscall.UTF16PtrFromString(lockPath)
	if err != nil {
		return nil, fmt.Errorf("cannot convert lock path: %w", err)
	}

	// Create/open the file with shared read access so we can read PID
	handle, _, err := procCreateFileW.Call(
		uintptr(unsafe.Pointer(lockPathPtr)),
		GENERIC_READ|GENERIC_WRITE,
		FILE_SHARE_READ,
		0,
		OPEN_ALWAYS,
		FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if syscall.Handle(handle) == syscall.InvalidHandle {
		if errno, ok := err.(syscall.Errno); ok && errno == ERROR_SHARING_VIOLATION {
			// File is locked by another process
			pid := readPIDFromLockFile(lockPath)
			if pid > 0 {
				return nil, fmt.Errorf("another cc-connect instance is already running (PID %d) with config %s", pid, configPath)
			}
			return nil, fmt.Errorf("another cc-connect instance is already running with config %s", configPath)
		}
		return nil, fmt.Errorf("cannot open lock file: %w", err)
	}

	// Try to acquire exclusive lock (non-blocking)
	// LockFileEx: hFile, dwFlags, dwReserved, nNumberOfBytesToLockLow, nNumberOfBytesToLockHigh, lpOverlapped
	var overlapped syscall.Overlapped
	ret, _, err := procLockFileEx.Call(
		uintptr(handle),
		LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1, // lock 1 byte
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret == 0 {
		procCloseHandle.Call(uintptr(handle))
		// Lock is held by another process
		pid := readPIDFromLockFile(lockPath)
		if pid > 0 {
			return nil, fmt.Errorf("another cc-connect instance is already running (PID %d) with config %s", pid, configPath)
		}
		return nil, fmt.Errorf("another cc-connect instance is already running with config %s", configPath)
	}

	// Write our PID to the lock file for diagnostics
	pid := os.Getpid()
	_ = syscall.WriteFile(syscall.Handle(handle), []byte(fmt.Sprintf("%d\n", pid)), nil, nil)

	return &InstanceLock{
		handle:   syscall.Handle(handle),
		path:     lockPath,
		acquired: true,
	}, nil
}

// Release releases the instance lock. It is safe to call multiple times.
func (l *InstanceLock) Release() {
	if l == nil || !l.acquired {
		return
	}

	if l.handle != syscall.InvalidHandle {
		// Unlock the file
		var overlapped syscall.Overlapped
		procUnlockFileEx.Call(
			uintptr(l.handle),
			0,
			1,
			0,
			uintptr(unsafe.Pointer(&overlapped)),
		)
		// Close the handle
		procCloseHandle.Call(uintptr(l.handle))
		l.handle = syscall.InvalidHandle
	}

	l.acquired = false
}

// Path returns the path to the lock file.
func (l *InstanceLock) Path() string {
	return l.path
}

// readPIDFromLockFile attempts to read a PID from a lock file.
// Returns 0 if the PID cannot be determined.
func readPIDFromLockFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0
	}

	return pid
}

// KillExistingInstance attempts to kill the process holding the lock for the given config.
// Returns true if a process was killed, false otherwise.
func KillExistingInstance(configPath string) bool {
	configDir := filepath.Dir(configPath)
	configBase := filepath.Base(configPath)
	lockName := fmt.Sprintf(".%s.lock", configBase)
	lockPath := filepath.Join(configDir, lockName)

	pid := readPIDFromLockFile(lockPath)
	if pid <= 0 {
		return false
	}

	// On Windows, we need to use syscall.OpenProcess to get a handle
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)

	// Terminate the process
	if err := syscall.TerminateProcess(handle, 1); err != nil {
		return false
	}

	return true
}