package forkdb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestInsertChatSubagent(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID,
	})
	seed := forkdb.ChatSubagent{
		ChatID:       chat.ID,
		AgentType:    "explore",
		Depth:        2,
		Isolation:    "worktree",
		WorktreePath: "/home/coder/.worktrees/explore",
		Branch:       "mike/explore",
	}
	sub := dbgen.ChatSubagent(t, db, seed)
	require.Equal(t, seed, sub)

	got, err := fdb.GetChatSubagentByChatID(ctx, chat.ID)
	require.NoError(t, err)
	require.Equal(t, sub, got)

	_, err = fdb.InsertChatSubagent(ctx, forkdb.InsertChatSubagentParams{
		ChatID:    chat.ID,
		AgentType: "general",
		Depth:     1,
		Isolation: "shared",
	})
	require.True(t, database.IsUniqueViolation(err), "a chat has at most one subagent row: %v", err)
}
