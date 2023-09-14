package qemu

import (
	"os"
	"path/filepath"
)

var (
	QemuCommand = "qemu-system-aarch64"
)

func (v *MachineVM) addArchOptions() {
    v.CmdLine.Accelerator = "kvm"
    v.CmdLine.CPU = "host"
    v.CmdLine.Machine = "virt,gic-version=max"
    v.CmdLine.Bios = getQemuUefiFile("QEMU_EFI.fd")
}

func (v *MachineVM) prepare() error {
	return nil
}

func (v *MachineVM) archRemovalFiles() []string {
	return []string{}
}

func getQemuUefiFile(name string) string {
	dirs := []string{
		"/usr/share/qemu-efi-aarch64",
		"/usr/share/edk2/aarch64",
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err == nil {
			return filepath.Join(dir, name)
		}
	}
	return name
}
