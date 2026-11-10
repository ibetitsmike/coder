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

// familyArchiveSettleTimeout bounds how long archiving a root waits for its
// active family to stop after interrupting it.
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
		descendant, err := p.db.GetChatByID(ctx, row.ID)
		if err != nil {
			logger.Warn(ctx, "interrupt cascade: get descendant", slog.F("descendant_id", row.ID), slog.Error(err))
			continue
		}
		if !isActiveChatStatus(descendant.Status) {
			continue
		}
		_, descendantInterrupted, err := p.InterruptChat(cascadeCtx, descendant)
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
	active, err := p.activeChatIDs(ctx, ids)
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
	for _, id := range active {
		cancel, err := p.pubsub.SubscribeWithErr(coderdpubsub.ChatStateUpdateChannel(id), func(context.Context, []byte, error) {
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

	for {
		// A requires_action member returns to running when interrupted,
		// so each wake interrupts the family again.
		if _, _, err := p.InterruptChat(ctx, root); err != nil && !errors.Is(err, chatstate.ErrTransitionNotAllowed) {
			return xerrors.Errorf("interrupt chat family: %w", err)
		}
		active, err = p.activeChatIDs(ctx, ids)
		if err != nil || len(active) == 0 {
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

func (p *Server) activeChatIDs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	var active []uuid.UUID
	for _, id := range ids {
		chat, err := p.db.GetChatByID(ctx, id)
		if err != nil {
			return nil, xerrors.Errorf("get family chat %s: %w", id, err)
		}
		if isActiveChatStatus(chat.Status) {
			active = append(active, id)
		}
	}
	return active, nil
}
