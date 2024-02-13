//go:build amd64 || arm64

package machine

import (
	"os"

	"github.com/containers/common/pkg/report"
	"github.com/containers/podman/v5/cmd/podman/common"
	"github.com/containers/podman/v5/cmd/podman/registry"
	"github.com/containers/podman/v5/cmd/podman/utils"
	"github.com/containers/podman/v5/pkg/machine"
	"github.com/containers/podman/v5/pkg/machine/vmconfigs"
	"github.com/spf13/cobra"
)

var (
	inspectCmd = &cobra.Command{
		Use:               "inspect [options] [MACHINE...]",
		Short:             "Inspect an existing machine",
		Long:              "Provide details on a managed virtual machine",
		PersistentPreRunE: machinePreRunE,
		RunE:              inspect,
		Example:           `podman machine inspect myvm`,
		ValidArgsFunction: autocompleteMachine,
	}
	inspectFlag = inspectFlagType{}
)

type inspectFlagType struct {
	format string
}

func init() {
	registry.Commands = append(registry.Commands, registry.CliCommand{
		Command: inspectCmd,
		Parent:  machineCmd,
	})

	flags := inspectCmd.Flags()
	formatFlagName := "format"
	flags.StringVar(&inspectFlag.format, formatFlagName, "", "Format volume output using JSON or a Go template")
	_ = inspectCmd.RegisterFlagCompletionFunc(formatFlagName, common.AutocompleteFormat(&machine.InspectInfo{}))
}

func inspect(cmd *cobra.Command, args []string) error {
	var (
		errs utils.OutputErrors
	)
	dirs, err := machine.GetMachineDirs(provider.VMType())
	if err != nil {
		return err
	}
	if len(args) < 1 {
		args = append(args, defaultMachineName)
	}

	vms := make([]machine.InspectInfo, 0, len(args))
	for _, name := range args {
		mc, err := vmconfigs.LoadMachineByName(name, dirs)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		state, err := provider.State(mc, false)
		if err != nil {
			return err
		}
		ignFile, err := mc.IgnitionFile()
		if err != nil {
			return err
		}

		ii := machine.InspectInfo{
			// TODO I dont think this is useful
			ConfigPath: *dirs.ConfigDir,
			// TODO Fill this out
			ConnectionInfo: machine.ConnectionConfig{},
			Created:        mc.Created,
			// TODO This is no longer applicable; we dont care about the provenance
			// of the image
			Image: machine.ImageConfig{
				IgnitionFile: *ignFile,
				ImagePath:    *mc.ImagePath,
			},
			LastUp:             mc.LastUp,
			Name:               mc.Name,
			Resources:          mc.Resources,
			SSHConfig:          mc.SSH,
			State:              state,
			UserModeNetworking: false,
			HostUser: mc.HostUser,
		}

		vms = append(vms, ii)
	}

	switch {
	case cmd.Flag("format").Changed:
		rpt := report.New(os.Stdout, cmd.Name())
		defer rpt.Flush()

		rpt, err := rpt.Parse(report.OriginUser, inspectFlag.format)
		if err != nil {
			return err
		}

		if err := rpt.Execute(vms); err != nil {
			errs = append(errs, err)
		}
	default:
		if err := printJSON(vms); err != nil {
			errs = append(errs, err)
		}
	}
	return errs.PrintErrors()
}

func printJSON(data []machine.InspectInfo) error {
	enc := json.NewEncoder(os.Stdout)
	// by default, json marshallers will force utf=8 from
	// a string. this breaks healthchecks that use <,>, &&.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "     ")
	return enc.Encode(data)
}
