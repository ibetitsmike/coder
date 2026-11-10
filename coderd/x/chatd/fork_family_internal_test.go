package chatd

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkChatFamilyCapacity(t *testing.T) {
	t.Parallel()
	f := newCapacityWaitFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	// The upstream pool is unlocked, so only the fork limits can queue.
	p := &Server{
		db:                     f.db,
		experiments:            codersdk.Experiments{codersdk.ExperimentForkNestedSubagents},
		inFlightChatStaleAfter: 30 * time.Second,
		agentCapacityLimiter:   newAgentCapacityLimiter(staticAgentCapacityUnlock(true), 30),
		chatLimits:             Limits{Fork: ForkLimits{MaxLiveSubagentsPerFamily: 5}},
	}

	got, err := p.ForkChatFamilyCapacity(ctx, f.root)
	require.NoError(t, err)
	require.Equal(t, codersdk.ForkChatFamilyCapacity{
		LiveSubagents:      2,
		MaxLiveSubagents:   5,
		WaitingForCapacity: []uuid.UUID{f.waiting.ID},
	}, got)

	// The occupant's owner is gone, so the family's only slot is free and
	// neither subagent waits for it.
	require.NoError(t, f.db.DeleteAllChatHeartbeats(ctx, f.occupant.ID))
	got, err = p.ForkChatFamilyCapacity(ctx, f.root)
	require.NoError(t, err)
	require.Empty(t, got.WaitingForCapacity)
}
