package dbcrypt

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// TestForkQueries checks that fork queries run through dbCrypt, which wraps
// the store in deployments with encryption keys.
func TestForkQueries(t *testing.T) {
	t.Parallel()

	_, crypt, _ := setup(t)
	fdb, err := forkdb.From(crypt)
	require.NoError(t, err)
	_, err = fdb.GetChatSubagentByChatID(context.Background(), uuid.New())
	require.ErrorIs(t, err, sql.ErrNoRows)
}
