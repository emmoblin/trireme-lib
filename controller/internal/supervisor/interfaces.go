package supervisor

import (
	"context"

	provider "go.aporeto.io/trireme-lib/controller/pkg/aclprovider"
	"go.aporeto.io/trireme-lib/policy"
)

// A Supervisor is implementing the node control plane that captures the packets.
type Supervisor interface {

	// Supervise adds a new supervised processing unit.
	Supervise(contextID string, puInfo *policy.PUInfo) error

	// Unsupervise unsupervises the given PU
	Unsupervise(contextID string) error

	// Start starts the Supervisor.
	Run(ctx context.Context) error

	// SetTargetNetworks sets the target networks of the supervisor
	SetTargetNetworks([]string) error

	// CleanUp requests the supervisor to clean up all ACLs
	CleanUp() error
}

// Implementor is the interface of the implementation based on iptables, ipsets, remote etc
type Implementor interface {

	// ConfigureRules configures the rules in the ACLs and datapath
	ConfigureRules(version int, contextID string, containerInfo *policy.PUInfo) error

	// UpdateRules updates the rules with a new version
	UpdateRules(version int, contextID string, containerInfo *policy.PUInfo, oldContainerInfo *policy.PUInfo) error

	// DeleteRules
	DeleteRules(version int, context string, tcpPorts, udpPorts string, mark string, uid string, proxyPort string) error

	// SetTargetNetworks sets the target networks of the supervisor
	SetTargetNetworks([]string, []string) error

	// Start initializes any defaults
	Run(ctx context.Context) error

	// CleanUp requests the implementor to clean up all ACLs
	CleanUp() error

	// ACLProvider returns the ACL provider used by the implementor
	ACLProvider() provider.IptablesProvider
}
