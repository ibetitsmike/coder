// This file is fork code: it is the sandbox.yaml template spec for the
// experimental native sandbox provisioner.
package sandbox

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpecVersion is the only sandbox.yaml version this parser accepts.
const SpecVersion = 1

// Spec is a parsed sandbox.yaml. It describes one disposable container that
// runs the workspace agent.
type Spec struct {
	Version   int           `yaml:"version"`
	Container ContainerSpec `yaml:"container"`
	Agent     AgentSpec     `yaml:"agent"`
}

// ContainerSpec describes the container image and how it starts.
type ContainerSpec struct {
	Image      string   `yaml:"image"`
	Entrypoint []string `yaml:"entrypoint"`
	// Env is the container environment. Names that start with
	// ReservedEnvPrefix are reserved for the variables the provisioner sets.
	Env map[string]string `yaml:"env"`
	// CPUMillis and MemoryMiB bound the container. Zero means the runtime
	// default.
	CPUMillis int64 `yaml:"cpu_millis"`
	MemoryMiB int64 `yaml:"memory_mib"`
}

// AgentSpec describes the Coder agent that runs inside the container.
type AgentSpec struct {
	Name string `yaml:"name"`
	Arch string `yaml:"arch"`
	OS   string `yaml:"os"`
	Dir  string `yaml:"dir"`
}

// ParseError describes one problem in a sandbox.yaml.
type ParseError struct {
	Field string
	Issue string
}

func (e ParseError) Error() string {
	return fmt.Sprintf("sandbox.yaml field %q %s", e.Field, e.Issue)
}

// ParseErrors is every problem found in one parse pass.
type ParseErrors []ParseError

func (e ParseErrors) Error() string {
	msgs := make([]string, 0, len(e))
	for _, err := range e {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// ReservedEnvPrefix starts the names of the container environment variables
// that the provisioner sets, such as the agent token. A spec cannot set them.
const ReservedEnvPrefix = "CODER_AGENT_"

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// MaxSpecBytes bounds the spec size. The archive carries it, so it must not
// be a place to hide unbounded data.
const MaxSpecBytes = 1 << 20

// ParseSpec parses and validates sandbox.yaml bytes. It returns every problem
// it finds in one pass.
func ParseSpec(data []byte) (*Spec, error) {
	if len(data) == 0 {
		return nil, ParseErrors{{Field: "", Issue: "is empty"}}
	}
	if len(data) > MaxSpecBytes {
		return nil, ParseErrors{{Field: "", Issue: fmt.Sprintf("exceeds %d bytes", MaxSpecBytes)}}
	}
	var spec Spec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, ParseErrors{{Field: "", Issue: fmt.Sprintf("is not valid yaml: %s", err)}}
	}

	var errs ParseErrors
	if spec.Version != SpecVersion {
		errs = append(errs, ParseError{
			Field: "version",
			Issue: fmt.Sprintf("is %d, want %d", spec.Version, SpecVersion),
		})
	}
	if spec.Container.Image == "" {
		errs = append(errs, ParseError{Field: "container.image", Issue: "is required"})
	}
	if spec.Container.CPUMillis < 0 || spec.Container.CPUMillis > 16384 {
		errs = append(errs, ParseError{
			Field: "container.cpu_millis",
			Issue: fmt.Sprintf("is %d, want between 0 and 16384", spec.Container.CPUMillis),
		})
	}
	if spec.Container.MemoryMiB < 0 || spec.Container.MemoryMiB > 65536 {
		errs = append(errs, ParseError{
			Field: "container.memory_mib",
			Issue: fmt.Sprintf("is %d, want between 0 and 65536", spec.Container.MemoryMiB),
		})
	}
	if spec.Agent.Name == "" {
		errs = append(errs, ParseError{Field: "agent.name", Issue: "is required"})
	}
	if spec.Agent.Arch == "" {
		spec.Agent.Arch = "amd64"
	}
	if spec.Agent.OS == "" {
		spec.Agent.OS = "linux"
	}
	switch spec.Agent.Arch {
	case "amd64", "arm64", "armv7":
	default:
		errs = append(errs, ParseError{
			Field: "agent.arch",
			Issue: fmt.Sprintf("is %q, want amd64, arm64 or armv7", spec.Agent.Arch),
		})
	}
	switch spec.Agent.OS {
	case "linux":
	default:
		errs = append(errs, ParseError{
			Field: "agent.os",
			Issue: fmt.Sprintf("is %q, want linux", spec.Agent.OS),
		})
	}
	names := make([]string, 0, len(spec.Container.Env))
	for name := range spec.Container.Env {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		switch {
		case !envNameRE.MatchString(name):
			errs = append(errs, ParseError{
				Field: "container.env." + name,
				Issue: "is not a valid environment variable name",
			})
		case strings.HasPrefix(name, ReservedEnvPrefix):
			errs = append(errs, ParseError{
				Field: "container.env." + name,
				Issue: fmt.Sprintf("is reserved: names that start with %s are set by the provisioner", ReservedEnvPrefix),
			})
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return &spec, nil
}
