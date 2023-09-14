package qemu

import (
	"fmt"
	"strconv"

	"github.com/containers/podman/v4/pkg/machine"
)

type FirmwareConfigDevice struct {
	Name         string
	IgnitionFile machine.VMFile
	ConfigString string
}

func (f *FirmwareConfigDevice) ToCmdline() []string {
	args := []string{"-fw_cfg"}

	vars := ""
	if f.Name != "" {
		vars += "name=" + f.Name
	}

	ignPath := f.IgnitionFile.GetPath()
	if ignPath != "" {
		if len(vars) > 0 {
			vars += ","
		}
		vars += "file=" + ignPath
	}

	if f.ConfigString != "" {
		if len(vars) > 0 {
			vars += ","
		}
		vars += "string=" + f.ConfigString
	}

	args = append(args, vars)
	return args
}

type QmpMonitor struct {
	Monitor Monitor
	Server  bool
	Wait    bool
}

func (q *QmpMonitor) GetServerStatus() string {
	if q.Server {
		return "on"
	}
	return "off"
}

func (q *QmpMonitor) GetWaitStatus() string {
	if q.Wait {
		return "on"
	}
	return "off"
}

func (q *QmpMonitor) ToCmdline() []string {
	args := []string{"-qmp"}
	vars := ""

	if q.Monitor.Network != "" && q.Monitor.Address.GetPath() != "" {
		vars += q.Monitor.Network + ":" + q.Monitor.Address.GetPath()
	}

	if vars != "" {
		vars += ","
	}
	vars += "server=" + q.GetServerStatus()

	if vars != "" {
		vars += ","
	}
	vars += "wait=" + q.GetWaitStatus()
	args = append(args, vars)
	return args
}

type Socket struct {
	ID             string
	FileDescriptor int
}

func (s *Socket) ToCmdline() string {
	vars := "socket"

	// if s.ID != "" {
	// 	vars += ",id=" + s.ID
	// }
	vars += ",id=vlan"

	if s.FileDescriptor >= 0 {
		vars += fmt.Sprintf(",fd=%d", s.FileDescriptor)
	}

	return vars
}

type VirtioNet struct {
	NetDevice  string
	MacAddress string
}

func (v *VirtioNet) ToCmdline() string {
	vars := "virtio-net-pci"

	if v.NetDevice != "" {
		vars += ",netdev=" + v.NetDevice
	}

	if v.MacAddress != "" {
		vars += ",mac=" + v.MacAddress
	}

	return vars
}

type Network struct {
	Socket
	VirtioNet
}

func (n *Network) ToCmdline() []string {
	return []string{"-netdev", n.Socket.ToCmdline(), "-device", n.VirtioNet.ToCmdline()}
}

type CharDev struct {
	SocketPath machine.VMFile
	Server     bool
	Wait       bool
	ID         string
}

func (c *CharDev) GetServerStatus() string {
	if c.Server {
		return "on"
	}
	return "off"
}

func (c *CharDev) GetWaitStatus() string {
	if c.Wait {
		return "on"
	}
	return "off"
}

func (c *CharDev) ToCmdline() []string {
	vars := ""
	if c.SocketPath.GetPath() != "" {
		vars += "socket,path=" + c.SocketPath.GetPath()
	}

	if vars != "" {
		vars += ","
	}
	// vars += "server=" + c.GetServerStatus() + ",wait=" + c.GetWaitStatus()
	vars += "server=" + "on" + ",wait=" + c.GetWaitStatus()

	if vars != "" {
		vars += ","
	}

	// if c.ID != "" {
	// 	vars += ",id=" + c.ID
	// }
	return []string{"-chardev", vars + fmt.Sprintf("id=a%s_ready", "podman-machine-default")}
}

type VirtSerialPort struct {
	Name    string
	CharDev string
}

func (v *VirtSerialPort) ToCmdline() []string {
	vars := "virtserialport"

	if v.CharDev != "" {
		vars += ",chardev=" + v.CharDev
	}

	if v.Name != "" {
		vars += ",name=" + v.Name
	}

	return []string{"-device", vars}
}

type SerialPort struct {
	PidFile machine.VMFile
	CharDev
	VirtSerialPort
}

func (s *SerialPort) ToCmdline() []string {
	args := []string{"-device", "virtio-serial"}

	args = append(args, s.CharDev.ToCmdline()...)
	args = append(args, s.VirtSerialPort.ToCmdline()...)
	args = append(args, []string{"-pidfile", s.PidFile.GetPath()}...)

	return args
}

type Virtfs struct {
	Path          machine.VMFile
	MountTag      string
	SecurityModel string
	ReadOnly      bool
}

func (v *Virtfs) ToCmdline() []string {
	vars := "local"

	if v.Path.GetPath() != "" {
		vars += ",path=" + v.Path.GetPath()
	}

	if v.MountTag != "" {
		vars += ",mount_tag=" + v.MountTag
	}

	if v.SecurityModel != "" {
		vars += ",security_model=" + v.SecurityModel
	}

	if v.ReadOnly {
		vars += ",readonly"
	}

	return []string{"-virtfs", vars}
}

type QemuCmd struct {
	Memory          int
	CPUs            int
	CPU             string
	Bios            string
	BootableImage   string
	Display         bool
	Accelerator     string
	Machine         string
	Mounts          []Virtfs
	FirmwareConfigs []FirmwareConfigDevice

	QmpMonitor
	Network
	SerialPort
}

func (q *QemuCmd) ToCmdline() []string {
	args := []string{}
	// memory
	args = append(args, "-m", strconv.Itoa(q.Memory))
	// cpus
	args = append(args, "-smp", strconv.Itoa(q.CPUs))
	// ignition file
	for _, fwCfgDev := range q.FirmwareConfigs {
		args = append(args, fwCfgDev.ToCmdline()...)
	}
	args = append(args, q.QmpMonitor.ToCmdline()...)
	// network
	args = append(args, q.Network.ToCmdline()...)
	// serial port
	args = append(args, q.SerialPort.ToCmdline()...)
	// mounts
	for _, mount := range q.Mounts {
		args = append(args, mount.ToCmdline()...)
	}
	//display
	if q.Display {
		args = append(args, "-display", "true")
	} else {
		args = append(args, "-display", "none")
	}
	// arch specific options
	args = append(args, "-accel", q.Accelerator)
	args = append(args, "-cpu", q.CPU)
	// bootable image
	args = append(args, "-drive", "if=virtio,file="+q.BootableImage)
	// bios
	if q.Bios != "" {
		args = append(args, "-bios", q.Bios)
	}
	// machine
	args = append(args, "-M", q.Machine)
	return args
}
