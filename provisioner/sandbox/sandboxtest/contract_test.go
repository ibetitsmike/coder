package sandboxtest_test

import (
	"testing"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
)

func TestFakeRuntimeContract(t *testing.T) {
	t.Parallel()
	sandboxtest.RuntimeContract(t, "alpine", func(*testing.T) sandbox.Runtime {
		return sandboxtest.NewFakeRuntime()
	})
}
