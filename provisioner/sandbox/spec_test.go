package sandbox_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/provisioner/sandbox"
)

func TestParseSpec(t *testing.T) {
	t.Parallel()

	t.Run("Minimal", func(t *testing.T) {
		t.Parallel()
		spec, err := sandbox.ParseSpec([]byte(`version: 1
container:
  image: docker.io/library/alpine:3.20
agent:
  name: main
`))
		require.NoError(t, err)
		require.Equal(t, 1, spec.Version)
		require.Equal(t, "docker.io/library/alpine:3.20", spec.Container.Image)
		require.Equal(t, "main", spec.Agent.Name)
		require.Equal(t, "amd64", spec.Agent.Arch)
		require.Equal(t, "linux", spec.Agent.OS)
	})

	t.Run("Full", func(t *testing.T) {
		t.Parallel()
		spec, err := sandbox.ParseSpec([]byte(`version: 1
container:
  image: docker.io/library/alpine:3.20
  entrypoint: ["/bin/sh", "-c", "sleep infinity"]
  env:
    HOME: /home/coder
  cpu_millis: 1000
  memory_mib: 2048
agent:
  name: main
  arch: arm64
  os: linux
  dir: /home/coder
lifecycle:
  stop_after_idle_minutes: 30
`))
		require.NoError(t, err)
		require.Equal(t, []string{"/bin/sh", "-c", "sleep infinity"}, spec.Container.Entrypoint)
		require.Equal(t, map[string]string{"HOME": "/home/coder"}, spec.Container.Env)
		require.Equal(t, int64(1000), spec.Container.CPUMillis)
		require.Equal(t, int64(2048), spec.Container.MemoryMiB)
		require.Equal(t, "arm64", spec.Agent.Arch)
		require.Equal(t, "/home/coder", spec.Agent.Dir)
		require.Equal(t, int64(30), spec.Lifecycle.StopAfterIdleMinutes)
	})

	t.Run("UnknownField", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte(`version: 1
container:
  image: alpine
  nope: true
agent:
  name: main
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "not valid yaml")
	})

	t.Run("MissingImage", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte(`version: 1
agent:
  name: main
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "container.image")
	})

	t.Run("BadResources", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte(`version: 1
container:
  image: alpine
  cpu_millis: 999999
agent:
  name: main
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "cpu_millis")
	})

	t.Run("BadArch", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte(`version: 1
container:
  image: alpine
agent:
  name: main
  arch: sparc
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "agent.arch")
	})

	t.Run("BadOS", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte(`version: 1
container:
  image: alpine
agent:
  name: main
  os: windows
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "agent.os")
	})

	t.Run("BadYAML", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte("version: [1,\n"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "not valid yaml")
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "is empty")
	})

	t.Run("WrongVersion", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte(`version: 2
container:
  image: alpine
agent:
  name: main
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "version")
	})

	t.Run("AllErrorsAtOnce", func(t *testing.T) {
		t.Parallel()
		_, err := sandbox.ParseSpec([]byte(`version: 3
container:
  cpu_millis: -5
lifecycle:
  stop_after_idle_minutes: -1
`))
		require.Error(t, err)
		for _, want := range []string{"version", "container.image", "agent.name", "cpu_millis", "stop_after_idle_minutes"} {
			require.Contains(t, err.Error(), want)
		}
	})

	t.Run("TooLarge", func(t *testing.T) {
		t.Parallel()
		big := make([]byte, sandbox.MaxSpecBytes+1)
		for i := range big {
			big[i] = ' '
		}
		_, err := sandbox.ParseSpec(big)
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds")
	})
}
