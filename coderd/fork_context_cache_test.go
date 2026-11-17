package coderd_test

// This file is fork code: it tests that the fork-context-cache experiment
// reaches agents through the push response (issue #306).

import (
	"testing"

	"github.com/stretchr/testify/require"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbfake"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/agentsdk"
	"github.com/coder/coder/v2/testutil"
)

// The push response turns the agent's resolve cache on only while the
// experiment is on. It ships off.
func TestForkContextCacheAdvertisement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		experiments []string
		want        bool
	}{
		{name: "Off", want: false},
		{name: "On", experiments: []string{string(codersdk.ExperimentForkContextCache)}, want: true},
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

			ctx := testutil.Context(t, testutil.WaitLong)
			agentClient := agentsdk.New(client.URL, agentsdk.WithFixedToken(r.AgentToken))
			aAPI, _, err := agentClient.ConnectRPC210(ctx)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, aAPI.DRPCConn().Close())
			}()

			resp, err := aAPI.PushContextState(ctx, &agentproto.PushContextStateRequest{
				Version:       1,
				Initial:       true,
				AggregateHash: []byte{0x01, 0x02},
			})
			require.NoError(t, err)
			require.True(t, resp.GetAccepted())
			require.Equal(t, tc.want, resp.GetForkContextCache())

			// A dropped replay carries the flag too, so the agent does not
			// read it as a retraction.
			resp, err = aAPI.PushContextState(ctx, &agentproto.PushContextStateRequest{
				Version:       1,
				AggregateHash: []byte{0x01, 0x02},
			})
			require.NoError(t, err)
			require.False(t, resp.GetAccepted())
			require.Equal(t, tc.want, resp.GetForkContextCache())
		})
	}
}
