package codersdk

// This file is fork code: the deployment options of the sandbox provisioner
// of the fork-sandbox-provisioner experiment.

import "github.com/coder/serpent"

// SandboxRuntimeDocker selects the Docker Engine runtime of the sandbox
// provisioner.
const SandboxRuntimeDocker = "docker"

// ForkSandboxConfig is the deployment configuration of the sandbox
// provisioner.
type ForkSandboxConfig struct {
	// Runtime selects the container runtime. Empty registers no sandbox
	// provisioner.
	Runtime serpent.String `json:"runtime" typescript:",notnull"`
	// DockerHost is the Docker daemon address of the docker runtime. Empty
	// uses DOCKER_HOST or the local socket.
	DockerHost serpent.String `json:"docker_host" typescript:",notnull"`
}

// forkOptions returns the fork-only deployment options.
func (c *DeploymentValues) forkOptions(chat *serpent.Group) serpent.OptionSet {
	group := &serpent.Group{
		Name:        "Sandbox",
		YAML:        "sandbox",
		Description: "Configure the sandbox provisioner of the fork-sandbox-provisioner experiment.",
	}
	return append(c.forkChatOptions(chat), serpent.OptionSet{
		{
			Name:        "Sandbox Runtime",
			Description: "The container runtime of the sandbox provisioner when the fork-sandbox-provisioner experiment is enabled. \"docker\" uses a Docker daemon. Empty registers no sandbox provisioner.",
			Flag:        "sandbox-runtime",
			Env:         "CODER_SANDBOX_RUNTIME",
			Value:       &c.Sandbox.Runtime,
			Group:       group,
			YAML:        "runtime",
		},
		{
			Name:        "Sandbox Docker Host",
			Description: "The Docker daemon address of the docker sandbox runtime, for example unix:///var/run/docker.sock. Empty uses DOCKER_HOST or the local socket.",
			Flag:        "sandbox-docker-host",
			Env:         "CODER_SANDBOX_DOCKER_HOST",
			Value:       &c.Sandbox.DockerHost,
			Group:       group,
			YAML:        "dockerHost",
		},
	}...)
}
