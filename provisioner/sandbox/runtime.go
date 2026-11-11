// This file is fork code: the container runtime interface for the sandbox
// provisioner. The real containerd adapter lands separately; tests use
// FakeRuntime.
package sandbox

import (
	"context"
	"time"

	"golang.org/x/xerrors"
)

// ContainerState is the lifecycle state of one sandbox container.
type ContainerState string

const (
	// ContainerStateCreated means the container exists but has not started.
	ContainerStateCreated ContainerState = "created"
	// ContainerStateRunning means the container process is up.
	ContainerStateRunning ContainerState = "running"
	// ContainerStateStopped means the container ran and then exited or was
	// stopped.
	ContainerStateStopped ContainerState = "stopped"
)

// CreateRequest describes the container to create.
type CreateRequest struct {
	// ID names the container. The caller derives it from the workspace and
	// build so a retried create converges on the same container.
	ID string
	// Image is the image reference from the spec.
	Image string
	// Entrypoint overrides the image entrypoint when set.
	Entrypoint []string
	// Env sets container environment variables.
	Env map[string]string
	// CPUMillis and MemoryMiB bound the container. Zero means the runtime
	// default.
	CPUMillis int64
	MemoryMiB int64
}

// Container is one container the runtime created.
type Container struct {
	ID        string
	Image     string
	State     ContainerState
	CreatedAt time.Time
	StartedAt time.Time
	StoppedAt time.Time
}

// ExecRequest describes a command to run inside a running container.
type ExecRequest struct {
	ContainerID string
	Cmd         []string
}

// Runtime creates and controls disposable containers. Every method converges
// on the same end state when retried, so the engine can replay operations
// after a crash.
type Runtime interface {
	// Create makes the container in the created state. Creating an existing
	// ID fails.
	Create(ctx context.Context, req CreateRequest) (Container, error)
	// Start runs the container process. Starting a running container succeeds
	// without changing it.
	Start(ctx context.Context, containerID string) error
	// Stop stops the container process. Stopping a stopped or created
	// container succeeds without changing it.
	Stop(ctx context.Context, containerID string, timeout time.Duration) error
	// Remove deletes the container. Removing an unknown ID fails.
	Remove(ctx context.Context, containerID string) error
	// Status reads one container. An unknown ID returns an error that
	// errors.Is matches with ErrNotFound.
	Status(ctx context.Context, containerID string) (Container, error)
	// Exec runs a command in a running container and returns its output.
	Exec(ctx context.Context, req ExecRequest) ([]byte, error)
}

// ErrNotFound reports that the runtime does not know the container.
var ErrNotFound = xerrors.New("container not found")

// ErrExists reports that the runtime already has a container with that ID.
var ErrExists = xerrors.New("container already exists")

// ErrNotRunning reports that the container is not in the running state.
var ErrNotRunning = xerrors.New("container not running")
