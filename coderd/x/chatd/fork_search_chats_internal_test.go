package chatd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestSearchChats(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	// The authorizing store is what a turn uses, so a search that skips the
	// owner check would find every chat as chatd.
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkChatTodos}))
	user, org, model := seedInternalChatDeps(t, db)
	other := dbgen.User(t, db, database.User{})
	dbgen.OrganizationMember(t, db, database.OrganizationMember{UserID: other.ID, OrganizationID: org.ID})

	newChat := func(t *testing.T, ownerID uuid.UUID, title string, parent uuid.NullUUID) database.Chat {
		t.Helper()
		return dbgen.Chat(t, db, database.Chat{
			OrganizationID:    org.ID,
			OwnerID:           ownerID,
			LastModelConfigID: model.ID,
			Title:             title,
			ParentChatID:      parent,
			RootChatID:        parent,
		})
	}
	// search runs the tool the way a turn does, as chatd, which can read
	// every chat, so only the owner check can hide a chat.
	search := func(t *testing.T, chat database.Chat, args searchChatsArgs, wantError bool) string {
		t.Helper()
		tools, err := server.appendForkTools(chatdTestContext(t), nil, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		return runChatTodoTool(chatdTestContext(t), t, tools, searchChatsToolName, args, wantError)
	}
	searchPage := func(t *testing.T, chat database.Chat, args searchChatsArgs) searchChatsResult {
		t.Helper()
		var page searchChatsResult
		require.NoError(t, json.Unmarshal([]byte(search(t, chat, args, false)), &page))
		return page
	}
	ids := func(page searchChatsResult) []uuid.UUID {
		out := []uuid.UUID{}
		for _, chat := range page.Chats {
			out = append(out, chat.ChatID)
		}
		return out
	}
	uniqueWord := func() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

	t.Run("FindsTheOwnersTopLevelChats", func(t *testing.T) {
		t.Parallel()
		word := uniqueWord()
		alpha := newChat(t, user.ID, word+" alpha", uuid.NullUUID{})
		beta := newChat(t, user.ID, word+" beta", uuid.NullUUID{})
		newChat(t, user.ID, word+" child", uuid.NullUUID{UUID: alpha.ID, Valid: true})
		newChat(t, other.ID, word+" private", uuid.NullUUID{})

		all := searchPage(t, alpha, searchChatsArgs{Query: "title:" + word})
		require.ElementsMatch(t, []uuid.UUID{alpha.ID, beta.ID}, ids(all))
		require.False(t, all.HasMore)
		require.NotZero(t, all.Chats[0].UpdatedAt)

		first := searchPage(t, alpha, searchChatsArgs{Query: "title:" + word, Limit: 1})
		require.Len(t, first.Chats, 1)
		require.True(t, first.HasMore)
		require.Equal(t, first.Chats[0].ChatID.String(), first.NextAfterID)
		second := searchPage(t, alpha, searchChatsArgs{Query: "title:" + word, Limit: 1, AfterID: first.NextAfterID})
		require.ElementsMatch(t, []uuid.UUID{alpha.ID, beta.ID}, append(ids(first), ids(second)...))
		require.False(t, second.HasMore)
		require.Empty(t, second.NextAfterID)
	})

	t.Run("FindsChatsSharedWithTheOwner", func(t *testing.T) {
		t.Parallel()
		word := uniqueWord()
		chat := newChat(t, user.ID, word+" mine", uuid.NullUUID{})
		shared := newChat(t, other.ID, word+" shared", uuid.NullUUID{})
		newChat(t, other.ID, word+" private", uuid.NullUUID{})
		query := "title:" + word + " source:created_by_me,shared_with_me"

		require.Equal(t, []uuid.UUID{chat.ID}, ids(searchPage(t, chat, searchChatsArgs{Query: query})))

		err := db.UpdateChatACLByID(systemRestrictedTestContext(t), database.UpdateChatACLByIDParams{
			ID:       shared.ID,
			UserACL:  database.ChatACL{user.ID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
			GroupACL: database.ChatACL{},
		})
		require.NoError(t, err)
		page := searchPage(t, chat, searchChatsArgs{Query: query})
		require.ElementsMatch(t, []uuid.UUID{chat.ID, shared.ID}, ids(page))
		for _, entry := range page.Chats {
			require.Equal(t, entry.ChatID == shared.ID, entry.Shared)
		}
	})

	t.Run("RejectsInvalidArguments", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t, user.ID, uniqueWord(), uuid.NullUUID{})
		for _, args := range []searchChatsArgs{
			{Query: "deploy"},
			{Query: "search:refactor title:deploy"},
			{Limit: maxSearchChats + 1},
			{AfterID: "not-a-chat"},
		} {
			search(t, chat, args, true)
		}
	})
}
