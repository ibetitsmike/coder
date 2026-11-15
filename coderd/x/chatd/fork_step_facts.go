package chatd

// This file is fork code: it lets the fork hooks of one generation step read
// each per-chat fact once. buildTurnEnvironment installs the facts on its
// context and seals them when it returns.
//
// The rule for a fact that changes during a step: the value read at the
// first lookup in the step wins for the rest of that step. The next step
// reads the new value. Tool calls, spawning and admission run after the
// step's environment is built, when the facts are sealed, so they read live
// and inside their own transactions. Every hook of a step runs with the
// step's context, so it reads as the same subject and dbauthz checks are
// unchanged.

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

type stepFactsKey struct{}

// forkStepFacts are the per-chat facts that the fork hooks of one generation
// step of chatID read.
type forkStepFacts struct {
	chatID uuid.UUID
	sealed atomic.Bool

	mu sync.Mutex
	// subagents holds the chat_subagents row of any chat that a hook of
	// the step looked up: the step's chat and its ancestors.
	subagents map[uuid.UUID]*stepFact[stepSubagentRow]
	// limits holds the subagent limits of each organization looked up.
	limits map[uuid.UUID]*stepFact[forkSubagentLimits]

	worktree stepFact[stepWorktree]
	packs    stepFact[[]enabledPack]
}

type stepSubagentRow struct {
	row   forkdb.ChatSubagent
	found bool
}

type stepWorktree struct {
	worktree subagentWorktree
	ok       bool
}

// stepFact is one fact of a step. It is loaded at most once successfully;
// a load that fails is not kept, so the next lookup tries again. Each fact
// has its own lock, so loading one fact can look up another.
type stepFact[T any] struct {
	mu    sync.Mutex
	ok    bool
	value T
}

// get returns the kept value, or calls load and keeps its value when load
// reports it can be kept.
func (f *stepFact[T]) get(load func() (T, bool)) T {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ok {
		return f.value
	}
	value, keep := load()
	if keep {
		f.value, f.ok = value, true
	}
	return value
}

func stepFactFor[T any](mu *sync.Mutex, facts map[uuid.UUID]*stepFact[T], key uuid.UUID) *stepFact[T] {
	mu.Lock()
	defer mu.Unlock()
	fact, ok := facts[key]
	if !ok {
		fact = &stepFact[T]{}
		facts[key] = fact
	}
	return fact
}

// withForkStepFacts installs empty facts for a generation step of chat on
// *ctx and returns the function that seals them.
func (*Server) withForkStepFacts(ctx *context.Context, chat database.Chat) func() {
	facts := &forkStepFacts{
		chatID:    chat.ID,
		subagents: map[uuid.UUID]*stepFact[stepSubagentRow]{},
		limits:    map[uuid.UUID]*stepFact[forkSubagentLimits]{},
	}
	*ctx = context.WithValue(*ctx, stepFactsKey{}, facts)
	return func() { facts.sealed.Store(true) }
}

// stepFacts returns the live facts of the step ctx builds, or nil outside a
// step or after it was built.
func stepFacts(ctx context.Context) *forkStepFacts {
	facts, _ := ctx.Value(stepFactsKey{}).(*forkStepFacts)
	if facts == nil || facts.sealed.Load() {
		return nil
	}
	return facts
}

// chatSubagentRow returns the chat_subagents row of chatID, with found false
// when the chat has none. fdb must read outside a transaction.
func chatSubagentRow(ctx context.Context, fdb forkdb.Store, chatID uuid.UUID) (forkdb.ChatSubagent, bool, error) {
	read := func() (forkdb.ChatSubagent, bool, error) {
		row, err := fdb.GetChatSubagentByChatID(ctx, chatID)
		if errors.Is(err, sql.ErrNoRows) {
			return forkdb.ChatSubagent{}, false, nil
		}
		if err != nil {
			return forkdb.ChatSubagent{}, false, err
		}
		return row, true, nil
	}
	facts := stepFacts(ctx)
	if facts == nil {
		return read()
	}
	var err error
	got := stepFactFor(&facts.mu, facts.subagents, chatID).get(func() (stepSubagentRow, bool) {
		var row stepSubagentRow
		row.row, row.found, err = read()
		return row, err == nil
	})
	return got.row, got.found, err
}

// stepSubagentDepth is subagentDepth for a hook of the generation step. The
// spawn path keeps subagentDepth, which reads in its transaction.
func stepSubagentDepth(ctx context.Context, fdb forkdb.Store, chat database.Chat) (int32, error) {
	if !chat.ParentChatID.Valid {
		return 0, nil
	}
	row, found, err := chatSubagentRow(ctx, fdb, chat.ID)
	if err != nil {
		return 0, xerrors.Errorf("get subagent depth of chat %s: %w", chat.ID, err)
	}
	if !found {
		// See subagentDepth.
		return 1, nil
	}
	return row.Depth, nil
}

// stepSubagentLimits is subagentLimits for a hook of the generation step.
// The spawn and admission paths keep subagentLimits, which reads in their
// transactions.
func (p *Server) stepSubagentLimits(ctx context.Context, fdb forkdb.Store, organizationID uuid.UUID) (forkSubagentLimits, error) {
	facts := stepFacts(ctx)
	if facts == nil {
		return p.subagentLimits(ctx, fdb, organizationID)
	}
	var err error
	limits := stepFactFor(&facts.mu, facts.limits, organizationID).get(func() (forkSubagentLimits, bool) {
		var limits forkSubagentLimits
		limits, err = p.subagentLimits(ctx, fdb, organizationID)
		return limits, err == nil
	})
	return limits, err
}

// stepChatWorktree returns the step's worktree when chat is the step's
// chat, or calls load.
func stepChatWorktree(ctx context.Context, chat database.Chat, load func() (subagentWorktree, bool, error)) (subagentWorktree, bool, error) {
	facts := stepFacts(ctx)
	if facts == nil || facts.chatID != chat.ID {
		return load()
	}
	var err error
	got := facts.worktree.get(func() (stepWorktree, bool) {
		var got stepWorktree
		got.worktree, got.ok, err = load()
		return got, err == nil
	})
	return got.worktree, got.ok, err
}

// stepEnabledPacks returns the step's enabled packs when chat is the step's
// chat, or calls load. load reports whether every lookup succeeded; packs
// from a failed lookup are not kept, so a later hook of the step tries
// again.
func stepEnabledPacks(ctx context.Context, chat database.Chat, load func() ([]enabledPack, bool)) []enabledPack {
	facts := stepFacts(ctx)
	if facts == nil || facts.chatID != chat.ID {
		packs, _ := load()
		return packs
	}
	return slices.Clone(facts.packs.get(load))
}
