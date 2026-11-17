package agentapi_test

// This file is fork code: it tests that coderd fills hash-only context rows
// (fork-context-hash-rows) from its stored bodies and rejects every row it
// cannot fill.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"cdr.dev/slog/v3/sloggers/slogtest"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/agentapi"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/quartz"
)

func hashRowsAPI(t *testing.T, on bool) (*agentapi.ContextAPI, *dbmock.MockStore) {
	t.Helper()
	dbm := dbmock.NewMockStore(gomock.NewController(t))
	return &agentapi.ContextAPI{
		AgentID:             uuid.New(),
		Log:                 slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		Clock:               quartz.NewMock(t),
		Database:            dbm,
		ForkContextHashRows: on,
	}, dbm
}

func hashRowSkill(source, name string) *agentproto.ContextResource {
	return &agentproto.ContextResource{
		Source:      source,
		ContentHash: []byte{0x01, 0x02},
		Status:      agentproto.ContextResource_OK,
		SizeBytes:   100,
		Body: &agentproto.ContextResource_Skill{Skill: &agentproto.SkillMetaBody{
			Meta:        []byte("---\nname: " + name + "\n---\n" + strings.Repeat("Step. ", 20)),
			Name:        name,
			Description: "Does " + name + ".",
		}},
	}
}

// storedRow is the row a previous push of r left in the database.
func storedRow(t *testing.T, agentID uuid.UUID, r *agentproto.ContextResource, kind database.WorkspaceAgentContextBodyKind) database.WorkspaceAgentContextResource {
	t.Helper()
	body, err := protojson.Marshal(agentproto.ForkContextBody(r))
	require.NoError(t, err)
	return database.WorkspaceAgentContextResource{
		WorkspaceAgentID: agentID,
		Source:           r.GetSource(),
		BodyKind:         kind,
		Body:             body,
		ContentHash:      r.GetContentHash(),
		Status:           database.WorkspaceAgentContextResourceStatusOk,
	}
}

// elide returns the hash-only form of r that an agent sends.
func elide(t *testing.T, r *agentproto.ContextResource) *agentproto.ContextResource {
	t.Helper()
	digest, err := agentproto.ForkContextBodyDigest(agentproto.ForkContextBody(r))
	require.NoError(t, err)
	//nolint:forcetypeassert // proto.Clone returns the type it is given.
	out := proto.Clone(r).(*agentproto.ContextResource)
	agentproto.ForkResetContextBody(out)
	out.ForkBodyDigest = digest
	return out
}

func TestPushContextStateFillsHashRows(t *testing.T) {
	t.Parallel()
	api, dbm := hashRowsAPI(t, true)

	kept := hashRowSkill("/home/coder/.agents/skills/kept", "kept")
	changed := hashRowSkill("/home/coder/.agents/skills/changed", "changed")
	dbm.EXPECT().ListWorkspaceAgentContextResources(gomock.Any(), api.AgentID).Return([]database.WorkspaceAgentContextResource{
		storedRow(t, api.AgentID, kept, database.WorkspaceAgentContextBodyKindSkill),
	}, nil)
	dbm.EXPECT().InTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(f func(database.Store) error, _ *database.TxOptions) error { return f(dbm) },
	)
	dbm.EXPECT().GetLatestWorkspaceAgentContextSnapshot(gomock.Any(), api.AgentID).
		Return(database.WorkspaceAgentContextSnapshot{}, sql.ErrNoRows)
	dbm.EXPECT().UpsertWorkspaceAgentContextSnapshot(gomock.Any(), gomock.Any()).
		Return(database.WorkspaceAgentContextSnapshot{}, nil)
	written := map[string]database.UpsertWorkspaceAgentContextResourceParams{}
	dbm.EXPECT().UpsertWorkspaceAgentContextResource(gomock.Any(), gomock.Any()).Times(2).
		DoAndReturn(func(_ context.Context, arg database.UpsertWorkspaceAgentContextResourceParams) (database.WorkspaceAgentContextResource, error) {
			written[arg.Source] = arg
			return database.WorkspaceAgentContextResource{}, nil
		})
	dbm.EXPECT().DeleteStaleWorkspaceAgentContextResources(gomock.Any(), gomock.Any()).Return(nil)

	hashOnly := elide(t, kept)
	hashOnly.Status = agentproto.ContextResource_OK
	hashOnly.Error = "a warning sent with the hash-only row"
	resp, err := api.PushContextState(context.Background(), &agentproto.PushContextStateRequest{
		Version:   2,
		Resources: []*agentproto.ContextResource{hashOnly, changed},
	})
	require.NoError(t, err)
	require.True(t, resp.GetAccepted())
	require.True(t, resp.GetForkContextHashRows())

	for _, want := range []*agentproto.ContextResource{kept, changed} {
		row, ok := written[want.GetSource()]
		require.True(t, ok)
		require.Equal(t, database.WorkspaceAgentContextBodyKindSkill, row.BodyKind)
		var got agentproto.SkillMetaBody
		require.NoError(t, protojson.Unmarshal(row.Body, &got))
		require.True(t, proto.Equal(want.GetSkill(), &got), "row %q has body %v", want.GetSource(), &got)
	}
	require.Equal(t, "a warning sent with the hash-only row", written[kept.GetSource()].Error,
		"fields outside the body come from the push")
}

func TestPushContextStateRejectsUnfillableHashRows(t *testing.T) {
	t.Parallel()
	skill := hashRowSkill("/home/coder/.agents/skills/a", "a")
	for _, tc := range []struct {
		name string
		off  bool
		// stored returns the rows the database holds; nil skips the read.
		stored  func(t *testing.T, agentID uuid.UUID) []database.WorkspaceAgentContextResource
		request func(t *testing.T) *agentproto.ContextResource
		wantErr string
	}{
		{
			name:    "ExperimentOff",
			off:     true,
			request: func(t *testing.T) *agentproto.ContextResource { return elide(t, skill) },
			wantErr: "fork-context-hash-rows is not enabled",
		},
		{
			name:    "NoStoredRow",
			stored:  func(*testing.T, uuid.UUID) []database.WorkspaceAgentContextResource { return nil },
			request: func(t *testing.T) *agentproto.ContextResource { return elide(t, skill) },
			wantErr: "no stored row",
		},
		{
			name: "StoredKindDiffers",
			stored: func(t *testing.T, agentID uuid.UUID) []database.WorkspaceAgentContextResource {
				return []database.WorkspaceAgentContextResource{storedRow(t, agentID, skill, database.WorkspaceAgentContextBodyKindSubagent)}
			},
			request: func(t *testing.T) *agentproto.ContextResource { return elide(t, skill) },
			wantErr: "stored row has kind subagent",
		},
		{
			name: "StoredBodyDiffers",
			stored: func(t *testing.T, agentID uuid.UUID) []database.WorkspaceAgentContextResource {
				older := hashRowSkill(skill.GetSource(), "older")
				return []database.WorkspaceAgentContextResource{storedRow(t, agentID, older, database.WorkspaceAgentContextBodyKindSkill)}
			},
			request: func(t *testing.T) *agentproto.ContextResource { return elide(t, skill) },
			wantErr: "stored body does not match the body digest",
		},
		{
			name: "StoredBodyUndecodable",
			stored: func(t *testing.T, agentID uuid.UUID) []database.WorkspaceAgentContextResource {
				row := storedRow(t, agentID, skill, database.WorkspaceAgentContextBodyKindSkill)
				row.Body = []byte(`{"unknown_field": 1}`)
				return []database.WorkspaceAgentContextResource{row}
			},
			request: func(t *testing.T) *agentproto.ContextResource { return elide(t, skill) },
			wantErr: "decode stored body",
		},
		{
			name: "ShortDigest",
			stored: func(t *testing.T, agentID uuid.UUID) []database.WorkspaceAgentContextResource {
				return []database.WorkspaceAgentContextResource{storedRow(t, agentID, skill, database.WorkspaceAgentContextBodyKindSkill)}
			},
			request: func(t *testing.T) *agentproto.ContextResource {
				r := elide(t, skill)
				r.ForkBodyDigest = r.ForkBodyDigest[:16]
				return r
			},
			wantErr: "body digest is 16 bytes",
		},
		{
			name: "BodyAndDigest",
			stored: func(t *testing.T, agentID uuid.UUID) []database.WorkspaceAgentContextResource {
				return []database.WorkspaceAgentContextResource{storedRow(t, agentID, skill, database.WorkspaceAgentContextBodyKindSkill)}
			},
			request: func(t *testing.T) *agentproto.ContextResource {
				//nolint:forcetypeassert // proto.Clone returns the type it is given.
				r := proto.Clone(skill).(*agentproto.ContextResource)
				r.ForkBodyDigest = elide(t, skill).GetForkBodyDigest()
				return r
			},
			wantErr: "carries both a body and a body digest",
		},
		{
			name: "NoBodyVariant",
			stored: func(t *testing.T, agentID uuid.UUID) []database.WorkspaceAgentContextResource {
				return []database.WorkspaceAgentContextResource{storedRow(t, agentID, skill, database.WorkspaceAgentContextBodyKindSkill)}
			},
			request: func(t *testing.T) *agentproto.ContextResource {
				r := elide(t, skill)
				r.Body = nil
				return r
			},
			wantErr: "missing or unknown body variant",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The mock fails the test on any write: a rejected push
			// stores nothing.
			api, dbm := hashRowsAPI(t, !tc.off)
			if tc.stored != nil {
				dbm.EXPECT().ListWorkspaceAgentContextResources(gomock.Any(), api.AgentID).Return(tc.stored(t, api.AgentID), nil)
			}
			_, err := api.PushContextState(context.Background(), &agentproto.PushContextStateRequest{
				Version:   2,
				Resources: []*agentproto.ContextResource{tc.request(t)},
			})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// Filled bodies count against the push caps like bodies sent in full, so
// hash-only rows cannot grow a snapshot past the aggregate cap.
func TestPushContextStateHashRowsKeepAggregateCap(t *testing.T) {
	t.Parallel()
	api, dbm := hashRowsAPI(t, true)

	var stored []database.WorkspaceAgentContextResource
	var resources []*agentproto.ContextResource
	for i := range 22 {
		r := &agentproto.ContextResource{
			Source: "/home/coder/rules/" + strings.Repeat("r", i+1) + ".md",
			Status: agentproto.ContextResource_OK,
			Body: &agentproto.ContextResource_InstructionFile{InstructionFile: &agentproto.InstructionFileBody{
				Content: []byte(strings.Repeat("x", 150*1024)),
			}},
		}
		stored = append(stored, storedRow(t, api.AgentID, r, database.WorkspaceAgentContextBodyKindInstructionFile))
		resources = append(resources, elide(t, r))
	}
	dbm.EXPECT().ListWorkspaceAgentContextResources(gomock.Any(), api.AgentID).Return(stored, nil)

	_, err := api.PushContextState(context.Background(), &agentproto.PushContextStateRequest{
		Version:   2,
		Resources: resources,
	})
	require.ErrorContains(t, err, "aggregate body size exceeds")
}

// The response carries the capability exactly when the experiment is on.
func TestPushContextStateAdvertisesForkContextHashRows(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		api, dbm := hashRowsAPI(t, on)
		dbm.EXPECT().InTx(gomock.Any(), gomock.Any()).DoAndReturn(
			func(f func(database.Store) error, _ *database.TxOptions) error { return f(dbm) },
		)
		resp, err := api.PushContextState(context.Background(), &agentproto.PushContextStateRequest{
			Version:              1,
			CapabilityGeneration: 1,
		})
		require.NoError(t, err)
		require.True(t, resp.GetAccepted())
		require.Equal(t, on, resp.GetForkContextHashRows())
	}
}
