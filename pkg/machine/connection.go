//go:build amd64 || arm64
// +build amd64 arm64

package machine

import (
	"errors"
	"fmt"

	"github.com/containers/common/pkg/config"
)

const LocalhostIP = "127.0.0.1"

// Identity is the private key that ssh uses to get access to a private server
func AddConnection(uri fmt.Stringer, name, identity string, isDefault bool) error {
	if len(identity) < 1 {
		return errors.New("identity must be defined")
	}
    // get the custom virtual machine config file if it exists
	cfg, err := config.ReadCustomConfig()
	if err != nil {
		return err
	}
    // Check to see if there is already a remote service that exists with the 
    // name we are trying to add
	if _, ok := cfg.Engine.ServiceDestinations[name]; ok {
		return errors.New("cannot overwrite connection")
	}
    // set the new service to be the active service
	if isDefault {
		cfg.Engine.ActiveService = name
	}
	dst := config.Destination{
		URI:       uri.String(),
		IsMachine: true,
	}
	dst.Identity = identity
    // Check if there are currently no remote services
	if cfg.Engine.ServiceDestinations == nil {
		cfg.Engine.ServiceDestinations = map[string]config.Destination{
			name: dst,
		}
		cfg.Engine.ActiveService = name
	} else {
		cfg.Engine.ServiceDestinations[name] = dst
	}
	return cfg.Write()
}

// Check to see if there are any services that are the default service
func AnyConnectionDefault(name ...string) (bool, error) {
	cfg, err := config.ReadCustomConfig()
	if err != nil {
		return false, err
	}
	for _, n := range name {
		if n == cfg.Engine.ActiveService {
			return true, nil
		}
	}

	return false, nil
}

func ChangeDefault(name string) error {
	cfg, err := config.ReadCustomConfig()
	if err != nil {
		return err
	}

	cfg.Engine.ActiveService = name

	return cfg.Write()
}

func RemoveConnections(names ...string) error {
	cfg, err := config.ReadCustomConfig()
	if err != nil {
		return err
	}
	for _, name := range names {
        // If it exists, delete the service
		if _, ok := cfg.Engine.ServiceDestinations[name]; ok {
			delete(cfg.Engine.ServiceDestinations, name)
		} else {
			return fmt.Errorf("unable to find connection named %q", name)
		}

        // if the removed service was the default, set a random service as the 
        // new default
		if cfg.Engine.ActiveService == name {
			cfg.Engine.ActiveService = ""
			for service := range cfg.Engine.ServiceDestinations {
				cfg.Engine.ActiveService = service
				break
			}
		}
	}
	return cfg.Write()
}
