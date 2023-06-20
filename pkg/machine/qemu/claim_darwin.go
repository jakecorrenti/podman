package qemu

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

// Couldn't this just be a constant?
func dockerClaimSupported() bool {
	return true
}

// If the podman mac helper sock exists, then it's been installed
func dockerClaimHelperInstalled() bool {
	u, err := user.Current()
	if err != nil {
		return false
	}

	labelName := fmt.Sprintf("com.github.containers.podman.helper-%s", u.Username)
	fileName := filepath.Join("/Library", "LaunchDaemons", labelName+".plist")
	info, err := os.Stat(fileName)
	return err == nil && info.Mode().IsRegular()
}

func claimDockerSock() bool {
	u, err := user.Current()
	if err != nil {
		return false
	}

	helperSock := fmt.Sprintf("/var/run/podman-helper-%s.socket", u.Username)
	// connect to the socket with a 5 second timeout
	con, err := net.DialTimeout("unix", helperSock, time.Second*5)
	if err != nil {
		return false
	}
	// set the deadline to write to the socket for 5 seconds from now
	_ = con.SetWriteDeadline(time.Now().Add(time.Second * 5))
	_, err = fmt.Fprintln(con, "GO")
	if err != nil {
		return false
	}

	// set the deadline to read from the socket for 5 seconds from now
	_ = con.SetReadDeadline(time.Now().Add(time.Second * 5))
	read, err := io.ReadAll(con)

	return err == nil && string(read) == "OK"
}

// return the path to the podman mac helper binary
func findClaimHelper() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}

	return filepath.Join(filepath.Dir(exe), "podman-mac-helper")
}
