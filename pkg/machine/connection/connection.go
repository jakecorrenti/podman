//go:build amd64 || arm64

package connection

import (
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/containers/common/pkg/config"
)

const LocalhostIP = "127.0.0.1"

type connection struct {
	name string
	uri  *url.URL
}

func addConnection(cons []connection, identity string, isDefault bool) error {
	if len(identity) < 1 {
		return errors.New("identity must be defined")
	}

	return config.EditConnectionConfig(func(cfg *config.ConnectionsFile) error {
		for i, con := range cons {
			if _, ok := cfg.Connection.Connections[con.name]; ok {
				return fmt.Errorf("cannot overwrite connection %q", con.name)
			}

			dst := config.Destination{
				URI:       con.uri.String(),
				IsMachine: true,
				Identity:  identity,
			}

			if isDefault && i == 0 {
				cfg.Connection.Default = con.name
			}

			if cfg.Connection.Connections == nil {
				cfg.Connection.Connections = map[string]config.Destination{
					con.name: dst,
				}
				cfg.Connection.Default = con.name
			} else {
				cfg.Connection.Connections[con.name] = dst
			}
		}

		return nil
	})
}

func UpdateConnectionPairPort(name string, port, uid int, remoteUsername string, identityPath string) error {
	cons := createConnections(name, uid, port, remoteUsername)
	return config.EditConnectionConfig(func(cfg *config.ConnectionsFile) error {
		for _, con := range cons {
			dst := config.Destination{
				IsMachine: true,
				URI:       con.uri.String(),
				Identity:  identityPath,
			}
			cfg.Connection.Connections[con.name] = dst
		}

		return nil
	})
}

// UpdateConnectionIfDefault updates the default connection to the rootful/rootless when depending
// on the bool but only if other rootful/less connection was already the default.
// Returns true if it modified the default
func UpdateConnectionIfDefault(rootful bool, name, rootfulName string) error {
	return config.EditConnectionConfig(func(cfg *config.ConnectionsFile) error {
		if name == cfg.Connection.Default && rootful {
			cfg.Connection.Default = rootfulName
		} else if rootfulName == cfg.Connection.Default && !rootful {
			cfg.Connection.Default = name
		}
		return nil
	})
}

func RemoveConnections(machines map[string]bool, names ...string) error {
	var dest config.Destination
	var service string

	if err := config.EditConnectionConfig(func(cfg *config.ConnectionsFile) error {
		return setNewDefaultConnection(cfg, &dest, &service, names...)
	}); err != nil {
		return err
	}

	rootful, ok := machines[service]
	if dest.IsMachine && ok {
		return UpdateConnectionIfDefault(rootful, service, service+"-root")
	}

	return nil
}

// setNewDefaultConnection iterates through the list of system connections and sets the new default as the very first one
func setNewDefaultConnection(cfg *config.ConnectionsFile, dest *config.Destination, service *string, names ...string) error {
	// delete the connection associated with the names and if that connection is
	// the default, reset the default connection
	for _, name := range names {
		if _, ok := cfg.Connection.Connections[name]; ok {
			delete(cfg.Connection.Connections, name)
		} else {
			return fmt.Errorf("unable to find connection named %q", name)
		}

		if cfg.Connection.Default == name {
			cfg.Connection.Default = ""
		}
	}

	// set the new default system connection to the first in the map
	for con := range cfg.Connection.Connections {
		cfg.Connection.Default = con
		if c, ok := cfg.Connection.Connections[con]; ok {
			*dest = c
			*service = con
		}
		break
	}

	return nil
}

// makeSSHURL creates a URL from the given input
func makeSSHURL(host, path, port, userName string) *url.URL {
	var hostname string
	if len(port) > 0 {
		hostname = net.JoinHostPort(host, port)
	} else {
		hostname = host
	}
	userInfo := url.User(userName)
	return &url.URL{
		Scheme: "ssh",
		User:   userInfo,
		Host:   hostname,
		Path:   path,
	}
}
