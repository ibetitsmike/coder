package sandbox_test

import (
	"slices"
	"testing"
	"time"
)

// TestSandboxPerfCreates measures sequential create builds through the full
// coderd + provisionerd + sandbox engine path.
func TestSandboxPerfCreates(t *testing.T) {
	t.Parallel()

	srv := startSandboxServer(t, nil)
	srv.importTemplate(t, "sandbox.yaml")

	var durs []time.Duration
	for i := 0; i < 10; i++ {
		start := time.Now()
		srv.createWorkspace(t)
		durs = append(durs, time.Since(start))
	}

	slices.Sort(durs)
	t.Logf("create durations: %v", durs)
	t.Logf("p50 create: %s", durs[5])
	if durs[5] >= 5*time.Second {
		t.Fatalf("p50 create %s exceeds 5s budget", durs[5])
	}
}
