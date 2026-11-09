package chatd

// This file is fork code: it holds the search_chats chat tool of the
// fork-chat-todos experiment. appendForkTools adds it to a turn.

import (
	"context"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/searchquery"
)

const (
	searchChatsToolName = "search_chats"
	defaultSearchChats  = 20
	maxSearchChats      = 50
)

type searchChatsArgs struct {
	Query   string `json:"query,omitempty" description:"Space-separated key:value filters, combined with AND. Keys: search (full-text search over titles, PR titles, PR numbers and message text; quote phrases), title, status (waiting, running, error, requires_action, interrupting, paused), archived (true or false, default false), has_unread, pr_status (draft, open, merged, closed, none), pr, repo, pr_title, diff_url, source (created_by_me, shared_with_me, or both comma-separated; default created_by_me). Bare words are rejected. Empty lists your recent chats."`
	AfterID string `json:"after_id,omitempty" description:"Return chats after this chat ID. Leave it empty for the first page, then use next_after_id of the previous page."`
	Limit   int    `json:"limit,omitempty" description:"The most chats to return, 1 to 50. The default is 20."`
}

type searchChatsResult struct {
	Chats       []searchChatsEntry `json:"chats"`
	NextAfterID string             `json:"next_after_id,omitempty"`
	HasMore     bool               `json:"has_more"`
}

type searchChatsEntry struct {
	ChatID    uuid.UUID `json:"chat_id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Shared    bool      `json:"shared,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// searchChatsTool returns the search_chats tool, which finds the chats the
// owner of chat can read with the chat list search syntax.
func (p *Server) searchChatsTool(chat database.Chat) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		searchChatsToolName,
		"Find chats your user can read, newest activity first, with the "+
			"chat list search syntax. Returns top-level chats only; "+
			"list_agents and read_chat_transcript reach their subagents. "+
			"Continue with after_id set to next_after_id while has_more is true.",
		func(ctx context.Context, args searchChatsArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			result, err := p.searchChats(ctx, chat, args)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return toolJSONResponse(result), nil
		},
	)
}

// searchChats runs one page of the chat list query as the owner of chat,
// never as chatd, which can read every chat, so it finds the same chats
// the owner's chat list does.
func (p *Server) searchChats(ctx context.Context, chat database.Chat, args searchChatsArgs) (searchChatsResult, error) {
	limit := defaultSearchChats
	if args.Limit != 0 {
		if args.Limit < 1 || args.Limit > maxSearchChats {
			return searchChatsResult{}, xerrors.Errorf("limit must be between 1 and %d", maxSearchChats)
		}
		limit = args.Limit
	}
	var afterID uuid.UUID
	if args.AfterID != "" {
		var err error
		afterID, err = uuid.Parse(args.AfterID)
		if err != nil {
			return searchChatsResult{}, xerrors.Errorf("after_id %q is not a valid ID", args.AfterID)
		}
	}
	filter, errs := searchquery.Chats(args.Query)
	if len(errs) > 0 {
		details := make([]string, 0, len(errs))
		for _, e := range errs {
			details = append(details, e.Field+": "+e.Detail)
		}
		return searchChatsResult{}, xerrors.Errorf("invalid query: %s", strings.Join(details, "; "))
	}

	owner, _, err := httpmw.UserRBACSubject(ctx, p.db, chat.OwnerID, rbac.ScopeAll)
	if err != nil {
		return searchChatsResult{}, xerrors.Errorf("load chat owner authorization: %w", err)
	}
	ownerCtx := dbauthz.As(ctx, owner)
	filter.ViewerID = chat.OwnerID
	filter.SharedWithUserID = chat.OwnerID
	if filter.SharedOnly {
		groups, err := p.db.GetGroups(ownerCtx, database.GetGroupsParams{HasMemberID: chat.OwnerID})
		if err != nil {
			return searchChatsResult{}, xerrors.Errorf("read the groups of the chat owner: %w", err)
		}
		for _, group := range groups {
			filter.SharedWithGroupIds = append(filter.SharedWithGroupIds, group.Group.ID.String())
		}
	}
	filter.AfterID = afterID
	// One extra row tells whether another page exists.
	// #nosec G115 - limit is at most maxSearchChats.
	filter.LimitOpt = int32(limit + 1)
	rows, err := p.db.GetChats(ownerCtx, filter)
	if err != nil {
		return searchChatsResult{}, xerrors.Errorf("search chats: %w", err)
	}

	result := searchChatsResult{Chats: []searchChatsEntry{}, HasMore: len(rows) > limit}
	for _, row := range rows[:min(len(rows), limit)] {
		result.Chats = append(result.Chats, searchChatsEntry{
			ChatID:    row.Chat.ID,
			Title:     row.Chat.Title,
			Status:    string(row.Chat.Status),
			Shared:    row.Chat.OwnerID != chat.OwnerID,
			CreatedAt: row.Chat.CreatedAt,
			UpdatedAt: row.Chat.UpdatedAt,
		})
	}
	if result.HasMore {
		result.NextAfterID = result.Chats[len(result.Chats)-1].ChatID.String()
	}
	return result, nil
}
