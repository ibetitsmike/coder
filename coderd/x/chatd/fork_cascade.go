package chatd

// This file is fork code: it implements the interrupt cascade and the archive
// of an active family behind the fork-nested-subagents experiment. Upstream's
// InterruptChat and setChatFamilyArchived call the hooks in this file
// (forkInterruptDescendants and forkArchiveActiveFamily), which do nothing
// when the experiment is off.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
)

// familyArchiveSettleTimeout bounds how long archiving a root, or a child
// with archive_agent, waits for its active members to stop after
// interrupting them.
const familyArchiveSettleTimeout = 30 * time.Second

// interruptCascadeKey marks the context of the InterruptChat calls that a
// cascade makes, so that they do not walk their own subtrees again.
type interruptCascadeKey struct{}

func isActiveChatStatus(status database.ChatStatus) bool {
	switch status {
	case database.ChatStatusRunning, database.ChatStatusInterrupting, database.ChatStatusRequiresAction:
		return true
	default:
		return false
	}
}

// forkInterruptDescendants interrupts every active descendant of chat, leaves
// first, and reports whether any of them transitioned.
func (p *Server) forkInterruptDescendants(ctx context.Context, chat database.Chat) bool {
	if !p.nestedSubagentsEnabled() || ctx.Value(interruptCascadeKey{}) != nil {
		return false
	}
	logger := p.logger.With(slog.F("chat_id", chat.ID))
	fdb, err := forkdb.From(p.db)
	if err != nil {
		logger.Warn(ctx, "interrupt cascade: fork queries", slog.Error(err))
		return false
	}
	rows, err := fdb.GetChatSubtreeIDs(ctx, chat.ID)
	if err != nil {
		logger.Warn(ctx, "interrupt cascade: get subtree", slog.Error(err))
		return false
	}
	cascadeCtx := context.WithValue(ctx, interruptCascadeKey{}, true)
	interrupted := false
	for _, row := range rows {
		// The subtree query returns each status, so an idle descendant
		// costs no read. InterruptChat locks the chat by ID and checks
		// the transition against the locked row.
		if !isActiveChatStatus(row.Status) {
			continue
		}
		_, descendantInterrupted, err := p.InterruptChat(cascadeCtx, database.Chat{ID: row.ID})
		if err != nil {
			if !errors.Is(err, chatstate.ErrTransitionNotAllowed) {
				logger.Warn(ctx, "interrupt cascade: interrupt descendant", slog.F("descendant_id", row.ID), slog.Error(err))
			}
			continue
		}
		// Interrupt succeeds unchanged for a descendant its worker is
		// already interrupting, so only a real transition counts.
		interrupted = interrupted || descendantInterrupted
	}
	return interrupted
}

// forkArchiveActiveFamily interrupts the active family of root and waits up
// to familyArchiveSettleTimeout for it to stop. It returns nil on timeout, so
// that SetFamilyArchived refuses the archive as upstream does.
func (p *Server) forkArchiveActiveFamily(ctx context.Context, root database.Chat) error {
	if !p.nestedSubagentsEnabled() {
		return nil
	}
	ids, err := p.db.GetChatFamilyIDsByRootID(ctx, root.ID)
	if err != nil {
		return xerrors.Errorf("get chat family: %w", err)
	}
	return p.settleChatsForArchive(ctx, root, ids)
}

// settleChatsForArchive interrupts top, which cascades to its descendants,
// and waits up to familyArchiveSettleTimeout until top and its subtree have
// no active chat. ids holds top and its descendants when the archive
// starts. Each wake reads the current subtree and its chats with a fixed
// number of statements and interrupts again only the members that can
// still transition. It returns nil on timeout, so that the archive
// transition refuses the members that are still active.
func (p *Server) settleChatsForArchive(ctx context.Context, top database.Chat, ids []uuid.UUID) error {
	active, err := p.activeFamilyChats(ctx, ids)
	if err != nil || len(active) == 0 {
		return err
	}

	timer := p.clock.NewTimer(familyArchiveSettleTimeout, "chatd", "family_archive_settle")
	defer timer.Stop()
	notify := make(chan struct{}, 1)
	var cancels []func()
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for _, chat := range active {
		cancel, err := p.pubsub.SubscribeWithErr(coderdpubsub.ChatStateUpdateChannel(chat.ID), func(context.Context, []byte, error) {
			select {
			case notify <- struct{}{}:
			default:
			}
		})
		// On failure the fallback ticker still notices the change.
		if err == nil {
			cancels = append(cancels, cancel)
		}
	}
	ticker := p.clock.NewTicker(subagentAwaitFallbackPoll, "chatd", "family_archive_poll")
	defer ticker.Stop()

	if _, _, err := p.InterruptChat(ctx, top); err != nil && !errors.Is(err, chatstate.ErrTransitionNotAllowed) {
		return xerrors.Errorf("interrupt chat family: %w", err)
	}
	cascadeCtx := context.WithValue(ctx, interruptCascadeKey{}, true)
	for {
		stillActive, err := p.settleArchivePass(cascadeCtx, top.ID)
		if err != nil || !stillActive {
			return err
		}
		select {
		case <-notify:
		case <-ticker.C:
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// settleArchivePass reads the current subtree of top, so that a child
// spawned during the settle is covered, and the chats of top and that
// subtree, in two statements. It interrupts again every active member
// except an interrupting one that its worker still owns, and reports
// whether any member is still active. ctx must carry interruptCascadeKey,
// since the pass already covers the whole subtree.
func (p *Server) settleArchivePass(ctx context.Context, topID uuid.UUID) (bool, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return false, xerrors.Errorf("fork queries: %w", err)
	}
	rows, err := fdb.GetChatSubtreeIDs(ctx, topID)
	if err != nil {
		return false, xerrors.Errorf("get subtree: %w", err)
	}
	// The subtree comes leaves first, so every chat is interrupted
	// before its parent, as the cascade does.
	ids := make([]uuid.UUID, 0, len(rows)+1)
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	ids = append(ids, topID)
	active, err := p.activeFamilyChats(ctx, ids)
	if err != nil || len(active) == 0 {
		return false, err
	}
	// A requires_action member returns to running when interrupted, and a
	// running member may have started after the cascade, so those are
	// interrupted again. An interrupting member only waits for its
	// worker, unless no worker owns it, in which case the interrupt
	// finishes it inline.
	for _, chat := range active {
		if chat.Status == database.ChatStatusInterrupting && chat.WorkerID.Valid {
			continue
		}
		if _, _, err := p.InterruptChat(ctx, chat); err != nil && !errors.Is(err, chatstate.ErrTransitionNotAllowed) {
			return false, xerrors.Errorf("interrupt family chat %s: %w", chat.ID, err)
		}
	}
	return true, nil
}

// activeFamilyChats returns the active chats among ids, in the order of ids,
// with one statement.
func (p *Server) activeFamilyChats(ctx context.Context, ids []uuid.UUID) ([]database.Chat, error) {
	chats, err := p.db.GetChatsByIDs(ctx, ids)
	if err != nil {
		return nil, xerrors.Errorf("get family chats: %w", err)
	}
	byID := make(map[uuid.UUID]database.Chat, len(chats))
	for _, chat := range chats {
		byID[chat.ID] = chat
	}
	var active []database.Chat
	for _, id := range ids {
		if chat, ok := byID[id]; ok && isActiveChatStatus(chat.Status) {
			active = append(active, chat)
		}
	}
	return active, nil
}
