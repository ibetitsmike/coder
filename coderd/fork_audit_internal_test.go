package coderd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForkAuditURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://example.com/org/repo.git", forkAuditURL("https://user:token@example.com/org/repo.git?access_token=secret#frag"))
	require.Equal(t, "https://example.com/org/repo.git", forkAuditURL("https://example.com/org/repo.git"))
	require.Empty(t, forkAuditURL("://bad"))
}
