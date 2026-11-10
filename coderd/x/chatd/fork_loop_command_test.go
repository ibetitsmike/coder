package chatd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/agenthooks"
	"github.com/coder/coder/v2/testutil"
)

// A user_prompt_submit hook sees the /loop command as submitted, so it can
// deny or audit scheduled execution; the chat stores only the prompt.
func TestLoopCommandPromptHookSeesSubmission(t *testing.T) {
	t.Parallel()
	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	user, org, model := seedChatDependencies(t, db)
	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		LastModelConfigID: model.ID,
	})
	var received agenthooks.Request
	consumer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		_, err := w.Write([]byte(`{}`))
		require.NoError(t, err)
	}))
	t.Cleanup(consumer.Close)
	server := newTestServer(t, db, ps, uuid.New(), func(cfg *chatd.Config) {
		cfg.HookDispatcher = newHookDispatcher(t, db, consumer)
		cfg.Experiments = append(slices.Clone(codersdk.ExperimentsKnown), codersdk.ExperimentForkChatWakeups)
	})

	result, err := server.SendMessage(ctx, chatd.SendMessageOptions{
		ChatID:  chat.ID,
		Content: []codersdk.ChatMessagePart{codersdk.ChatMessageText("/loop 5m check CI")},
	})
	require.NoError(t, err)
	require.Equal(t, "check CI", hookMessageText(t, result.Message))
	require.Equal(t, agenthooks.EventUserPromptSubmit, received.Type)
	require.Equal(t, "/loop 5m check CI", decodeHookData[agenthooks.UserPromptSubmitData](t, received).Prompt)
}
