// Package dockerruntime is fork code: a sandbox.Runtime on the Docker Engine
// API. The sandbox engine names each container after its workspace, so the
// runtime addresses containers by that name.
package dockerruntime

import (
	"context"
	"slices"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
)

// LabelSandbox marks the containers this runtime creates. The runtime acts
// only on containers that carry it.
const LabelSandbox = "com.coder.sandbox"

// ErrNotOwned reports a container of the name that this runtime did not
// create.
var ErrNotOwned = xerrors.New("the container was not created by the sandbox runtime")

// Runtime drives sandbox containers through a Docker daemon. It serializes
// the calls on one container, so a call from a reaped job cannot interleave
// with a call from a newer build of the same workspace.
type Runtime struct {
	client *client.Client
	locks  locks
}

var _ sandbox.Runtime = (*Runtime)(nil)

// New connects to the Docker daemon that the DOCKER_HOST environment
// selects, or the local socket, and checks that it answers.
func New(ctx context.Context) (*Runtime, error) {
	c, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, xerrors.Errorf("docker client: %w", err)
	}
	if _, err := c.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		_ = c.Close()
		return nil, xerrors.Errorf("ping docker: %w", err)
	}
	return &Runtime{client: c, locks: locks{held: map[string]*lock{}}}, nil
}

// Close releases the client's connections.
func (r *Runtime) Close() error {
	return r.client.Close()
}

// Create makes the container and pulls its image when the daemon lacks it.
func (r *Runtime) Create(ctx context.Context, req sandbox.CreateRequest) (sandbox.Container, error) {
	unlock, err := r.locks.acquire(ctx, req.ID)
	if err != nil {
		return sandbox.Container{}, err
	}
	defer unlock()

	env := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	opts := client.ContainerCreateOptions{
		Name: req.ID,
		Config: &container.Config{
			Image:      req.Image,
			Entrypoint: req.Entrypoint,
			Env:        env,
			Labels:     map[string]string{LabelSandbox: "true"},
		},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				NanoCPUs: req.CPUMillis * 1_000_000,
				Memory:   req.MemoryMiB << 20,
			},
		},
	}
	_, err = r.client.ContainerCreate(ctx, opts)
	if cerrdefs.IsNotFound(err) {
		// The daemon does not have the image.
		if err := r.pull(ctx, req.Image); err != nil {
			return sandbox.Container{}, xerrors.Errorf("pull image %q: %w", req.Image, err)
		}
		_, err = r.client.ContainerCreate(ctx, opts)
	}
	if cerrdefs.IsConflict(err) {
		if _, err := r.inspect(ctx, req.ID); err != nil {
			return sandbox.Container{}, err
		}
		return sandbox.Container{}, xerrors.Errorf("%w: %s", sandbox.ErrExists, req.ID)
	}
	if err != nil {
		return sandbox.Container{}, xerrors.Errorf("create container %s: %w", req.ID, err)
	}
	return r.inspect(ctx, req.ID)
}

func (r *Runtime) pull(ctx context.Context, image string) error {
	resp, err := r.client.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer resp.Close()
	return resp.Wait(ctx)
}

// Start runs the container. The daemon accepts a start of a running
// container without restarting it.
func (r *Runtime) Start(ctx context.Context, containerID string) error {
	unlock, err := r.locks.acquire(ctx, containerID)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := r.inspect(ctx, containerID); err != nil {
		return err
	}
	_, err = r.client.ContainerStart(ctx, containerID, client.ContainerStartOptions{})
	return mapError("start", containerID, err)
}

// Stop stops the container, and kills it after timeout. The daemon accepts
// a stop of a container that is not running.
func (r *Runtime) Stop(ctx context.Context, containerID string, timeout time.Duration) error {
	unlock, err := r.locks.acquire(ctx, containerID)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := r.inspect(ctx, containerID); err != nil {
		return err
	}
	seconds := int(timeout / time.Second)
	_, err = r.client.ContainerStop(ctx, containerID, client.ContainerStopOptions{Timeout: &seconds})
	return mapError("stop", containerID, err)
}

// Remove deletes the container, running or not, with its anonymous volumes.
func (r *Runtime) Remove(ctx context.Context, containerID string) error {
	unlock, err := r.locks.acquire(ctx, containerID)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := r.inspect(ctx, containerID); err != nil {
		return err
	}
	_, err = r.client.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	return mapError("remove", containerID, err)
}

// Status reads the container.
func (r *Runtime) Status(ctx context.Context, containerID string) (sandbox.Container, error) {
	unlock, err := r.locks.acquire(ctx, containerID)
	if err != nil {
		return sandbox.Container{}, err
	}
	defer unlock()
	return r.inspect(ctx, containerID)
}

// inspect reads the container and fails with ErrNotOwned when it lacks
// LabelSandbox.
func (r *Runtime) inspect(ctx context.Context, containerID string) (sandbox.Container, error) {
	res, err := r.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return sandbox.Container{}, mapError("inspect", containerID, err)
	}
	in := res.Container
	if in.Config == nil || in.Config.Labels[LabelSandbox] != "true" {
		return sandbox.Container{}, xerrors.Errorf("%w: %s", ErrNotOwned, containerID)
	}
	c := sandbox.Container{
		ID:        containerID,
		State:     sandbox.ContainerStateStopped,
		CreatedAt: parseTime(in.Created),
	}
	c.Image = in.Config.Image
	if in.State != nil {
		switch in.State.Status {
		case container.StateCreated:
			c.State = sandbox.ContainerStateCreated
		case container.StateRunning, container.StatePaused, container.StateRestarting:
			c.State = sandbox.ContainerStateRunning
		}
		c.StartedAt = parseTime(in.State.StartedAt)
		c.StoppedAt = parseTime(in.State.FinishedAt)
	}
	return c, nil
}

// mapError reports a container the daemon does not know as
// sandbox.ErrNotFound.
func mapError(op, containerID string, err error) error {
	switch {
	case err == nil:
		return nil
	case cerrdefs.IsNotFound(err):
		return xerrors.Errorf("%w: %s", sandbox.ErrNotFound, containerID)
	default:
		return xerrors.Errorf("%s container %s: %w", op, containerID, err)
	}
}

// parseTime reads a daemon timestamp. The daemon reports a time that has not
// happened as the zero time, and an unreadable one is treated the same.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.IsZero() {
		return time.Time{}
	}
	return t
}
