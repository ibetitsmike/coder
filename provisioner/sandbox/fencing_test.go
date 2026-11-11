package sandbox_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/provisioner/sandbox"
)

func TestGeneration(t *testing.T) {
	t.Parallel()

	t.Run("AdvanceMovesToNewer", func(t *testing.T) {
		t.Parallel()

		g := sandbox.NewGeneration(1)
		require.True(t, g.Advance(2))
		require.Equal(t, int64(2), g.Current())
	})

	t.Run("AdvanceIgnoresOlderAndEqual", func(t *testing.T) {
		t.Parallel()

		g := sandbox.NewGeneration(3)
		require.False(t, g.Advance(2))
		require.False(t, g.Advance(3))
		require.Equal(t, int64(3), g.Current())
	})

	t.Run("Superseded", func(t *testing.T) {
		t.Parallel()

		g := sandbox.NewGeneration(5)
		require.True(t, g.Superseded(4))
		require.False(t, g.Superseded(5))
		require.False(t, g.Superseded(6))
	})

	t.Run("ConcurrentAdvancesKeepMax", func(t *testing.T) {
		t.Parallel()

		g := sandbox.NewGeneration(0)
		var wg sync.WaitGroup
		for i := int64(1); i <= 50; i++ {
			wg.Add(1)
			go func(i int64) {
				defer wg.Done()
				g.Advance(i)
			}(i)
		}
		wg.Wait()
		require.Equal(t, int64(50), g.Current())
	})
}
