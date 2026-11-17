package coderd_test

// This file is fork code: it tests hash-only context rows
// (fork-context-hash-rows) end to end, from the agent's pusher through the
// agent RPC and dbauthz to the stored rows (issue #306).

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/agent/agentcontext"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbfake"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/agentsdk"
	"github.com/coder/coder/v2/testutil"
)

// recordingAgentClient forwards pushes to coderd and keeps a copy of each.
type recordingAgentClient struct {
	agentproto.DRPCAgentClient210
	reqs []*agentproto.PushContextStateRequest
}

func (c *recordingAgentClient) PushContextState(ctx context.Context, req *agentproto.PushContextStateRequest) (*agentproto.PushContextStateResponse, error) {
	//nolint:forcetypeassert // proto.Clone returns the type it is given.
	c.reqs = append(c.reqs, proto.Clone(req).(*agentproto.PushContextStateRequest))
	return c.DRPCAgentClient210.PushContextState(ctx, req)
}

func hashRowsSkills(n int) []agentcontext.Resource {
	out := make([]agentcontext.Resource, 0, n)
	for i := range n {
		name := fmt.Sprintf("skill-%03d", i)
		meta := "---\nname: " + name + "\n---\n" + strings.Repeat("Step. ", 20)
		out = append(out, agentcontext.Resource{
			Kind:        agentcontext.KindSkill,
			Source:      "/home/coder/.agents/skills/" + name,
			ContentHash: [32]byte{byte(i), byte(i >> 8)},
			Payload:     []byte(meta),
			SizeBytes:   uint64(len(meta)),
			Status:      agentcontext.StatusOK,
			Name:        name,
		})
	}
	return out
}

func requestBodies(req *agentproto.PushContextStateRequest) int {
	n := 0
	for _, r := range req.GetResources() {
		if len(r.GetForkBodyDigest()) == 0 {
			n++
		}
	}
	return n
}

func TestForkContextHashRows(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		experiments []string
		on          bool
	}{
		{name: "Off"},
		{name: "On", experiments: []string{string(codersdk.ExperimentForkContextHashRows)}, on: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client, db := coderdtest.NewWithDatabase(t, &coderdtest.Options{
				DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
					dv.Experiments = tc.experiments
				}),
			})
			user := coderdtest.CreateFirstUser(t, client)
			r := dbfake.WorkspaceBuild(t, db, database.WorkspaceTable{
				OrganizationID: user.OrganizationID,
				OwnerID:        user.UserID,
			}).WithAgent().Do()
			agentID := r.Agents[0].ID

			ctx := testutil.Context(t, testutil.WaitLong)
			agentClient := agentsdk.New(client.URL, agentsdk.WithFixedToken(r.AgentToken))
			aAPI, _, err := agentClient.ConnectRPC210(ctx)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, aAPI.DRPCConn().Close())
			}()
			rec := &recordingAgentClient{DRPCAgentClient210: aAPI}
			pusher := agentcontext.NewDRPCPusher(rec)

			skills := hashRowsSkills(200)
			resp, err := pusher.PushContextState(ctx, &agentcontext.PushRequest{Version: 1, Initial: true, Resources: skills})
			require.NoError(t, err)
			require.True(t, resp.Accepted)

			changed := append([]agentcontext.Resource(nil), skills...)
			changed[7].Payload = []byte("---\nname: skill-007\n---\n" + strings.Repeat("New step. ", 20))
			changed[7].ContentHash = [32]byte{0xff}
			resp, err = pusher.PushContextState(ctx, &agentcontext.PushRequest{Version: 2, Resources: changed})
			require.NoError(t, err)
			require.True(t, resp.Accepted)

			require.Len(t, rec.reqs, 2)
			require.Equal(t, 200, requestBodies(rec.reqs[0]))
			if tc.on {
				require.Equal(t, 1, requestBodies(rec.reqs[1]), "only the changed skill carries a body")
			} else {
				require.Equal(t, 200, requestBodies(rec.reqs[1]), "without the experiment every body is sent")
			}

			// The stored rows hold every body as if each push sent it in
			// full.
			assertStored := func() {
				t.Helper()
				rows, err := db.ListWorkspaceAgentContextResources(dbauthz.AsSystemRestricted(ctx), agentID)
				require.NoError(t, err)
				require.Len(t, rows, 200)
				bySource := map[string]database.WorkspaceAgentContextResource{}
				for _, row := range rows {
					bySource[row.Source] = row
				}
				for _, want := range changed {
					row, ok := bySource[want.Source]
					require.True(t, ok, want.Source)
					var body agentproto.SkillMetaBody
					require.NoError(t, protojson.Unmarshal(row.Body, &body))
					require.Equal(t, want.Payload, body.GetMeta(), want.Source)
					require.Equal(t, want.Name, body.GetName(), want.Source)
					require.Equal(t, want.ContentHash[:], row.ContentHash, want.Source)
				}
			}
			assertStored()

			// A hash-only row whose digest does not match the stored body
			// fails the push and stores nothing.
			forged := proto.Clone(rec.reqs[0]).(*agentproto.PushContextStateRequest) //nolint:forcetypeassert // proto.Clone returns the type it is given.
			forged.Version = 3
			forged.Initial = false
			digest, err := agentproto.ForkContextBodyDigest(agentproto.ForkContextBody(forged.Resources[7]))
			require.NoError(t, err, "the digest of the original body, which coderd no longer stores")
			agentproto.ForkResetContextBody(forged.Resources[7])
			forged.Resources[7].ForkBodyDigest = digest
			_, err = aAPI.PushContextState(ctx, forged)
			if tc.on {
				require.ErrorContains(t, err, "stored body does not match the body digest")
			} else {
				require.ErrorContains(t, err, "fork-context-hash-rows is not enabled")
			}
			assertStored()

			// A dropped replay carries the flag too.
			replay, err := aAPI.PushContextState(ctx, &agentproto.PushContextStateRequest{Version: 2})
			require.NoError(t, err)
			require.False(t, replay.GetAccepted())
			require.Equal(t, tc.on, replay.GetForkContextHashRows())
		})
	}
}
