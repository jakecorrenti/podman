//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd
// +build darwin dragonfly freebsd linux netbsd openbsd

package qemu

import (
	"bytes"
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func isProcessAlive(pid int) bool {
	// if the signal is 0, then no signal is sent, but error checking is still
	// performed. This can be used to check for the existance of a process ID
	//  or GID
	err := unix.Kill(pid, syscall.Signal(0))
	// if we get a permissions error, then that means the process does exist
	if err == nil || err == unix.EPERM {
		return true
	}
	return false
	// could just rewrite this as return (err == nil || err == unix.EPERM)
}

func checkProcessStatus(processHint string, pid int, stderrBuf *bytes.Buffer) error {
	var status syscall.WaitStatus
	pid, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err != nil {
		return fmt.Errorf("failed to read qem%su process status: %w", processHint, err)
	}
	if pid > 0 {
		// child exited
		return fmt.Errorf("%s exited unexpectedly with exit code %d, stderr: %s", processHint, status.ExitStatus(), stderrBuf.String())
	}
	return nil
}

func pathsFromVolume(volume string) []string {
	return strings.SplitN(volume, ":", 3)
}

func extractTargetPath(paths []string) string {
	if len(paths) > 1 {
		return paths[1]
	}
	return paths[0]
}
