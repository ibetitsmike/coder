package coderd_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// TestForkChatRoutes checks the fork chat mount with no fork experiment
// enabled. The fork paths require authentication and then answer 404 from the
// fork subrouter instead of falling into the /chats wildcard, where parsing
// {chat} as a UUID answers 400.
func TestForkChatRoutes(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client := coderdtest.New(t, nil)
	_ = coderdtest.CreateFirstUser(t, client)
	anonymous := codersdk.New(client.URL)

	for _, path := range []string{
		"/api/v2/chats/fork",
		"/api/v2/chats/fork/anything",
		"/api/v2/chats/fork/anything/deeper",
	} {
		res, err := client.Request(ctx, http.MethodGet, path, nil)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, path)

		res, err = anonymous.Request(ctx, http.MethodGet, path, nil)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, path)
	}
}
