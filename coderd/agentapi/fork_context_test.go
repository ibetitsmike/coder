package agentapi_test

// This file is fork code: it tests persistence of the fork-only subagent body.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"cdr.dev/slog/v3/sloggers/slogtest"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/agentapi"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/quartz"
)

func TestPushContextStatePersistsSubagentBody(t *testing.T) {
	t.Parallel()

	agentID := uuid.New()
	dbm := dbmock.NewMockStore(gomock.NewController(t))
	api := &agentapi.ContextAPI{
		AgentID:  agentID,
		Log:      slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		Clock:    quartz.NewMock(t),
		Database: dbm,
	}

	dbm.EXPECT().InTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(f func(database.Store) error, _ *database.TxOptions) error { return f(dbm) },
	)
	dbm.EXPECT().GetLatestWorkspaceAgentContextSnapshot(gomock.Any(), agentID).
		Return(database.WorkspaceAgentContextSnapshot{}, sql.ErrNoRows)
	dbm.EXPECT().UpsertWorkspaceAgentContextSnapshot(gomock.Any(), gomock.Any()).
		Return(database.WorkspaceAgentContextSnapshot{}, nil)
	var got database.UpsertWorkspaceAgentContextResourceParams
	dbm.EXPECT().UpsertWorkspaceAgentContextResource(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg database.UpsertWorkspaceAgentContextResourceParams) (database.WorkspaceAgentContextResource, error) {
			got = arg
			return database.WorkspaceAgentContextResource{}, nil
		})
	dbm.EXPECT().DeleteStaleWorkspaceAgentContextResources(gomock.Any(), gomock.Any()).Return(nil)

	want := &agentproto.SubagentBody{
		Definition:  []byte("---\nname: reviewer\ndescription: Reviews diffs.\n---\nReview."),
		Name:        "reviewer",
		Description: "Reviews diffs.",
	}
	resp, err := api.PushContextState(context.Background(), &agentproto.PushContextStateRequest{
		Version: 1,
		Initial: true,
		Resources: []*agentproto.ContextResource{{
			Source:      "/home/coder/.claude/agents/reviewer.md",
			ContentHash: []byte{0x01},
			Status:      agentproto.ContextResource_OK,
			Error:       `unknown front-matter field "foo" ignored`,
			Body:        &agentproto.ContextResource_Subagent{Subagent: want},
		}},
	})
	require.NoError(t, err)
	require.True(t, resp.GetAccepted())

	require.Equal(t, database.WorkspaceAgentContextBodyKindSubagent, got.BodyKind)
	require.Equal(t, `unknown front-matter field "foo" ignored`, got.Error)
	var body agentproto.SubagentBody
	require.NoError(t, protojson.Unmarshal(got.Body, &body))
	require.Equal(t, want.GetDefinition(), body.GetDefinition())
	require.Equal(t, want.GetName(), body.GetName())
	require.Equal(t, want.GetDescription(), body.GetDescription())
}
