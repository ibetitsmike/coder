// This file is fork code: the container runtime interface for the sandbox
// provisioner. No production runtime exists yet. Tests use
// sandboxtest.FakeRuntime.
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
	// ID names the container. The caller derives it from the workspace so a
	// retried create converges on the same container.
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
	// Volumes are the named volumes to mount. Create makes a missing volume,
	// and removing the container keeps it.
	Volumes []VolumeMount
}

// VolumeMount mounts the named volume at Path in the container.
type VolumeMount struct {
	Name string
	Path string
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

// Runtime creates and controls disposable containers. Start, Stop and Remove
// converge on the same end state when retried, and Create reports an existing
// ID with ErrExists, so the engine can replay an operation after a crash.
type Runtime interface {
	// Create makes the container in the created state. Creating an existing
	// ID returns an error that errors.Is matches with ErrExists.
	Create(ctx context.Context, req CreateRequest) (Container, error)
	// Start runs the container process. Starting a running container succeeds
	// without changing it.
	Start(ctx context.Context, containerID string) error
	// Stop stops the container process. Stopping a stopped or created
	// container succeeds without changing it.
	Stop(ctx context.Context, containerID string, timeout time.Duration) error
	// Remove deletes the container. Removing an unknown ID returns an error
	// that errors.Is matches with ErrNotFound.
	Remove(ctx context.Context, containerID string) error
	// Status reads one container. An unknown ID returns an error that
	// errors.Is matches with ErrNotFound.
	Status(ctx context.Context, containerID string) (Container, error)
	// Volumes lists the names of the volumes the runtime created whose names
	// start with prefix.
	Volumes(ctx context.Context, prefix string) ([]string, error)
	// RemoveVolume deletes a volume. Removing a volume that a container
	// mounts fails, and removing an unknown name returns an error that
	// errors.Is matches with ErrNotFound.
	RemoveVolume(ctx context.Context, name string) error
}

// ErrNotFound reports that the runtime does not know the container or
// volume.
var ErrNotFound = xerrors.New("not found")

// ErrExists reports that the runtime already has a container with that ID.
var ErrExists = xerrors.New("container already exists")
