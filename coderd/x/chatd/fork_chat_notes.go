package chatd

// This file is fork code: it holds the notes chat tool of the
// fork-token-budget experiment. appendForkTools adds it to a turn.

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

const (
	chatNotesToolName = "notes"
	// maxChatNoteBytes is the size cap of one note. The chat_notes table
	// checks the same cap.
	maxChatNoteBytes = 16384
	// maxChatNotes is the number of notes one chat keeps at most.
	maxChatNotes = 20
	// chatCheckpointNotePath is the note that holds the chat's checkpoint.
	chatCheckpointNotePath = "checkpoint.md"
)

// chatNotePathRE matches a note path. The chat_notes table checks the same
// pattern.
var chatNotePathRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)

const chatNotesToolDescription = "Keep notes for this chat. Notes stay when the chat history " +
	"is compacted or a new context window starts; earlier messages do not. Keep a short " +
	"checkpoint in " + chatCheckpointNotePath + ": the goal, the decisions, the progress, what " +
	"you learned and the next steps. Update it as you work. Other chats, subagents included, " +
	"cannot read these notes. Actions: list; read (path); write (path, content), which replaces " +
	"the note; append (path, content), which adds the content as a new line; delete (path). " +
	"A path uses a-z, 0-9, '.', '_', '/' and '-', starts with a letter or a digit and has at " +
	"most 128 characters. A note holds 16384 bytes at most, and a chat keeps 20 notes at most."

type chatNotesArgs struct {
	Action  string `json:"action" enum:"list,read,write,append,delete"`
	Path    string `json:"path,omitempty" description:"The note path, for example checkpoint.md. Every action except list needs it."`
	Content string `json:"content,omitempty" description:"For write, the new content of the note. For append, the line to add."`
}

type chatNoteSummary struct {
	Path      string    `json:"path"`
	Bytes     int64     `json:"bytes"`
	UpdatedAt time.Time `json:"updated_at"`
}

type chatNotesListResult struct {
	Notes []chatNoteSummary `json:"notes"`
}

type chatNoteResult struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

type chatNoteDeleteResult struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
}

// chatNotesTool returns the notes tool, which reads and writes the notes of
// chat.
func (p *Server) chatNotesTool(chat database.Chat) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		chatNotesToolName,
		chatNotesToolDescription,
		func(ctx context.Context, args chatNotesArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			result, err := p.runChatNotes(ctx, chat.ID, args)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return toolJSONResponse(result), nil
		},
	)
}

// runChatNotes runs one notes action on the notes of chatID. The returned
// error is the text the model sees.
func (p *Server) runChatNotes(ctx context.Context, chatID uuid.UUID, args chatNotesArgs) (any, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, xerrors.Errorf("fork queries: %w", err)
	}
	if args.Action == "list" {
		rows, err := fdb.ListChatNotes(ctx, chatID)
		if err != nil {
			return nil, xerrors.Errorf("list notes: %w", err)
		}
		notes := make([]chatNoteSummary, 0, len(rows))
		for _, row := range rows {
			notes = append(notes, chatNoteSummary(row))
		}
		return chatNotesListResult{Notes: notes}, nil
	}
	switch args.Action {
	case "read", "write", "append", "delete":
	default:
		return nil, xerrors.Errorf("unknown action %q: use list, read, write, append or delete", args.Action)
	}
	if !chatNotePathRE.MatchString(args.Path) {
		return nil, xerrors.Errorf("invalid note path %q: use 1 to 128 characters from a-z, 0-9, '.', '_', '/' and '-', starting with a letter or a digit", args.Path)
	}
	switch args.Action {
	case "read":
		note, err := fdb.GetChatNote(ctx, forkdb.GetChatNoteParams{ChatID: chatID, Path: args.Path})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("note %q does not exist", args.Path)
		}
		if err != nil {
			return nil, xerrors.Errorf("read note: %w", err)
		}
		return chatNoteResult{Path: note.Path, Content: note.Content, UpdatedAt: note.UpdatedAt}, nil
	case "delete":
		deleted, err := fdb.DeleteChatNote(ctx, forkdb.DeleteChatNoteParams{ChatID: chatID, Path: args.Path})
		if err != nil {
			return nil, xerrors.Errorf("delete note: %w", err)
		}
		return chatNoteDeleteResult{Path: args.Path, Deleted: deleted > 0}, nil
	}
	if len(args.Content) > maxChatNoteBytes {
		return nil, xerrors.Errorf("content is %d bytes: a note holds %d bytes at most", len(args.Content), maxChatNoteBytes)
	}
	note, err := fdb.WriteChatNote(ctx, forkdb.WriteChatNoteParams{
		ChatID:   chatID,
		Path:     args.Path,
		Content:  args.Content,
		MaxNotes: maxChatNotes,
		Append:   args.Action == "append",
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, xerrors.Errorf("this chat has %d notes, the most it keeps: write to an existing note or delete one first", maxChatNotes)
	case database.IsCheckViolation(err):
		return nil, xerrors.Errorf("note %q would be over %d bytes: write a shorter note instead", args.Path, maxChatNoteBytes)
	case err != nil:
		return nil, xerrors.Errorf("write note: %w", err)
	}
	return chatNoteSummary{Path: note.Path, Bytes: int64(len(note.Content)), UpdatedAt: note.UpdatedAt}, nil
}
