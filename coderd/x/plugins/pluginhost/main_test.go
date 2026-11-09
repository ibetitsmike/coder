package pluginhost_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"go.uber.org/goleak"

	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
	"github.com/coder/coder/v2/testutil"
)

const canaryEnv = "CODER_PLUGINHOST_TEST_CANARY"

// TestMain doubles as the plugin host child: StartProcess re-executes the
// test binary with the default "plugin-host" argument.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "plugin-host" {
		os.Exit(runChild())
	}
	// The child must not inherit this.
	_ = os.Setenv(canaryEnv, "secret")
	goleak.VerifyTestMain(m, testutil.GoleakOptions...)
}

func runChild() int {
	env, _ := json.Marshal(os.Environ())
	_, _ = fmt.Fprintf(os.Stderr, "CHILD_ENV=%s\n", env)
	err := pluginhost.Serve(context.Background(), &stdio{Reader: os.Stdin, Writer: os.Stdout}, os.Stderr)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type stdio struct {
	io.Reader
	io.Writer
}

func (*stdio) Close() error {
	_ = os.Stdin.Close()
	return os.Stdout.Close()
}
