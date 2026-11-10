package chatd

// This file is fork code: it holds the schedule_wakeup chat tool of the
// fork-chat-wakeups experiment and the wake-up validation that the tool and
// the /loop command share. appendForkTools adds the tool to a turn.

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

const (
	scheduleWakeupToolName = "schedule_wakeup"
	wakeupSourceTool       = "schedule_wakeup"
	wakeupSourceLoop       = "loop"

	// minWakeupPeriod matches the chat_wakeups interval CHECK constraint.
	minWakeupPeriod     = time.Minute
	maxWakeupPeriod     = 30 * 24 * time.Hour
	maxChatWakeups      = 10
	maxWakeupPromptSize = 4000
	loopCommand         = "/loop"
)

// wakeupSpec describes a wake-up to create. Exactly the field of its kind is
// set: delaySeconds, intervalSeconds or cron.
type wakeupSpec struct {
	source           string
	kind             forkdb.ChatWakeupKind
	prompt           string
	delaySeconds     int
	intervalSeconds  int
	cron             string
	timezone         string
	maxRuns          int
	expiresInSeconds int
}

type scheduleWakeupArgs struct {
	Prompt           string  `json:"prompt" description:"The user message this chat receives at each run. It must not start with /loop."`
	DelaySeconds     *int    `json:"delay_seconds,omitempty" description:"Run once, this many seconds from now."`
	IntervalSeconds  *int    `json:"interval_seconds,omitempty" description:"Run every this many seconds, at least 60, starting one interval from now."`
	Cron             *string `json:"cron,omitempty" description:"Run on this 5-field cron schedule (minute hour day-of-month month day-of-week), e.g. \"0 9 * * 1-5\"."`
	Timezone         *string `json:"timezone,omitempty" description:"IANA time zone of cron, e.g. \"Europe/Warsaw\". Defaults to UTC. Only valid with cron."`
	MaxRuns          *int    `json:"max_runs,omitempty" description:"Stop after this many runs."`
	ExpiresInSeconds *int    `json:"expires_in_seconds,omitempty" description:"Stop this many seconds from now."`
}

type scheduleWakeupResult struct {
	ID        uuid.UUID             `json:"id"`
	Kind      forkdb.ChatWakeupKind `json:"kind"`
	NextRunAt time.Time             `json:"next_run_at"`
	ExpiresAt *time.Time            `json:"expires_at,omitempty"`
	MaxRuns   *int32                `json:"max_runs,omitempty"`
}

// scheduleWakeupTool returns the schedule_wakeup tool, which creates a
// wake-up of chat.
func (p *Server) scheduleWakeupTool(chat database.Chat) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		scheduleWakeupToolName,
		"Schedule this chat to receive prompt as a new user message later: "+
			"once after delay_seconds, every interval_seconds, or on a 5-field "+
			"cron schedule in timezone (default UTC). Set exactly one of them. "+
			"Optionally stop after max_runs runs or after expires_in_seconds. "+
			"A wake-up never interrupts a running turn: it waits until the "+
			"chat is idle. Returns the wake-up ID and its first run time.",
		func(ctx context.Context, args scheduleWakeupArgs, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			spec, err := scheduleWakeupSpec(args)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			// A tool call replayed after a worker restart must not create a
			// second wake-up, so the ID derives from the call.
			id := uuid.NewSHA1(chat.ID, []byte(call.ID))
			wakeup, err := p.createChatWakeup(ctx, p.db, chat, id, spec, p.clock.Now())
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			result := scheduleWakeupResult{
				ID:        wakeup.ID,
				Kind:      wakeup.Kind,
				NextRunAt: wakeup.NextRunAt.Time,
			}
			if loc, err := time.LoadLocation(wakeup.Timezone); err == nil {
				result.NextRunAt = result.NextRunAt.In(loc)
			}
			if wakeup.ExpiresAt.Valid {
				result.ExpiresAt = &wakeup.ExpiresAt.Time
			}
			if wakeup.MaxRuns.Valid {
				result.MaxRuns = &wakeup.MaxRuns.Int32
			}
			return toolJSONResponse(result), nil
		},
	)
}

// scheduleWakeupSpec maps schedule_wakeup arguments to a wake-up spec.
func scheduleWakeupSpec(args scheduleWakeupArgs) (wakeupSpec, error) {
	spec := wakeupSpec{source: wakeupSourceTool, prompt: args.Prompt}
	schedules := 0
	if args.DelaySeconds != nil {
		schedules++
		spec.kind, spec.delaySeconds = forkdb.ChatWakeupKindDelay, *args.DelaySeconds
	}
	if args.IntervalSeconds != nil {
		schedules++
		spec.kind, spec.intervalSeconds = forkdb.ChatWakeupKindInterval, *args.IntervalSeconds
	}
	if args.Cron != nil {
		schedules++
		spec.kind, spec.cron = forkdb.ChatWakeupKindCron, *args.Cron
	}
	if schedules != 1 {
		return wakeupSpec{}, xerrors.New("set exactly one of delay_seconds, interval_seconds and cron")
	}
	if args.Timezone != nil {
		if spec.kind != forkdb.ChatWakeupKindCron {
			return wakeupSpec{}, xerrors.New("timezone is only valid with cron")
		}
		spec.timezone = *args.Timezone
	}
	if args.MaxRuns != nil {
		if *args.MaxRuns <= 0 || *args.MaxRuns > math.MaxInt32 {
			return wakeupSpec{}, xerrors.Errorf("max_runs must be a positive number, got %d", *args.MaxRuns)
		}
		spec.maxRuns = *args.MaxRuns
	}
	if args.ExpiresInSeconds != nil {
		if *args.ExpiresInSeconds <= 0 || *args.ExpiresInSeconds > math.MaxInt32 {
			return wakeupSpec{}, xerrors.Errorf("expires_in_seconds must be a positive number, got %d", *args.ExpiresInSeconds)
		}
		spec.expiresInSeconds = *args.ExpiresInSeconds
	}
	return spec, nil
}

// validate checks the shared wake-up rules and returns spec with a trimmed
// prompt and a resolved timezone.
func (spec wakeupSpec) validate() (wakeupSpec, error) {
	spec.prompt = strings.TrimSpace(spec.prompt)
	switch {
	case spec.prompt == "":
		return wakeupSpec{}, xerrors.New("prompt is required")
	case len(spec.prompt) > maxWakeupPromptSize:
		return wakeupSpec{}, xerrors.Errorf("prompt is longer than %d bytes", maxWakeupPromptSize)
	case isLoopCommand(spec.prompt):
		return wakeupSpec{}, xerrors.Errorf("prompt must not start with %s", loopCommand)
	}
	maxSeconds := int(maxWakeupPeriod / time.Second)
	switch spec.kind {
	case forkdb.ChatWakeupKindDelay:
		if spec.delaySeconds <= 0 || spec.delaySeconds > maxSeconds {
			return wakeupSpec{}, xerrors.Errorf("delay must be between 1 second and %s, got %d seconds", maxWakeupPeriod, spec.delaySeconds)
		}
	case forkdb.ChatWakeupKindInterval:
		if spec.intervalSeconds < int(minWakeupPeriod/time.Second) || spec.intervalSeconds > maxSeconds {
			return wakeupSpec{}, xerrors.Errorf("interval must be between %s and %s, got %d seconds", minWakeupPeriod, maxWakeupPeriod, spec.intervalSeconds)
		}
	case forkdb.ChatWakeupKindCron:
		// Five fields keep out descriptors such as @every, which could run
		// more often than once a minute, and a CRON_TZ prefix.
		spec.cron = strings.TrimSpace(spec.cron)
		if len(strings.Fields(spec.cron)) != 5 {
			return wakeupSpec{}, xerrors.Errorf("cron must have 5 fields (minute hour day-of-month month day-of-week), got %q", spec.cron)
		}
	default:
		return wakeupSpec{}, xerrors.Errorf("unsupported wake-up kind %q", spec.kind)
	}
	if spec.timezone == "" {
		spec.timezone = "UTC"
	}
	if _, err := time.LoadLocation(spec.timezone); err != nil || spec.timezone == "Local" {
		return wakeupSpec{}, xerrors.Errorf("unknown time zone %q", spec.timezone)
	}
	return spec, nil
}

// createChatWakeup validates spec and creates an active wake-up of chat with
// id on behalf of its owner. It returns the existing wake-up when chat already
// has one with id. store may be a transaction.
func (*Server) createChatWakeup(ctx context.Context, store database.Store, chat database.Chat, id uuid.UUID, spec wakeupSpec, now time.Time) (forkdb.ChatWakeup, error) {
	spec, err := spec.validate()
	if err != nil {
		return forkdb.ChatWakeup{}, err
	}
	now = dbtime.Time(now).UTC()
	draft := forkdb.ChatWakeup{Kind: spec.kind, Timezone: spec.timezone}
	if spec.kind == forkdb.ChatWakeupKindInterval {
		draft.IntervalSeconds.Int32, draft.IntervalSeconds.Valid = int32(spec.intervalSeconds), true //nolint:gosec // validate bounds it.
	}
	if spec.kind == forkdb.ChatWakeupKindCron {
		draft.Cron.String, draft.Cron.Valid = spec.cron, true
	}
	if spec.maxRuns > 0 {
		draft.MaxRuns.Int32, draft.MaxRuns.Valid = int32(spec.maxRuns), true //nolint:gosec // validate bounds it.
	}
	if spec.expiresInSeconds > 0 {
		draft.ExpiresAt.Time, draft.ExpiresAt.Valid = now.Add(time.Duration(spec.expiresInSeconds)*time.Second), true
	}
	next, err := firstWakeupRun(draft, now, time.Duration(spec.delaySeconds)*time.Second)
	if err != nil {
		return forkdb.ChatWakeup{}, err
	}

	fdb, err := forkdb.From(store)
	if err != nil {
		return forkdb.ChatWakeup{}, xerrors.Errorf("fork queries: %w", err)
	}
	existing, err := fdb.ListChatWakeupsByChatID(ctx, chat.ID)
	if err != nil {
		return forkdb.ChatWakeup{}, xerrors.Errorf("list chat wake-ups: %w", err)
	}
	pending := 0
	for _, wakeup := range existing {
		if wakeup.ID == id {
			return wakeup, nil
		}
		if wakeup.Status == forkdb.ChatWakeupStatusActive || wakeup.Status == forkdb.ChatWakeupStatusPaused {
			pending++
		}
	}
	if pending >= maxChatWakeups {
		return forkdb.ChatWakeup{}, xerrors.Errorf("a chat can have at most %d active or paused wake-ups", maxChatWakeups)
	}

	wakeup, err := fdb.InsertChatWakeup(ctx, forkdb.InsertChatWakeupParams{
		ID:              id,
		ChatID:          chat.ID,
		CreatedBy:       chat.OwnerID,
		Source:          spec.source,
		Kind:            spec.kind,
		Prompt:          spec.prompt,
		IntervalSeconds: draft.IntervalSeconds,
		Cron:            draft.Cron,
		Timezone:        spec.timezone,
		Events:          json.RawMessage(`[]`),
		Status:          forkdb.ChatWakeupStatusActive,
		NextRunAt:       sql.NullTime{Time: next, Valid: true},
		ExpiresAt:       draft.ExpiresAt,
		MaxRuns:         draft.MaxRuns,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		return forkdb.ChatWakeup{}, xerrors.Errorf("create chat wake-up: %w", err)
	}
	return wakeup, nil
}

// firstWakeupRun returns the first run time of a new wake-up. A repeating
// wake-up schedules its first run the way the worker schedules every later
// run, as if a run had just happened now.
func firstWakeupRun(draft forkdb.ChatWakeup, now time.Time, delay time.Duration) (time.Time, error) {
	errExpired := xerrors.New("the wake-up expires before its first run")
	if draft.Kind == forkdb.ChatWakeupKindDelay {
		next := now.Add(delay)
		if draft.ExpiresAt.Valid && !next.Before(draft.ExpiresAt.Time) {
			return time.Time{}, errExpired
		}
		return next, nil
	}
	draft.NextRunAt = sql.NullTime{Time: now, Valid: true}
	finish := nextWakeupRun(draft, now, 0)
	switch {
	case finish.nextRunAt.Valid:
		return finish.nextRunAt.Time, nil
	case draft.Kind == forkdb.ChatWakeupKindCron && finish.reason == "invalid_cron":
		return time.Time{}, xerrors.Errorf("cron %q is not a valid schedule", draft.Cron.String)
	default:
		return time.Time{}, errExpired
	}
}
