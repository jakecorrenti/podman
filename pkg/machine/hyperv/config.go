//go:build windows
// +build windows

package hyperv

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/containers/libhvee/pkg/hypervctl"
	"github.com/containers/podman/v4/pkg/machine"
	"github.com/docker/go-units"
	"github.com/sirupsen/logrus"
)

// Like the ones in pkg/machine/applehv and pkg/machine/qemu, this can be extracted
// as its just an identical duplicate of the others
// NOTE: I think that this should be an interface, where we define the various 
// functions that each type must implement. Where these types may be something
// along the lines of AppleHVVirtualization, HyperVVirtualization, QEMUVirtualization,
// WSLVirtualization, etc. All of the implementations have the same type defined,
// and implement the same functions, just with their respective architecture
// dependent implementations. Lots of unnecessary complexity and duplicate code.
type Virtualization struct {
	artifact    machine.Artifact
	compression machine.ImageCompression
	format      machine.ImageFormat
}

func (v Virtualization) Artifact() machine.Artifact {
	return machine.None
}

func (v Virtualization) CheckExclusiveActiveVM() (bool, string, error) {
	vmm := hypervctl.NewVirtualMachineManager()
	// Use of GetAll is OK here because we do not want to use the same name
	// as something already *actually* configured in hyperv
	vms, err := vmm.GetAll()
	if err != nil {
		return false, "", err
	}
	for _, vm := range vms {
        // Check to see if there is currently a virtual machine that is running,
        // since there can only be one running virtual machine at a time
		if vm.IsStarting() || vm.State() == hypervctl.Enabled {
			return true, vm.ElementName, nil
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

func (v Virtualization) IsValidVMName(name string) (bool, error) {
	// We check both the local filesystem and hyperv for the valid name
	mm := HyperVMachine{Name: name}
	configDir, err := machine.GetConfDir(v.VMType())
	if err != nil {
		return false, err
	}
    // I think this function name is a typo.
	if err := loadMacMachineFromJSON(configDir, &mm); err != nil {
		return false, err
	}
	// The name is valid for the local filesystem
	if _, err := hypervctl.NewVirtualMachineManager().GetMachine(name); err != nil {
		return false, err
	}
	// The lookup in hyperv worked, so it is also valid there
	return true, nil
}

func (v Virtualization) List(opts machine.ListOptions) ([]*machine.ListResponse, error) {
    // Load a list of the virtual machines
	mms, err := v.loadFromLocalJson()
	if err != nil {
		return nil, err
	}

	var response []*machine.ListResponse
	vmm := hypervctl.NewVirtualMachineManager()

	for _, mm := range mms {
        // convert type HyperVMachine to libhvee.VirtualMachine
		vm, err := vmm.GetMachine(mm.Name)
		if err != nil {
			return nil, err
		}
		mlr := machine.ListResponse{
			Name:           mm.Name,
			CreatedAt:      mm.Created,
			LastUp:         mm.LastUp,
			Running:        vm.State() == hypervctl.Enabled,
			Starting:       vm.IsStarting(),
			Stream:         mm.ImageStream,
			VMType:         machine.HyperVVirt.String(),
			CPUs:           mm.CPUs,
			Memory:         mm.Memory * units.MiB,
			DiskSize:       mm.DiskSize * units.GiB,
			Port:           mm.Port,
			RemoteUsername: mm.RemoteUsername,
			IdentityPath:   mm.IdentityPath,
		}
		response = append(response, &mlr)
	}
	return response, err
}

func (v Virtualization) LoadVMByName(name string) (machine.VM, error) {
	m := &HyperVMachine{Name: name}
	return m.loadFromFile()
}

// So here we return a machine.VM instance. However, in some of the functions 
// in this file, we return *HyperVMachine. If we can get away with just dealing
// with the base interface type, we would (I presume), be able to reduce a lot
// of duplicate code
func (v Virtualization) NewMachine(opts machine.InitOptions) (machine.VM, error) {
	m := HyperVMachine{Name: opts.Name}
    // why this is necessary is specified below
	if len(opts.ImagePath) < 1 {
		return nil, errors.New("must define --image-path for hyperv support")
	}

    // get location for hyperv configuration files
	configDir, err := machine.GetConfDir(machine.HyperVVirt)
	if err != nil {
		return nil, err
	}

    // get the location for the virtual machine's JSON config file and create
    // a VMFile instance
	configPath, err := machine.NewMachineFile(getVMConfigPath(configDir, opts.Name), nil)
	if err != nil {
		return nil, err
	}
	m.ConfigPath = *configPath

    // get the location for the virtual machine's ignition config file and 
    // create a VMFile instance
	ignitionPath, err := machine.NewMachineFile(filepath.Join(configDir, m.Name)+".ign", nil)
	if err != nil {
		return nil, err
	}
	m.IgnitionFile = *ignitionPath

	// Set creation time
	m.Created = time.Now()

    // get location for virtual machine images
	dataDir, err := machine.GetDataDir(machine.HyperVVirt)
	if err != nil {
		return nil, err
	}

	// Acquire the image
	// Until we are producing vhdx images in fcos, all images must be fed to us
	// with --image-path.  We should, however, accept both a file or url
    // Create a DistributedDownload instance that represents the image that
    // we would like to download to the host
	g, err := machine.NewGenericDownloader(machine.HyperVVirt, opts.Name, opts.ImagePath)
	if err != nil {
		return nil, err
	}

    // get the path to the uncompressed image
	imagePath, err := machine.NewMachineFile(g.Get().GetLocalUncompressedFile(dataDir), nil)
	if err != nil {
		return nil, err
	}
	m.ImagePath = *imagePath
    // actually download the image to the host
	if err := machine.DownloadImage(g); err != nil {
		return nil, err
	}

	config := hypervctl.HardwareConfig{
		CPUs:     uint16(opts.CPUS),
		DiskPath: imagePath.GetPath(),
		DiskSize: opts.DiskSize,
		Memory:   int32(opts.Memory),
	}

	// Write the json configuration file which will be loaded by
	// LoadByName
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return nil, err
	}
    // once the image is downloaded, we can write the machine's config to 
    // the host filesystem
	if err := os.WriteFile(m.ConfigPath.GetPath(), b, 0644); err != nil {
		return nil, err
	}

	vmm := hypervctl.NewVirtualMachineManager()
    // This *actually* creates the virtual machine via the virtualization provider,
    // which in this case is handled by containers/libhvee
	if err := vmm.NewVirtualMachine(opts.Name, &config); err != nil {
		return nil, err
	}
    // once the virtual machine is actually created by libhvee we can load it 
    // and return the instance
	return v.LoadVMByName(opts.Name)
}

func (v Virtualization) RemoveAndCleanMachines() error {
	// Error handling used here is following what qemu did
	var (
		prevErr error
	)

	// The next three info lookups must succeed or we return
    // get a list of all the hyperv virtual machines
	mms, err := v.loadFromLocalJson()
	if err != nil {
		return err
	}

    // filepath for all virtual machine config files
	configDir, err := machine.GetConfDir(vmtype)
	if err != nil {
		return err
	}

    // directory where podman-machine stores virtual machine images
	dataDir, err := machine.GetDataDir(vmtype)
	if err != nil {
		return err
	}

	vmm := hypervctl.NewVirtualMachineManager()
	for _, mm := range mms {
        // convert from  HyperVMachine instance to *libhvee.VirtualMachine
		vm, err := vmm.GetMachine(mm.Name)
		if err != nil {
			prevErr = handlePrevError(err, prevErr)
		}

		// If the VM is not stopped, we need to stop it
		// TODO stop might not be enough if the state is dorked. we may need
		// something like forceoff hard switch
		if vm.State() != hypervctl.Disabled {
			if err := vm.Stop(); err != nil {
				prevErr = handlePrevError(err, prevErr)
			}
		}
        // Remove the virtual machine once ensured it has been stopped
		if err := vm.Remove(mm.ImagePath.GetPath()); err != nil {
			prevErr = handlePrevError(err, prevErr)
		}
        // Remove any sockets associated with the virtual machine
		if err := mm.ReadyHVSock.Remove(); err != nil {
			prevErr = handlePrevError(err, prevErr)
		}
		if err := mm.NetworkHVSock.Remove(); err != nil {
			prevErr = handlePrevError(err, prevErr)
		}
	}

	// Nuke the config and dataDirs
	if err := os.RemoveAll(configDir); err != nil {
		prevErr = handlePrevError(err, prevErr)
	}
	if err := os.RemoveAll(dataDir); err != nil {
		prevErr = handlePrevError(err, prevErr)
	}
	return prevErr
}

func (v Virtualization) VMType() machine.VMType {
	return vmtype
}

// I think the name of this function is not great. Load what from Local JSON?
// Unclear if it is loading a single virtual machine or a list. I think something
// like loadMachinesFromLocalJSON is a little more descriptive and doesn't require
// me to look at the function definition to see what the return type is
func (v Virtualization) loadFromLocalJson() ([]*HyperVMachine, error) {
	var (
		jsonFiles []string
		mms       []*HyperVMachine
	)
	configDir, err := machine.GetConfDir(v.VMType())
	if err != nil {
		return nil, err
	}
    // get a list of all the JSON config files in the config direcotry
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

    // Iterate through the JSON config files and convert to their respective
    // HyperVMachine instances
	for _, jsonFile := range jsonFiles {
		mm := HyperVMachine{}
		if err := loadMacMachineFromJSON(jsonFile, &mm); err != nil {
			return nil, err
		}
        // duplicate error check
		if err != nil {
			return nil, err
		}
		mms = append(mms, &mm)
	}
	return mms, nil
}

func handlePrevError(e, prevErr error) error {
	if prevErr != nil {
		logrus.Error(e)
	}
	return e
}
