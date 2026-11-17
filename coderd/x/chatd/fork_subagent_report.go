package chatd

// This file is fork code: it reads a subagent's report from its newest
// messages instead of its whole history. Upstream's
// latestSubagentAssistantMessage calls forkLatestSubagentReport first and
// keeps its own full read for a store without fork queries.

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
)

// forkReportPageSize is the page size after the first page, which reads one
// message. The first page holds the report unless the newest assistant
// messages have no text.
const forkReportPageSize = 50

// forkLatestSubagentReport returns the text of the newest assistant message
// of chatID that the user sees and that has text, as upstream's
// latestSubagentAssistantMessage does, reading newest first. ok is false
// when store has no fork queries, so that the caller reads as upstream.
func forkLatestSubagentReport(ctx context.Context, store database.Store, chatID uuid.UUID) (report string, ok bool, err error) {
	fdb, err := forkdb.From(store)
	if err != nil {
		return "", false, nil
	}
	params := forkdb.GetChatReportMessagesParams{ChatID: chatID, LimitCount: 1}
	for {
		rows, err := fdb.GetChatReportMessages(ctx, params)
		if err != nil {
			return "", true, xerrors.Errorf("get chat messages: %w", err)
		}
		for _, row := range rows {
			content, parseErr := chatprompt.ParseContent(database.ChatMessage{
				Role:           database.ChatMessageRoleAssistant,
				Content:        row.Content,
				ContentVersion: row.ContentVersion,
			})
			if parseErr != nil {
				continue
			}
			if text := strings.TrimSpace(contentBlocksToText(content)); text != "" {
				return text, true, nil
			}
		}
		if len(rows) < int(params.LimitCount) {
			return "", true, nil
		}
		params.BeforeID = rows[len(rows)-1].ID
		params.LimitCount = forkReportPageSize
	}
}
