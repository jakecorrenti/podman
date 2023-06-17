//go:build arm64 && darwin

package applehv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/containers/common/pkg/config"
	"github.com/containers/podman/v4/pkg/machine"
	"github.com/containers/podman/v4/pkg/util"
	"github.com/docker/go-units"
	"github.com/sirupsen/logrus"
)

var (
	// vmtype refers to qemu (vs libvirt, krun, etc).
	vmtype = machine.AppleHvVirt
)

// Why isn't this in applehv/config.go where the Virtualization type is defined?
func GetVirtualizationProvider() machine.VirtProvider {
	return &Virtualization{
		artifact:    machine.None,
		compression: machine.Xz,
		format:      machine.Raw,
	}
}

// VfkitHelper describes the use of vfkit: cmdline and endpoint
type VfkitHelper struct {
	Bootloader        string
	Devices           []string
	LogLevel          logrus.Level
	PathToVfkitBinary string
	Endpoint          string
}

type MacMachine struct {
	// ConfigPath is the fully qualified path to the configuration file
	ConfigPath machine.VMFile
	// HostUser contains info about host user
	machine.HostUser
	// ImageConfig describes the bootable image
	machine.ImageConfig
	// Mounts is the list of remote filesystems to mount
	Mounts []machine.Mount
	// Name of VM
	Name string
	// TODO We will need something like this for applehv but until host networking
	// is worked out, we cannot be sure what it looks like.
	/*
		// NetworkVSock is for the user networking
		NetworkHVSock machine.HVSockRegistryEntry
		// ReadySocket tells host when vm is booted
		ReadyHVSock HVSockRegistryEntry
		// ResourceConfig is physical attrs of the VM
	*/
	machine.ResourceConfig
	// SSHConfig for accessing the remote vm
	machine.SSHConfig
	// Starting tells us whether the machine is running or if we have just dialed it to start it
	Starting bool
	// Created contains the original created time instead of querying the file mod time
	Created time.Time
	// LastUp contains the last recorded uptime
	LastUp time.Time
	// The VFKit endpoint where we can interact with the VM
	VfkitHelper
}

func (m *MacMachine) Init(opts machine.InitOptions) (bool, error) {
	var (
		key string
	)
	// Get the path to the directory where vm images are located
	dataDir, err := machine.GetDataDir(machine.AppleHvVirt)
	if err != nil {
		return false, err
	}
	// Acquire the image
	// NOTE: I don't really understand what is going on in this switch statement.
	// What purpose do the FCOS streams play? Is the default case what happens
	// when the user doesn't have the image already downloaded?
	switch opts.ImagePath {
	// Check FCOS (Fedora CoreOS) stream type
	// Testing -- content in this stream is updated regularly
	// Next    -- often used to experiment with new features and to test out
	//            rebases on top opf the next major Fedora version. Content
	//            will eventually filter down into `Testing` and `Stable`
	// Stable  -- most reliable stream with changes only reaching that
	//            stream after spending time in the `Testing` stream
	case machine.Testing.String(), machine.Next.String(), machine.Stable.String(), "":
		// This is used when the user has already pulled the image, and is
		// providing it to podman. Creates a DistributedDownload instance which
		// represents the information that describes the virtual machine image
		g, err := machine.NewGenericDownloader(machine.HyperVVirt, opts.Name, opts.ImagePath)
		if err != nil {
			return false, err
		}

		// Create a VMFile instance that points to the uncompressed virtual
		// machine image
		imagePath, err := machine.NewMachineFile(g.Get().GetLocalUncompressedFile(dataDir), nil)
		if err != nil {
			return false, err
		}
		m.ImagePath = *imagePath
	default:
		// The user has provided an alternate image which can be a file path
		// or URL.
		m.ImageStream = "custom"
		g, err := machine.NewGenericDownloader(vmtype, m.Name, opts.ImagePath)
		if err != nil {
			return false, err
		}
		// Create a new virtual machine file that represents an uncompressed version
		// of the virtual machine image
		imagePath, err := machine.NewMachineFile(g.Get().LocalUncompressedFile, nil)
		if err != nil {
			return false, err
		}
		m.ImagePath = *imagePath
		// Download the image from the URL and decompress it at the image's
		// local path
		if err := machine.DownloadImage(g); err != nil {
			return false, err
		}
	}

	// Store VFKit stuffs
	vfhelper, err := newVfkitHelper(m.Name, defaultVFKitEndpoint, m.ImagePath.GetPath())
	if err != nil {
		return false, err
	}
	m.VfkitHelper = *vfhelper

	// Get the .ssh directory path
	m.IdentityPath = util.GetIdentityPath(m.Name)
	m.Rootful = opts.Rootful
	m.RemoteUsername = opts.Username

	m.UID = os.Getuid()

	// TODO A final decision on networking implementation will need to be made
	// prior to this working
	//sshPort, err := utils.GetRandomPort()
	//if err != nil {
	//	return false, err
	//}
	m.Port = 22

	// Ignition is the utility used by Fedora CoreOS and RHEL CoreOS to
	// manipulate disks during the initramfs. This includes partitioning disks,
	// formatting partitions, writing files (regular files, systemd units, etc.),
	// and configuring users. On first boot, Ignition reads its configuration
	// from a source of truth (remote URL, network metadata service, hypervisor
	// bridge, etc.) and applies the configuration
	// NOTE: need to re-look at this. not too sure what is oging on.
	if len(opts.IgnitionPath) < 1 {
		// TODO localhost needs to be restored here
		uri := machine.SSHRemoteConnection.MakeSSHURL("192.168.64.2", fmt.Sprintf("/run/user/%d/podman/podman.sock", m.UID), strconv.Itoa(m.Port), m.RemoteUsername)
		uriRoot := machine.SSHRemoteConnection.MakeSSHURL("localhost", "/run/podman/podman.sock", strconv.Itoa(m.Port), "root")
		identity := m.IdentityPath

		uris := []url.URL{uri, uriRoot}
		names := []string{m.Name, m.Name + "-root"}

		// The first connection defined when connections is empty will become the default
		// regardless of IsDefault, so order according to rootful
		if opts.Rootful {
			uris[0], names[0], uris[1], names[1] = uris[1], names[1], uris[0], names[0]
		}

		for i := 0; i < 2; i++ {
			if err := machine.AddConnection(&uris[i], names[i], identity, opts.IsDefault && i == 0); err != nil {
				return false, err
			}
		}
	} else {
		fmt.Println("An ignition path was provided.  No SSH connection was added to Podman")
	}

	// TODO resize disk

	// Write the virtual machine config to the JSON config file
	if err := m.writeConfig(); err != nil {
		return false, err
	}

	if len(opts.IgnitionPath) < 1 {
		var err error
		// make public and private ssh keys for interacting with the virtual machine
		key, err = machine.CreateSSHKeys(m.IdentityPath)
		if err != nil {
			return false, err
		}
	}

	// If the user provided an ignition file, read and apply the configuration
	// NOTE: This conditional check could have been put before the one above,
	// and then the one above could have just been executed without the conditional
	if len(opts.IgnitionPath) > 0 {
		inputIgnition, err := os.ReadFile(opts.IgnitionPath)
		if err != nil {
			return false, err
		}
		return false, os.WriteFile(m.IgnitionFile.GetPath(), inputIgnition, 0644)
	}
	// TODO Ignition stuff goes here
	// Write the ignition file
	ign := machine.DynamicIgnition{
		Name:      opts.Username,
		Key:       key,
		VMName:    m.Name,
		VMType:    machine.AppleHvVirt,
		TimeZone:  opts.TimeZone,
		WritePath: m.IgnitionFile.GetPath(),
		UID:       m.UID,
		Rootful:   m.Rootful,
	}

	// Generate and write the ignition file
	if err := ign.GenerateIgnitionConfig(); err != nil {
		return false, err
	}
	if err := ign.Write(); err != nil {
		return false, err
	}

	return true, nil
}

func (m *MacMachine) Inspect() (*machine.InspectInfo, error) {
	// Get the state of the current virtual machine
	vmState, err := m.state()
	if err != nil {
		return nil, err
	}
	ii := machine.InspectInfo{
		ConfigPath: m.ConfigPath,
		ConnectionInfo: machine.ConnectionConfig{
			PodmanSocket: nil,
			PodmanPipe:   nil,
		},
		Created: m.Created,
		Image: machine.ImageConfig{
			IgnitionFile: m.IgnitionFile,
			ImageStream:  m.ImageStream,
			ImagePath:    m.ImagePath,
		},
		LastUp: m.LastUp,
		Name:   m.Name,
		Resources: machine.ResourceConfig{
			CPUs:     m.CPUs,
			DiskSize: m.DiskSize,
			Memory:   m.Memory,
		},
		SSHConfig: m.SSHConfig,
		State:     vmState,
	}
	return &ii, nil
}

func (m *MacMachine) Remove(name string, opts machine.RemoveOptions) (string, func() error, error) {
	var (
		files []string
	)

	vmState, err := m.state()
	if err != nil {
		return "", nil, err
	}
	if vmState == machine.Running {
		if !opts.Force {
			return "", nil, fmt.Errorf("invalid state: %s is running", m.Name)
		}
		// If the user wants to force remove the virtual machine, it needs to
		// be stopped first
		if err := m.stop(true, true); err != nil {
			return "", nil, err
		}
	}

	if !opts.SaveKeys {
		files = append(files, m.IdentityPath, m.IdentityPath+".pub")
	}
	if !opts.SaveIgnition {
		files = append(files, m.IgnitionFile.GetPath())
	}

	if !opts.SaveImage {
		files = append(files, m.ImagePath.GetPath())
	}

	files = append(files, m.ConfigPath.GetPath())

	confirmationMessage := "\nThe following files will be deleted:\n\n"
	// List the files that are going to be deleted by removing the virtual machine
	for _, msg := range files {
		confirmationMessage += msg + "\n"
	}

	confirmationMessage += "\n"
	return confirmationMessage, func() error {
		// Remove the files the user didn't specify they wanted to keep
		for _, f := range files {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				logrus.Error(err)
			}
		}
		// NOTE: this is a variadic function, why not just pass m.Name and
		// (m.Name + "-root") to `machine.RemoveConnections()?
		if err := machine.RemoveConnections(m.Name); err != nil {
			logrus.Error(err)
		}
		if err := machine.RemoveConnections(m.Name + "-root"); err != nil {
			logrus.Error(err)
		}

		// TODO We will need something like this for applehv too i think
		/*
			// Remove the HVSOCK for networking
			if err := m.NetworkHVSock.Remove(); err != nil {
				logrus.Errorf("unable to remove registry entry for %s: %q", m.NetworkHVSock.KeyName, err)
			}

			// Remove the HVSOCK for events
			if err := m.ReadyHVSock.Remove(); err != nil {
				logrus.Errorf("unable to remove registry entry for %s: %q", m.NetworkHVSock.KeyName, err)
			}
		*/
		return nil
	}, nil
}

// Marshal the machine instance into a JSON string and write that string to the
// JSON virtual machine config file
func (m *MacMachine) writeConfig() error {
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.ConfigPath.Path, b, 0644)
}

// What is the reasoning behind the return type?
func (m *MacMachine) Set(name string, opts machine.SetOptions) ([]error, error) {
	var setErrors []error
	vmState, err := m.State(false)
	if err != nil {
		return nil, err
	}
	// Cannot set any virtual machine settings to a non-running machine
	if vmState != machine.Stopped {
		return nil, machine.ErrWrongState
	}
	// Check if user wants to change number of CPUs
	if cpus := opts.CPUs; cpus != nil {
		m.CPUs = *cpus
	}
	// Check if user wants to change amount of memory
	if mem := opts.Memory; mem != nil {
		m.Memory = *mem
	}
	// Check if user wants to expand the disk size of the virtual machine
	if newSize := opts.DiskSize; newSize != nil {
		if *newSize < m.DiskSize {
			setErrors = append(setErrors, errors.New("new disk size smaller than existing disk size: cannot shrink disk size"))
		} else {
			m.DiskSize = *newSize
		}
	}

	// Write the machine config to the filesystem
	err = m.writeConfig()
	setErrors = append(setErrors, err)
	switch len(setErrors) {
	case 0:
		return setErrors, nil
	case 1:
		return nil, setErrors[0]
	default:
		// Number of errors is 2 or more
		lastErr := setErrors[len(setErrors)-1]
		return setErrors[:len(setErrors)-1], lastErr
	}
}

func (m *MacMachine) SSH(name string, opts machine.SSHOptions) error {
	st, err := m.State(false)
	if err != nil {
		return err
	}
	// Can only ssh into running virtual machine
	if st != machine.Running {
		return fmt.Errorf("vm %q is not running", m.Name)
	}
	username := opts.Username
	if username == "" {
		username = m.RemoteUsername
	}
	// TODO when host networking is figured out, we need to switch this back to
	// machine.commonssh
	return AppleHVSSH(username, m.IdentityPath, m.Name, m.Port, opts.Args)
}

func (m *MacMachine) Start(name string, opts machine.StartOptions) error {
	st, err := m.State(false)
	if err != nil {
		return err
	}
	if st == machine.Running {
		return machine.ErrVMAlreadyRunning
	}
	// TODO Once we decide how to do networking, we can enable the following lines
	// for API forwarding, etc
	//_, _, err = m.startHostNetworking()
	//if err != nil {
	//	return err
	//}
	// To start the VM, we need to call vfkit
	// TODO need to hold the start command until fcos tells us it is started
	return m.VfkitHelper.startVfkit(m)
}

// NOTE: can we get rid of the parameter? completely unused. API compat?
func (m *MacMachine) State(_ bool) (machine.Status, error) {
	vmStatus, err := m.VfkitHelper.state()
	if err != nil {
		return "", err
	}
	return vmStatus, nil
}

func (m *MacMachine) Stop(name string, opts machine.StopOptions) error {
	vmState, err := m.State(false)
	if err != nil {
		return err
	}
	if vmState != machine.Running {
		return machine.ErrWrongState
	}
	return m.VfkitHelper.stop(false, true)
}

// getVMConfigPath is a simple wrapper for getting the fully-qualified
// path of the vm json config file.  It should be used to get conformity
func getVMConfigPath(configDir, vmName string) string {
	return filepath.Join(configDir, fmt.Sprintf("%s.json", vmName))
}

func (m *MacMachine) loadFromFile() (*MacMachine, error) {
	// Is this possible? not passing a name resorts to podman-machine-default
	// and you can't change the name using set
	if len(m.Name) < 1 {
		return nil, errors.New("encountered machine with no name")
	}

	// Get the path to the virtual machine's JSON configuration file
	jsonPath, err := m.jsonConfigPath()
	if err != nil {
		return nil, err
	}
	mm := MacMachine{}

	if err := loadMacMachineFromJSON(jsonPath, &mm); err != nil {
		return nil, err
	}
	return &mm, nil
}

// Read the JSON config file and Unmarshal it, putting the contents into the
// MacMachine instance passed in
func loadMacMachineFromJSON(fqConfigPath string, macMachine *MacMachine) error {
	b, err := os.ReadFile(fqConfigPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%q: %w", fqConfigPath, machine.ErrNoSuchVM)
		}
		return err
	}
	return json.Unmarshal(b, macMachine)
}

func (m *MacMachine) jsonConfigPath() (string, error) {
	configDir, err := machine.GetConfDir(machine.AppleHvVirt)
	if err != nil {
		return "", err
	}
	return getVMConfigPath(configDir, m.Name), nil
}

func getVMInfos() ([]*machine.ListResponse, error) {
	vmConfigDir, err := machine.GetConfDir(vmtype)
	if err != nil {
		return nil, err
	}

	var listed []*machine.ListResponse

	// Iterate through each of the files in the virtual machine's configuration
	// file directory
	if err = filepath.WalkDir(vmConfigDir, func(path string, d fs.DirEntry, err error) error {
		vm := new(MacMachine)
		if strings.HasSuffix(d.Name(), ".json") {
			fullPath := filepath.Join(vmConfigDir, d.Name())
			b, err := os.ReadFile(fullPath)
			if err != nil {
				return err
			}
			// put contents of the config file into the virtual machine instance
			err = json.Unmarshal(b, vm)
			if err != nil {
				return err
			}
			listEntry := new(machine.ListResponse)

			listEntry.Name = vm.Name
			listEntry.Stream = vm.ImageStream
			listEntry.VMType = machine.AppleHvVirt.String()
			listEntry.CPUs = vm.CPUs
			listEntry.Memory = vm.Memory * units.MiB
			listEntry.DiskSize = vm.DiskSize * units.GiB
			listEntry.Port = vm.Port
			listEntry.RemoteUsername = vm.RemoteUsername
			listEntry.IdentityPath = vm.IdentityPath
			listEntry.CreatedAt = vm.Created
			listEntry.Starting = vm.Starting

			if listEntry.CreatedAt.IsZero() {
				listEntry.CreatedAt = time.Now()
				vm.Created = time.Now()
				if err := vm.writeConfig(); err != nil {
					return err
				}
			}

			vmState, err := vm.State(false)
			if err != nil {
				return err
			}
			listEntry.Running = vmState == machine.Running

			if !vm.LastUp.IsZero() { // this means we have already written a time to the config
				listEntry.LastUp = vm.LastUp
			} else { // else we just created the machine AKA last up = created time
				listEntry.LastUp = vm.Created
				vm.LastUp = listEntry.LastUp
				if err := vm.writeConfig(); err != nil {
					return err
				}
			}

			listed = append(listed, listEntry)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return listed, err
}

// Currently unused since networking hasn't been figured out for applehv yet
func (m *MacMachine) startHostNetworking() (string, machine.APIForwardingState, error) {
	var (
		forwardSock string
		state       machine.APIForwardingState
	)
	// Get the default contents for a configuration file
	cfg, err := config.Default()
	if err != nil {
		return "", machine.NoForwarding, err
	}

	// attributes that will be applied to a new process
	// NOTE: the following lines are also in vfkit.go, which means this could
	// probably get pulled out to a shared function to make this function
	// easier to read
	attr := new(os.ProcAttr)
	dnr, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0755)
	if err != nil {
		return "", machine.NoForwarding, err
	}
	dnw, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0755)
	if err != nil {
		return "", machine.NoForwarding, err
	}

	defer func() {
		if err := dnr.Close(); err != nil {
			logrus.Error(err)
		}
	}()
	defer func() {
		if err := dnw.Close(); err != nil {
			logrus.Error(err)
		}
	}()

	gvproxy, err := cfg.FindHelperBinary("gvproxy", false)
	if err != nil {
		return "", 0, err
	}

    // why are we putting the write only DevNull device in the attribute files
    // twice?
	attr.Files = []*os.File{dnr, dnw, dnw}
	cmd := []string{gvproxy}
	// Add the ssh port
	cmd = append(cmd, []string{"-ssh-port", fmt.Sprintf("%d", m.Port)}...)
	// TODO Fix when host networking is setup
	//cmd = append(cmd, []string{"-listen", fmt.Sprintf("vsock://%s", m.NetworkHVSock.KeyName)}...)

	cmd, forwardSock, state = m.setupAPIForwarding(cmd)
	if logrus.GetLevel() == logrus.DebugLevel {
		cmd = append(cmd, "--debug")
		fmt.Println(cmd)
	}
	_, err = os.StartProcess(cmd[0], cmd, attr)
	if err != nil {
		return "", 0, fmt.Errorf("unable to execute: %q: %w", cmd, err)
	}
	return forwardSock, state, nil
}

// AppleHVSSH is a temporary function for applehv until we decide how the networking will work
// for certain.
func AppleHVSSH(username, identityPath, name string, sshPort int, inputArgs []string) error {
	sshDestination := username + "@192.168.64.2"
	port := strconv.Itoa(sshPort)

	args := []string{"-i", identityPath, "-p", port, sshDestination,
		"-o", "StrictHostKeyChecking=no", "-o", "LogLevel=ERROR", "-o", "SetEnv=LC_ALL="}
	if len(inputArgs) > 0 {
		args = append(args, inputArgs...)
	} else {
		fmt.Printf("Connecting to vm %s. To close connection, use `~.` or `exit`\n", name)
	}

	cmd := exec.Command("ssh", args...)
	logrus.Debugf("Executing: ssh %v\n", args)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd.Run()
}

// Return the command that can be run to send something from the virtual 
// machine's socket to the podman socket on the local machine?
func (m *MacMachine) setupAPIForwarding(cmd []string) ([]string, string, machine.APIForwardingState) {
    // path to the virtual machine's podman socket 
	socket, err := m.forwardSocketPath()
	if err != nil {
		return cmd, "", machine.NoForwarding
	}

	destSock := fmt.Sprintf("/run/user/%d/podman/podman.sock", m.UID)
	forwardUser := "core"

	if m.Rootful {
		destSock = "/run/podman/podman.sock"
		forwardUser = "root"
	}

	cmd = append(cmd, []string{"-forward-sock", socket.GetPath()}...)
	cmd = append(cmd, []string{"-forward-dest", destSock}...)
	cmd = append(cmd, []string{"-forward-user", forwardUser}...)
	cmd = append(cmd, []string{"-forward-identity", m.IdentityPath}...)

	return cmd, "", machine.MachineLocal
}

// This function and the `forwardSocketPath` functions have the same functionality, 
// the return type is just different (string vs. machine.VMFile)
func (m *MacMachine) dockerSock() (string, error) {
	dd, err := machine.GetDataDir(machine.AppleHvVirt)
	if err != nil {
		return "", err
	}
	return filepath.Join(dd, "podman.sock"), nil
}

func (m *MacMachine) forwardSocketPath() (*machine.VMFile, error) {
	sockName := "podman.sock"
	path, err := machine.GetDataDir(machine.AppleHvVirt)
	if err != nil {
		return nil, fmt.Errorf("Resolving data dir: %s", err.Error())
	}
	return machine.NewMachineFile(filepath.Join(path, sockName), &sockName)
}
