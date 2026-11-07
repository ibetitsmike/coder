// Package plugintest builds plugin Wasm modules for tests.
package plugintest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
)

// TestPluginPackage is a plugin that exercises the whole guest ABI. See its
// source for the commands its OnEvent handler accepts.
const TestPluginPackage = "github.com/coder/coder/v2/coderd/x/plugins/sandbox/testdata/testplugin"

type buildResult struct {
	once sync.Once
	wasm []byte
	err  error
}

var builds sync.Map

// TestPlugin returns the Wasm module of TestPluginPackage.
func TestPlugin(t testing.TB) []byte {
	t.Helper()
	return BuildWasm(t, TestPluginPackage)
}

// BuildWasm builds pkgPath with GOOS=wasip1 GOARCH=wasm -buildmode=c-shared
// and returns the module. Each package is built at most once per test
// binary, and its compiled code stays in the sandbox compilation cache until
// the test binary exits. The test is skipped when no Go toolchain is
// installed.
func BuildWasm(t testing.TB, pkgPath string) []byte {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("building plugin %s requires the go toolchain on PATH: %v", pkgPath, err)
	}
	key := pkgPath
	if strings.HasPrefix(pkgPath, ".") {
		key, err = filepath.Abs(pkgPath)
		require.NoError(t, err)
	}
	v, _ := builds.LoadOrStore(key, &buildResult{})
	res, _ := v.(*buildResult)
	res.once.Do(func() {
		res.wasm, res.err = build(goBin, t.TempDir(), pkgPath)
		if res.err == nil {
			res.err = keepCompiled(res.wasm)
		}
	})
	require.NoError(t, res.err, "build plugin %s", pkgPath)
	return res.wasm
}

func build(goBin, dir, pkgPath string) ([]byte, error) {
	out := filepath.Join(dir, "plugin.wasm")
	// A cold build can take minutes, so only the test timeout bounds it.
	cmd := exec.Command(goBin, "build", "-buildmode=c-shared", "-o", out, pkgPath)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, xerrors.Errorf("go build: %w\n%s", err, output)
	}
	return os.ReadFile(out)
}

// keepCompiled opens wasm in a sandbox and never closes it. The sandbox
// compilation cache frees an entry when the last module that uses it closes,
// and one compile of a Go module takes approximately 20 seconds under the
// race detector. The open module makes each later compile of the same bytes
// in this test binary a cache hit.
func keepCompiled(wasm []byte) error {
	if _, err := sandbox.New(context.Background(), wasm, sandbox.Limits{}, nil); err != nil {
		return xerrors.Errorf("compile module: %w", err)
	}
	return nil
}
