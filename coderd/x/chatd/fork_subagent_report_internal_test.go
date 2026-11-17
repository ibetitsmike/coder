package chatd

// This file is fork code: it tests that a subagent's report is read from its
// newest messages and matches upstream's full read.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// reportReads counts the reads of a subagent's report.
type reportReads struct {
	pages     int
	rows      int
	fullReads int
}

// reportCountStore counts the report reads of the store it wraps.
type reportCountStore struct {
	database.Store
	reads *reportReads
}

func (s reportCountStore) GetChatMessagesByChatID(ctx context.Context, arg database.GetChatMessagesByChatIDParams) ([]database.ChatMessage, error) {
	s.reads.fullReads++
	return s.Store.GetChatMessagesByChatID(ctx, arg)
}

func (s reportCountStore) ForkUnwrap() database.Store { return s.Store }

func (s reportCountStore) ForkWrap(inner forkdb.Store) forkdb.Store {
	return reportCountForkStore{Store: inner, reads: s.reads}
}

type reportCountForkStore struct {
	forkdb.Store
	reads *reportReads
}

func (s reportCountForkStore) GetChatReportMessages(ctx context.Context, arg forkdb.GetChatReportMessagesParams) ([]forkdb.GetChatReportMessagesRow, error) {
	rows, err := s.Store.GetChatReportMessages(ctx, arg)
	s.reads.pages++
	s.reads.rows += len(rows)
	return rows, err
}

func TestForkLatestSubagentReport(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	user, org, model := seedInternalChatDeps(t, db)
	newChat := func() database.Chat {
		return dbgen.Chat(t, db, database.Chat{
			OrganizationID:    org.ID,
			OwnerID:           user.ID,
			LastModelConfigID: model.ID,
		})
	}
	insert := func(chatID uuid.UUID, role database.ChatMessageRole, visibility database.ChatMessageVisibility, parts ...codersdk.ChatMessagePart) database.ChatMessage {
		content, err := chatprompt.MarshalParts(parts)
		require.NoError(t, err)
		return dbgen.ChatMessage(t, db, database.ChatMessage{
			ChatID:        chatID,
			ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
			Role:          role,
			Visibility:    visibility,
			Content:       content,
		})
	}
	text := codersdk.ChatMessageText
	toolCall := func() codersdk.ChatMessagePart {
		return codersdk.ChatMessageToolCall(uuid.NewString(), "execute", json.RawMessage(`{"command":"make"}`))
	}
	assistant, userRole, tool := database.ChatMessageRoleAssistant, database.ChatMessageRoleUser, database.ChatMessageRoleTool
	both, modelOnly := database.ChatMessageVisibilityBoth, database.ChatMessageVisibilityModel

	// read returns the report through the fork path with its reads, and
	// checks that upstream's full read returns the same report.
	read := func(t *testing.T, chatID uuid.UUID) (string, reportReads) {
		t.Helper()
		ctx := testutil.Context(t, testutil.WaitMedium)
		var reads reportReads
		report, err := latestSubagentAssistantMessage(ctx, reportCountStore{Store: db, reads: &reads}, chatID)
		require.NoError(t, err)
		upstream, err := latestSubagentAssistantMessage(ctx, struct{ database.Store }{db}, chatID)
		require.NoError(t, err)
		require.Equal(t, upstream, report, "fork report differs from upstream's")
		return report, reads
	}

	t.Run("NewestMessage", func(t *testing.T) {
		t.Parallel()
		chat := newChat()
		for range 30 {
			insert(chat.ID, userRole, both, text("do it"))
			insert(chat.ID, assistant, both, text("working"), toolCall())
			insert(chat.ID, tool, both, codersdk.ChatMessageToolResult(uuid.NewString(), "execute", json.RawMessage(`{"output":"ok"}`), false, false))
		}
		insert(chat.ID, assistant, both, text("  task complete  "))

		report, reads := read(t, chat.ID)
		require.Equal(t, "task complete", report)
		require.Equal(t, reportReads{pages: 1, rows: 1}, reads)
	})

	t.Run("PastMessagesWithoutReport", func(t *testing.T) {
		t.Parallel()
		chat := newChat()
		insert(chat.ID, assistant, both, text("older report"))
		insert(chat.ID, assistant, both, text("the report"))
		for range 60 {
			insert(chat.ID, assistant, both, toolCall())
			insert(chat.ID, tool, both, codersdk.ChatMessageToolResult(uuid.NewString(), "execute", json.RawMessage(`{"output":"ok"}`), false, false))
		}
		insert(chat.ID, assistant, modelOnly, text("model only"))
		deleted := insert(chat.ID, assistant, both, text("deleted"))
		ctx := testutil.Context(t, testutil.WaitMedium)
		_, err := sqlDB.ExecContext(ctx, "UPDATE chat_messages SET deleted = true WHERE id = $1", deleted.ID)
		require.NoError(t, err)
		insert(chat.ID, userRole, both, text("user text"))
		insert(chat.ID, assistant, both, text("   "))

		report, reads := read(t, chat.ID)
		require.Equal(t, "the report", report)
		// Pages of 1, 50 and 12 rows, the last holding both reports.
		require.Equal(t, reportReads{pages: 3, rows: 63}, reads)
	})

	t.Run("NoReport", func(t *testing.T) {
		t.Parallel()
		chat := newChat()
		insert(chat.ID, userRole, both, text("do it"))
		insert(chat.ID, assistant, both, toolCall())
		insert(chat.ID, assistant, modelOnly, text("model only"))

		report, reads := read(t, chat.ID)
		require.Empty(t, report)
		// The first page is full, so a second page shows the end.
		require.Equal(t, reportReads{pages: 2, rows: 1}, reads)
	})

	t.Run("NoMessages", func(t *testing.T) {
		t.Parallel()
		report, reads := read(t, newChat().ID)
		require.Empty(t, report)
		require.Equal(t, reportReads{pages: 1}, reads)
	})
}
