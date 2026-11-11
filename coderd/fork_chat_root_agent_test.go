package coderd_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkCreateChatAgentType(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db, _ := newChatClientWithAPIAndDatabase(t, withChatWorkerDisabled, func(o *coderdtest.Options) {
		o.DeploymentValues.Experiments = append(o.DeploymentValues.Experiments, string(codersdk.ExperimentForkAgentDefinitions))
	})
	user := coderdtest.CreateFirstUser(t, client.Client)
	defaultModel := createChatModel(t, client)
	definitionModel := createAdditionalChatModel(t, client, "openai", "gpt-4.1")
	memberRaw, _ := coderdtest.CreateAnotherUser(t, client.Client, user.OrganizationID)
	memberClient := codersdk.NewExperimentalClient(memberRaw)

	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	//nolint:gocritic // The test reads fork rows directly.
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	_, err = client.CreateForkUserAgentDefinition(ctx, user.OrganizationID, codersdk.Me, codersdk.CreateForkAgentDefinitionRequest{
		Content: "---\nname: reviewer\nmodel: gpt-4.1\nreasoning_effort: high\nreadonly: true\ntools: [Read]\n---\nREVIEWER-MARKER\n",
	})
	require.NoError(t, err)
	_, err = client.CreateForkOrganizationAgentDefinition(ctx, user.OrganizationID, codersdk.CreateForkAgentDefinitionRequest{
		Content: "---\nname: private\n---\nPrivate.\n",
	})
	require.NoError(t, err)
	require.NoError(t, client.UpdateForkOrganizationAgentDefinitionACL(ctx, user.OrganizationID, "private", codersdk.UpdateOrganizationSkillACLRequest{
		GroupRoles: map[string]codersdk.OrganizationSkillRole{user.OrganizationID.String(): codersdk.OrganizationSkillRoleDeleted},
	}))

	create := func(c *codersdk.ExperimentalClient, req codersdk.CreateChatRequest) (codersdk.Chat, error) {
		req.OrganizationID = user.OrganizationID
		req.Content = []codersdk.ChatInputPart{{Type: codersdk.ChatInputPartTypeText, Text: "hello"}}
		return c.CreateChat(ctx, req)
	}

	reviewer, err := create(client, codersdk.CreateChatRequest{AgentType: "reviewer"})
	require.NoError(t, err)
	require.Equal(t, "reviewer", reviewer.AgentType)
	require.Equal(t, definitionModel.ID, reviewer.LastModelConfigID)
	row, err := fdb.GetChatSubagentByChatID(sysCtx, reviewer.ID)
	require.NoError(t, err)
	require.Equal(t, forkdb.ChatSubagent{
		ChatID:     reviewer.ID,
		AgentType:  "reviewer",
		Depth:      0,
		Isolation:  "shared",
		ToolPolicy: row.ToolPolicy,
	}, row)
	var policy map[string]any
	require.NoError(t, json.Unmarshal(row.ToolPolicy.RawMessage, &policy))
	require.Equal(t, true, policy["read_only"])
	require.Equal(t, []any{"Read"}, policy["tools"])
	messages, err := db.GetChatMessagesForPromptByChatID(sysCtx, reviewer.ID)
	require.NoError(t, err)
	var systemPrompts string
	for _, message := range messages {
		if message.Role == database.ChatMessageRoleSystem {
			systemPrompts += string(message.Content.RawMessage)
		}
		if message.Role == database.ChatMessageRoleUser {
			require.Equal(t, database.NullChatReasoningEffort{ChatReasoningEffort: database.ChatReasoningEffortHigh, Valid: true}, message.ReasoningEffort)
		}
	}
	require.Contains(t, systemPrompts, "REVIEWER-MARKER")

	got, err := client.GetChat(ctx, reviewer.ID)
	require.NoError(t, err)
	require.Equal(t, "reviewer", got.AgentType)
	require.Empty(t, got.Children)

	// A chat created without a message keeps the definition's effort for
	// its first turn.
	idle, err := client.CreateChat(ctx, codersdk.CreateChatRequest{OrganizationID: user.OrganizationID, AgentType: "reviewer"})
	require.NoError(t, err)
	idleRow, err := db.GetChatByID(sysCtx, idle.ID)
	require.NoError(t, err)
	require.Equal(t, database.NullChatReasoningEffort{ChatReasoningEffort: database.ChatReasoningEffortHigh, Valid: true}, idleRow.LastReasoningEffort)

	// The request's model wins over the definition's.
	explicit, err := create(client, codersdk.CreateChatRequest{AgentType: "reviewer", ModelConfigID: &defaultModel.ID})
	require.NoError(t, err)
	require.Equal(t, defaultModel.ID, explicit.LastModelConfigID)

	explore, err := create(client, codersdk.CreateChatRequest{AgentType: "explore"})
	require.NoError(t, err)
	require.Equal(t, "explore", explore.AgentType)

	plain, err := create(client, codersdk.CreateChatRequest{})
	require.NoError(t, err)
	require.Empty(t, plain.AgentType)
	_, err = fdb.GetChatSubagentByChatID(sysCtx, plain.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// Roots with a row stay roots in the list: none is embedded as a child.
	chats, err := client.ListChats(ctx, nil)
	require.NoError(t, err)
	types := map[string]string{}
	for _, chat := range chats {
		require.Nil(t, chat.ParentChatID)
		require.Empty(t, chat.Children)
		types[chat.ID.String()] = chat.AgentType
	}
	require.Equal(t, map[string]string{
		reviewer.ID.String(): "reviewer",
		idle.ID.String():     "reviewer",
		explicit.ID.String(): "reviewer",
		explore.ID.String():  "explore",
		plain.ID.String():    "",
	}, types)

	for _, tc := range []struct {
		name      string
		client    *codersdk.ExperimentalClient
		agentType string
		detail    string
	}{
		{name: "Unknown", client: client, agentType: "nope", detail: "type must be one of: general, explore, private, reviewer ("},
		{name: "WorkspaceQualified", client: client, agentType: "workspace/reviewer", detail: "only be used through spawn_agent"},
		{name: "WorkspaceOnly", client: client, agentType: "workspace-only", detail: "only be used through spawn_agent"},
		{name: "ComputerUse", client: client, agentType: "computer_use", detail: `type "computer_use" cannot start a chat`},
		{name: "OrganizationNotShared", client: memberClient, agentType: "private", detail: "type must be one of: general, explore ("},
	} {
		_, err := create(tc.client, codersdk.CreateChatRequest{AgentType: tc.agentType})
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr, tc.name)
		require.Equal(t, http.StatusBadRequest, sdkErr.StatusCode(), tc.name)
		require.Contains(t, sdkErr.Detail, tc.detail, tc.name)
	}
}

func TestForkCreateChatAgentTypeExperimentOff(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, _, _ := newChatClientWithAPIAndDatabase(t, withChatWorkerDisabled)
	user := coderdtest.CreateFirstUser(t, client.Client)
	_ = createChatModel(t, client)
	_, err := client.CreateChat(ctx, codersdk.CreateChatRequest{
		OrganizationID: user.OrganizationID,
		Content:        []codersdk.ChatInputPart{{Type: codersdk.ChatInputPartTypeText, Text: "hello"}},
		AgentType:      "general",
	})
	var sdkErr *codersdk.Error
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, http.StatusBadRequest, sdkErr.StatusCode())
	require.Contains(t, sdkErr.Message, "fork-agent-definitions")
}
