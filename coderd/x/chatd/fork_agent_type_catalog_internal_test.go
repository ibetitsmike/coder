package chatd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
)

func TestForkAgentTypeCatalogComputerUse(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{Anthropic: "test-anthropic-key"})
	user, org, _ := seedInternalChatDeps(t, db)
	types, err := server.AgentTypeCatalog(chatdTestContext(t), AgentTypeCatalogOptions{OwnerID: user.ID, OrganizationID: org.ID})
	require.NoError(t, err)
	require.Len(t, types, 3)
	require.Equal(t, subagentTypeComputerUse, types[2].Name)
	require.False(t, types[2].RootAvailable)
	require.Equal(t, `type "computer_use" cannot start a chat`, types[2].RootUnavailableReason)
}
