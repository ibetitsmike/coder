package agentapi_test

// This file is fork code: it tests persistence of the fork-only subagent body
// and that the agent bounds error text to fit coderd's push caps.

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentcontext"
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

// contextAPIClient forwards the agent's PushContextState call straight into
// coderd's handler, so a test exercises the agent's wire conversion against
// coderd's real validation.
type contextAPIClient struct {
	agentproto.DRPCAgentClient210
	api *agentapi.ContextAPI
}

func (c contextAPIClient) PushContextState(ctx context.Context, req *agentproto.PushContextStateRequest) (*agentproto.PushContextStateResponse, error) {
	return c.api.PushContextState(ctx, req)
}

// Resource and snapshot errors can quote user-controlled text far past
// coderd's 4096-byte error cap; coderd rejects the whole push when one does.
func TestPushContextStateAcceptsAgentOversizedErrors(t *testing.T) {
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
	var snapshot database.UpsertWorkspaceAgentContextSnapshotParams
	dbm.EXPECT().UpsertWorkspaceAgentContextSnapshot(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg database.UpsertWorkspaceAgentContextSnapshotParams) (database.WorkspaceAgentContextSnapshot, error) {
			snapshot = arg
			return database.WorkspaceAgentContextSnapshot{}, nil
		})
	var resources []database.UpsertWorkspaceAgentContextResourceParams
	dbm.EXPECT().UpsertWorkspaceAgentContextResource(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg database.UpsertWorkspaceAgentContextResourceParams) (database.WorkspaceAgentContextResource, error) {
			resources = append(resources, arg)
			return database.WorkspaceAgentContextResource{}, nil
		}).Times(2)
	dbm.EXPECT().DeleteStaleWorkspaceAgentContextResources(gomock.Any(), gomock.Any()).Return(nil)

	skillErr := `skill name "` + strings.Repeat("é", 2500) + `" is not kebab-case`
	symlinkErr := "resolve symlink: " + strings.Repeat("/long-target", 400)
	pusher := agentcontext.NewDRPCPusher(contextAPIClient{api: api})
	resp, err := pusher.PushContextState(context.Background(), &agentcontext.PushRequest{
		Version:       1,
		Initial:       true,
		SnapshotError: "watcher degraded: " + strings.Repeat("x", 5000),
		Resources: []agentcontext.Resource{
			{
				Kind:   agentcontext.KindSkill,
				Source: "/home/coder/.agents/skills/bad",
				Status: agentcontext.StatusInvalid,
				Error:  skillErr,
			},
			{
				Kind:   agentcontext.KindInstructionFile,
				Source: "/home/coder/AGENTS.md",
				Status: agentcontext.StatusUnreadable,
				Error:  symlinkErr,
			},
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Accepted)

	require.Len(t, resources, 2)
	for _, got := range []string{snapshot.SnapshotError, resources[0].Error, resources[1].Error} {
		require.LessOrEqual(t, len(got), 1024)
		require.True(t, utf8.ValidString(got), "truncation must keep valid UTF-8")
		require.True(t, strings.HasSuffix(got, " (truncated)"))
	}
	for _, got := range resources {
		if got.Source == "/home/coder/.agents/skills/bad" {
			require.True(t, strings.HasPrefix(got.Error, skillErr[:500]))
		}
	}
}
