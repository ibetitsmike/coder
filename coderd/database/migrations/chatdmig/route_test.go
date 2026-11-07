package chatdmig_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

func TestClassifyUpstreamMigrations(t *testing.T) {
	t.Parallel()
	dump, err := os.ReadFile("../../dump.sql")
	require.NoError(t, err)
	c := chatdmig.NewClassifier(string(dump))
	for file, want := range map[string]chatdmig.Ledger{
		// Creates skills and a core function.
		"000621_skills.up.sql": chatdmig.LedgerMixed,
		// Alters organizations, creates a chatd table.
		"000636_organization_skill_member_preferences.up.sql": chatdmig.LedgerChatd,
		// Alters a chatd table, deletes from site_configs.
		"000635_chat_model_override_computer_use.up.sql":       chatdmig.LedgerCore,
		"000637_delete_mcp_tool_search_experiment_rule.up.sql": chatdmig.LedgerCore,
		"000638_chat_queued_messages_editing_since.up.sql":     chatdmig.LedgerChatd,
		"000639_mcp_server_oauth2_client_registration.up.sql":  chatdmig.LedgerChatd,
	} {
		sql, err := os.ReadFile("../" + file)
		require.NoError(t, err)
		require.Equal(t, want, c.Classify(string(sql)), file)
	}
}

func TestClassifyIgnoresLiterals(t *testing.T) {
	t.Parallel()
	c := &chatdmig.Classifier{Relations: map[string]bool{"users": true, "chats": true}}
	// The CREATE inside the body and the comment do not count.
	require.Equal(t, chatdmig.LedgerChatd, c.Classify(`
-- CREATE TABLE workspaces_x ();
CREATE FUNCTION chat_touch() RETURNS void AS $$ BEGIN CREATE TABLE workspaces_y (); END $$ LANGUAGE plpgsql;
`))
	// "ON CONFLICT" and a quoted "FROM users" are not tables.
	require.Equal(t, chatdmig.LedgerChatd, c.Classify(`INSERT INTO chats (id) VALUES ('FROM users') ON CONFLICT DO NOTHING;`))
	require.Equal(t, chatdmig.LedgerCore, c.Classify(`UPDATE users SET x = (SELECT count(*) FROM chats);`))
}
