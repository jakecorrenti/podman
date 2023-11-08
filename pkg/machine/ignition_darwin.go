//go:build darwin
// +build darwin

package machine

import (
	"os"
	"strings"
)

const AppleHVReadyUnit = `[Unit]
Requires=dev-virtio\\x2dports-%s.device
After=remove-moby.service sshd.socket sshd.service
OnFailure=emergency.target
OnFailureJobMode=isolate
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c '/usr/bin/echo Ready | socat - VSOCK-CONNECT:2:1025'
[Install]
RequiredBy=default.target
`

func getLocalTimeZone() (string, error) {
	tzPath, err := os.Readlink("/etc/localtime")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(tzPath, "/var/db/timezone/zoneinfo"), nil
}
