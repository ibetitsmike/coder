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
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestReadChatTranscript(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	// The authorizing store is what a turn uses, so a read that skips the
	// owner check would succeed as chatd.
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkChatTodos}))
	user, org, model := seedInternalChatDeps(t, db)
	other := dbgen.User(t, db, database.User{})
	dbgen.OrganizationMember(t, db, database.OrganizationMember{UserID: other.ID, OrganizationID: org.ID})

	newChat := func(t *testing.T, ownerID uuid.UUID, parent, root uuid.NullUUID) database.Chat {
		t.Helper()
		return dbgen.Chat(t, db, database.Chat{
			OrganizationID:    org.ID,
			OwnerID:           ownerID,
			LastModelConfigID: model.ID,
			ParentChatID:      parent,
			RootChatID:        root,
		})
	}
	type row struct {
		role       database.ChatMessageRole
		visibility database.ChatMessageVisibility
		parts      []codersdk.ChatMessagePart
	}
	insert := func(t *testing.T, chatID uuid.UUID, rows ...row) {
		t.Helper()
		params := database.InsertChatMessagesParams{ChatID: chatID}
		for _, r := range rows {
			content, err := chatprompt.MarshalParts(r.parts)
			require.NoError(t, err)
			params.CreatedBy = append(params.CreatedBy, user.ID)
			params.ModelConfigID = append(params.ModelConfigID, model.ID)
			params.ReasoningEffort = append(params.ReasoningEffort, "")
			params.Role = append(params.Role, r.role)
			params.Content = append(params.Content, string(content.RawMessage))
			params.ContentVersion = append(params.ContentVersion, chatprompt.CurrentContentVersion)
			params.Visibility = append(params.Visibility, r.visibility)
			params.InputTokens = append(params.InputTokens, 0)
			params.OutputTokens = append(params.OutputTokens, 0)
			params.TotalTokens = append(params.TotalTokens, 0)
			params.ReasoningTokens = append(params.ReasoningTokens, 0)
			params.CacheCreationTokens = append(params.CacheCreationTokens, 0)
			params.CacheReadTokens = append(params.CacheReadTokens, 0)
			params.ContextLimit = append(params.ContextLimit, 0)
			params.Compressed = append(params.Compressed, false)
			params.RuntimeMs = append(params.RuntimeMs, 0)
		}
		_, err := db.InsertChatMessages(chatdTestContext(t), params)
		require.NoError(t, err)
	}
	userText := func(text string) row {
		return row{role: database.ChatMessageRoleUser, visibility: database.ChatMessageVisibilityBoth, parts: []codersdk.ChatMessagePart{codersdk.ChatMessageText(text)}}
	}
	// read runs the tool the way a turn does, as chatd, which can read
	// every chat, so only the owner check can refuse a read.
	read := func(t *testing.T, chat database.Chat, args readChatTranscriptArgs, wantError bool) string {
		t.Helper()
		tools, err := server.appendForkTools(chatdTestContext(t), nil, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		return runChatTodoTool(chatdTestContext(t), t, tools, readChatTranscriptToolName, args, wantError)
	}
	readPage := func(t *testing.T, chat database.Chat, args readChatTranscriptArgs) chatTranscriptResult {
		t.Helper()
		var page chatTranscriptResult
		require.NoError(t, json.Unmarshal([]byte(read(t, chat, args, false)), &page))
		return page
	}
	texts := func(page chatTranscriptResult) []string {
		var out []string
		for _, message := range page.Messages {
			for _, part := range message.Parts {
				out = append(out, string(message.Role)+":"+string(part.Type)+":"+part.Text)
			}
		}
		return out
	}

	t.Run("PagesTheUserVisibleMessages", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t, user.ID, uuid.NullUUID{}, uuid.NullUUID{})
		reasoning := codersdk.ChatMessageReasoning("thinking")
		reasoning.ProviderMetadata = json.RawMessage(`{"anthropic":{"signature":"secret-signature"}}`)
		reasoning.ProviderCredential = "secret-credential"
		reasoning.ProviderIdentity = "secret-identity"
		insert(t, chat.ID,
			userText("first question"),
			row{role: database.ChatMessageRoleAssistant, visibility: database.ChatMessageVisibilityBoth, parts: []codersdk.ChatMessagePart{reasoning, codersdk.ChatMessageText("answer")}},
			row{role: database.ChatMessageRoleUser, visibility: database.ChatMessageVisibilityModel, parts: []codersdk.ChatMessagePart{codersdk.ChatMessageText("model-only context")}},
			row{role: database.ChatMessageRoleTool, visibility: database.ChatMessageVisibilityUser, parts: []codersdk.ChatMessagePart{codersdk.ChatMessageText("secret-user-projection")}},
			row{role: database.ChatMessageRoleUser, visibility: database.ChatMessageVisibilityBoth, parts: []codersdk.ChatMessagePart{
				codersdk.ChatMessageText("second question"),
				{Type: codersdk.ChatMessagePartTypeHookNotice, Text: "secret-hook-notice"},
			}},
			row{role: database.ChatMessageRoleAssistant, visibility: database.ChatMessageVisibilityBoth, parts: []codersdk.ChatMessagePart{
				codersdk.ChatMessageToolCall("call-1", readChatTranscriptToolName, json.RawMessage(`{"after_id":1}`)),
			}},
			row{role: database.ChatMessageRoleTool, visibility: database.ChatMessageVisibilityBoth, parts: []codersdk.ChatMessagePart{
				codersdk.ChatMessageToolResult("call-1", readChatTranscriptToolName, json.RawMessage(`{"messages":"secret-earlier-page"}`), false, false),
			}},
		)

		raw := read(t, chat, readChatTranscriptArgs{Limit: 2}, false)
		for _, secret := range []string{"secret-signature", "secret-credential", "secret-identity", "provider_metadata", "secret-user-projection"} {
			require.NotContains(t, raw, secret)
		}
		var first chatTranscriptResult
		require.NoError(t, json.Unmarshal([]byte(raw), &first))
		require.Equal(t, chat.ID, first.ChatID)
		require.Equal(t, []string{"user:text:first question", "assistant:reasoning:thinking", "assistant:text:answer"}, texts(first))
		require.True(t, first.HasMore)

		secondRaw := read(t, chat, readChatTranscriptArgs{AfterID: first.NextAfterID}, false)
		require.NotContains(t, secondRaw, "secret-hook-notice")
		require.NotContains(t, secondRaw, "secret-earlier-page")
		var second chatTranscriptResult
		require.NoError(t, json.Unmarshal([]byte(secondRaw), &second))
		require.Equal(t, []string{"user:text:second question"}, texts(second))
		require.False(t, second.HasMore)

		filtered := readPage(t, chat, readChatTranscriptArgs{Roles: []string{"assistant"}, PartTypes: []string{"text"}})
		require.Equal(t, []string{"assistant:text:answer"}, texts(filtered))
	})

	t.Run("BoundsThePageSize", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t, user.ID, uuid.NullUUID{}, uuid.NullUUID{})
		pasted := row{role: database.ChatMessageRoleUser, visibility: database.ChatMessageVisibilityBoth, parts: []codersdk.ChatMessagePart{
			codersdk.ChatMessageText(strings.Repeat("b", 10000)),
			codersdk.ChatMessageFileReference("main.go", 1, 500, strings.Repeat("c", 10000)),
		}}
		insert(t, chat.ID, userText(strings.Repeat("a", 3000)), pasted, userText("last"))

		first := readPage(t, chat, readChatTranscriptArgs{MaxBytes: minTranscriptBytes})
		require.Len(t, first.Messages, 1)
		require.True(t, first.HasMore)

		second := readPage(t, chat, readChatTranscriptArgs{AfterID: first.NextAfterID, MaxBytes: minTranscriptBytes})
		require.Len(t, second.Messages, 1)
		text := second.Messages[0].Parts[0].Text
		require.LessOrEqual(t, len(text), minTranscriptBytes)
		require.Contains(t, text, "Coder truncated")
		pastedContent := second.Messages[0].Parts[1].Content
		require.LessOrEqual(t, len(pastedContent), minTranscriptBytes)
		require.Contains(t, pastedContent, "Coder truncated")
		require.True(t, second.HasMore)

		last := readPage(t, chat, readChatTranscriptArgs{AfterID: second.NextAfterID, MaxBytes: minTranscriptBytes})
		require.Equal(t, []string{"user:text:last"}, texts(last))
		require.False(t, last.HasMore)
	})

	t.Run("ReadsAsTheChatOwner", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t, user.ID, uuid.NullUUID{}, uuid.NullUUID{})
		child := newChat(t, user.ID, uuid.NullUUID{UUID: chat.ID, Valid: true}, uuid.NullUUID{UUID: chat.ID, Valid: true})
		grandchild := newChat(t, user.ID, uuid.NullUUID{UUID: child.ID, Valid: true}, uuid.NullUUID{UUID: chat.ID, Valid: true})
		insert(t, grandchild.ID, userText("grandchild task"))
		othersChat := newChat(t, other.ID, uuid.NullUUID{}, uuid.NullUUID{})
		insert(t, othersChat.ID, userText("private"))

		require.Equal(t, []string{"user:text:grandchild task"},
			texts(readPage(t, chat, readChatTranscriptArgs{ChatID: grandchild.ID.String()})))

		refused := read(t, chat, readChatTranscriptArgs{ChatID: othersChat.ID.String()}, true)
		require.Contains(t, refused, "does not exist or your user cannot read it")
		require.NotContains(t, refused, "private")

		err := db.UpdateChatACLByID(systemRestrictedTestContext(t), database.UpdateChatACLByIDParams{
			ID:       othersChat.ID,
			UserACL:  database.ChatACL{user.ID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
			GroupACL: database.ChatACL{},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"user:text:private"},
			texts(readPage(t, chat, readChatTranscriptArgs{ChatID: othersChat.ID.String()})))
	})

	t.Run("RejectsInvalidArguments", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t, user.ID, uuid.NullUUID{}, uuid.NullUUID{})
		for _, args := range []readChatTranscriptArgs{
			{ChatID: "not-a-chat"},
			{Roles: []string{"robot"}},
			{PartTypes: []string{"poem"}},
			{Limit: maxTranscriptMessages + 1},
			{MaxBytes: minTranscriptBytes - 1},
			{AfterID: -1},
		} {
			read(t, chat, args, true)
		}
	})
}
