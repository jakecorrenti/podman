//go:build arm64 && darwin

package applehv

import (
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/containers/podman/v4/pkg/machine"
	"github.com/docker/go-units"
	"golang.org/x/sys/unix"
)

const (
	defaultVFKitEndpoint = "http://localhost:8081"
)

// This is duplicated for each virtualization interface
type Virtualization struct {
	artifact    machine.Artifact
	compression machine.ImageCompression
	format      machine.ImageFormat
}

type MMHardwareConfig struct {
	CPUs     uint16
	DiskPath string
	DiskSize uint64
	Memory   int32
}

func (v Virtualization) Artifact() machine.Artifact {
	return machine.Metal
}

// Check to see if there is already an active virtual machine on the system
func (v Virtualization) CheckExclusiveActiveVM() (bool, string, error) {
	fsVms, err := getVMInfos()
	if err != nil {
		return false, "", err
	}
	for _, vm := range fsVms {
		if vm.Running || vm.Starting {
			return true, vm.Name, nil
		}
	}

	return false, "", nil
}

func (v Virtualization) Compression() machine.ImageCompression {
	return v.compression
}

func (v Virtualization) Format() machine.ImageFormat {
	return v.format
}

// Try and load the specified virtual machine's JSON configuration from the
// configuration directory. If this succeeds, then we have a valid name.
func (v Virtualization) IsValidVMName(name string) (bool, error) {
	mm := MacMachine{Name: name}
	// Get the directory where config files are located
	configDir, err := machine.GetConfDir(machine.AppleHvVirt)
	if err != nil {
		return false, err
	}
	// Check to see if can successfully load the machine from the configuration file.
	// If it can be loaded, we have a valid machine name, otherwise we do not.
	if err := loadMacMachineFromJSON(configDir, &mm); err != nil {
		return false, err
	}
	return true, nil
}

func (v Virtualization) List(opts machine.ListOptions) ([]*machine.ListResponse, error) {
	var (
		response []*machine.ListResponse
	)

	// Load the machine from the local configuration file in the configuration
	// directory. The naming of this function is confusing when compared to
	// `loadMacMachineFromJSON`. My first thought was this was loading a single
	// virtual machine from the config file, not getting the list of all mac
	// machines. Granted the context of this gives the correct answer away, but
	// the name of the function alone feels a little misleading.
	mms, err := v.loadFromLocalJson()
	if err != nil {
		return nil, err
	}

	for _, mm := range mms {
        // use vfkit to get the virtual machine's state
		vmState, err := mm.state()
		if err != nil {
			if errors.Is(err, unix.ECONNREFUSED) {
                // if the vfkit service is not responding, assume the service is
                // not running
				vmState = machine.Stopped
			} else {
				return nil, err
			}
		}

		mlr := machine.ListResponse{
			Name:           mm.Name,
			CreatedAt:      mm.Created,
			LastUp:         mm.LastUp,
			Running:        vmState == machine.Running,
			Starting:       vmState == machine.Starting,
			Stream:         mm.ImageStream,
			VMType:         machine.AppleHvVirt.String(),
			CPUs:           mm.CPUs,
			Memory:         mm.Memory * units.MiB,
			DiskSize:       mm.DiskSize * units.GiB,
			Port:           mm.Port,
			RemoteUsername: mm.RemoteUsername,
			IdentityPath:   mm.IdentityPath,
		}
		response = append(response, &mlr)
	}
	return response, nil
}

func (v Virtualization) LoadVMByName(name string) (machine.VM, error) {
    // need to create a new instance of a machine with just the name because the
    // name is needed to locate the JSON configuration file
	m := MacMachine{Name: name}
	return m.loadFromFile()
}

func (v Virtualization) NewMachine(opts machine.InitOptions) (machine.VM, error) {
	m := MacMachine{Name: opts.Name}

	configDir, err := machine.GetConfDir(machine.AppleHvVirt)
	if err != nil {
		return nil, err
	}

    // Create a new JSON config file for the machine
	configPath, err := machine.NewMachineFile(getVMConfigPath(configDir, opts.Name), nil)
	if err != nil {
		return nil, err
	}
	m.ConfigPath = *configPath

    // Create a new Ignition config file for the machine
	ignitionPath, err := machine.NewMachineFile(filepath.Join(configDir, m.Name)+".ign", nil)
	if err != nil {
		return nil, err
	}
	m.IgnitionFile = *ignitionPath

	// Set creation time
	m.Created = time.Now()

	m.ResourceConfig = machine.ResourceConfig{
		CPUs:     opts.CPUS,
		DiskSize: opts.DiskSize,
		// Diskpath will be needed
		Memory: opts.Memory,
	}

    // Marshals the machine instance and writes it to the config file
	if err := m.writeConfig(); err != nil {
		return nil, err
	}
	return m.loadFromFile()
}

func (v Virtualization) RemoveAndCleanMachines() error {
	// This can be implemented when host networking is completed.
	return machine.ErrNotImplemented
}

func (v Virtualization) VMType() machine.VMType {
	return vmtype
}

func (v Virtualization) loadFromLocalJson() ([]*MacMachine, error) {
	var (
		jsonFiles []string
		mms       []*MacMachine
	)
	configDir, err := machine.GetConfDir(v.VMType())
	if err != nil {
		return nil, err
	}
    // Gather the list of all virtual machine JSON configuration files
	if err := filepath.WalkDir(configDir, func(input string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if filepath.Ext(d.Name()) == ".json" {
			jsonFiles = append(jsonFiles, input)
		}
		return nil
	}); err != nil {
		return nil, err
	}

    // Iterate through the virtual machine JSON configuration files and load
    // each instance to eventually get returned
	for _, jsonFile := range jsonFiles {
		mm := MacMachine{}
		if err := loadMacMachineFromJSON(jsonFile, &mm); err != nil {
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		mms = append(mms, &mm)
	}
	return mms, nil
}
