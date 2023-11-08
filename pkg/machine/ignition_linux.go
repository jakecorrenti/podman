package machine

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

const QemuReadyUnit = `[Unit]
Requires=dev-virtio\\x2dports-%s.device
After=remove-moby.service sshd.socket sshd.service
After=systemd-user-sessions.service
OnFailure=emergency.target
OnFailureJobMode=isolate
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c '/usr/bin/echo Ready >/dev/%s'
[Install]
RequiredBy=default.target
`

func getLocalTimeZone() (string, error) {
	output, err := exec.Command("timedatectl", "show", "--property=Timezone").Output()
	if errors.Is(err, exec.ErrNotFound) {
		output, err = os.ReadFile("/etc/timezone")
	}
	if err != nil {
		return "", err
	}
	// Remove prepended field and the newline
	return strings.TrimPrefix(strings.TrimSuffix(string(output), "\n"), "Timezone="), nil
}
