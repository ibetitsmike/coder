package agentapi_test

// This file is fork code: it tests that the process events of an agent fire
// the process watches of the fork-chat-wakeups experiment.

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"cdr.dev/slog/v3/sloggers/slogtest"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/agentapi"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestReportChatProcessEvents(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, _ := dbtestutil.NewDB(t)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	authzDB := dbauthz.New(db, rbac.NewAuthorizer(prometheus.NewRegistry()), logger, coderdtest.AccessControlStorePointer())
	mClock := quartz.NewMock(t)

	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID,
	})
	now := dbtime.Now()
	wakeup, err := fdb.InsertChatWakeup(ctx, forkdb.InsertChatWakeupParams{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		CreatedBy: chat.OwnerID,
		Source:    "wait_for_events",
		Kind:      forkdb.ChatWakeupKindEvents,
		Prompt:    "continue",
		Timezone:  "UTC",
		Status:    forkdb.ChatWakeupStatusActive,
		NextRunAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		ExpiresAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	require.NoError(t, err)
	agentID := uuid.New()
	watch, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID:        uuid.New(),
		WakeupID:  wakeup.ID,
		Kind:      forkdb.ChatWakeupWatchKindProcess,
		AgentID:   uuid.NullUUID{UUID: agentID, Valid: true},
		ProcessID: sql.NullString{String: "watched", Valid: true},
		Spec:      json.RawMessage(`{}`),
		CreatedAt: now,
	})
	require.NoError(t, err)
	getWatch := func() forkdb.ChatWakeupWatch {
		t.Helper()
		watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
		require.NoError(t, err)
		require.Len(t, watches, 1)
		return watches[0]
	}

	newAPI := func(events *agentapi.ForkChatProcessEvents) *agentapi.API {
		return agentapi.New(agentapi.Options{
			AgentID:               agentID,
			AuthenticatedCtx:      ctx,
			Log:                   logger,
			Clock:                 mClock,
			Database:              authzDB,
			ForkChatProcessEvents: events,
		}, database.Workspace{}, database.WorkspaceAgent{ID: agentID})
	}
	exitedAt := now.Add(-time.Minute)
	exit := func(processID string) *agentproto.ReportChatProcessEventsRequest {
		return &agentproto.ReportChatProcessEventsRequest{Events: []*agentproto.ReportChatProcessEventsRequest_Event{{
			ChatId:    chat.ID.String(),
			ProcessId: processID,
			Kind: &agentproto.ReportChatProcessEventsRequest_Event_Exit_{Exit: &agentproto.ReportChatProcessEventsRequest_Event_Exit{
				ExitCode:   3,
				OutputTail: "bye\n",
			}},
			At: timestamppb.New(exitedAt),
		}}}
	}

	// With the experiment off, the events are acked without a write.
	resp, err := newAPI(nil).ReportChatProcessEvents(ctx, exit("watched"))
	require.NoError(t, err)
	require.WithinDuration(t, mClock.Now(), resp.GetAckedAt().AsTime(), time.Microsecond)
	_, err = newAPI(nil).ReportChatProcessEvents(ctx, &agentproto.ReportChatProcessEventsRequest{LastAckedAt: timestamppb.New(mClock.Now())})
	require.NoError(t, err)
	require.Equal(t, watch, getWatch())

	poked := make(chan uuid.UUID, 10)
	api := newAPI(&agentapi.ForkChatProcessEvents{Poke: func(chatID uuid.UUID) { poked <- chatID }})
	reportedAt := func() time.Time {
		t.Helper()
		var spec struct {
			ReportedAt time.Time `json:"reported_at"`
		}
		require.NoError(t, json.Unmarshal(getWatch().Spec, &spec))
		return spec.ReportedAt
	}

	// A report without events and without last_acked_at, the first of a
	// connection, certifies nothing: the agent read its queue before any
	// time of coderd that it knows.
	resp, err = api.ReportChatProcessEvents(ctx, &agentproto.ReportChatProcessEventsRequest{})
	require.NoError(t, err)
	require.WithinDuration(t, mClock.Now(), resp.GetAckedAt().AsTime(), time.Microsecond)
	require.Equal(t, watch, getWatch())

	// With last_acked_at it records that time, not the time of receipt,
	// and pokes nobody.
	mClock.Advance(10 * time.Second).MustWait(ctx)
	lastAcked := resp.GetAckedAt()
	resp, err = api.ReportChatProcessEvents(ctx, &agentproto.ReportChatProcessEventsRequest{LastAckedAt: lastAcked})
	require.NoError(t, err)
	require.WithinDuration(t, mClock.Now(), resp.GetAckedAt().AsTime(), time.Microsecond)
	require.WithinDuration(t, lastAcked.AsTime(), reportedAt(), time.Microsecond)
	reported := getWatch()
	require.False(t, reported.FiredAt.Valid)
	require.Empty(t, poked)

	// A last_acked_at after the time of receipt is cut to it.
	_, err = api.ReportChatProcessEvents(ctx, &agentproto.ReportChatProcessEventsRequest{LastAckedAt: timestamppb.New(mClock.Now().Add(time.Hour))})
	require.NoError(t, err)
	require.WithinDuration(t, mClock.Now(), reportedAt(), time.Microsecond)
	watch = getWatch()

	// An exit that no watch waits for changes nothing and pokes nobody.
	_, err = api.ReportChatProcessEvents(ctx, exit("unwatched"))
	require.NoError(t, err)
	require.Equal(t, watch, getWatch())
	require.Empty(t, poked)

	// The exit of the watched process fires its watch and pokes its chat.
	_, err = api.ReportChatProcessEvents(ctx, exit("watched"))
	require.NoError(t, err)
	fired := getWatch()
	require.True(t, fired.FiredAt.Valid)
	require.WithinDuration(t, mClock.Now(), fired.FiredAt.Time, time.Millisecond)
	var payload forkdb.ChatProcessFired
	require.NoError(t, json.Unmarshal(fired.FiredPayload.RawMessage, &payload))
	require.NotNil(t, payload.Exit)
	require.Equal(t, 3, payload.Exit.Code)
	require.Equal(t, "bye\n", payload.Exit.Tail)
	require.WithinDuration(t, exitedAt, payload.Exit.At, time.Millisecond)
	require.Equal(t, chat.ID, testutil.TryReceive(ctx, t, poked))
	require.Empty(t, poked)

	// A resent batch, after a lost ack, keeps the first fire time.
	mClock.Advance(time.Minute).MustWait(ctx)
	_, err = api.ReportChatProcessEvents(ctx, exit("watched"))
	require.NoError(t, err)
	require.Equal(t, fired.FiredAt, getWatch().FiredAt)
}

// A resent output match, after a lost ack, fires nothing, so it does not
// count against the output_max_events of the watch, and a later match
// fires it again. An output event that no open watch takes lines for is
// named in the response, so the agent stops its output watch.
func TestReportChatProcessEventsOutputResent(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, _ := dbtestutil.NewDB(t)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	authzDB := dbauthz.New(db, rbac.NewAuthorizer(prometheus.NewRegistry()), logger, coderdtest.AccessControlStorePointer())
	mClock := quartz.NewMock(t)

	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID,
	})
	now := dbtime.Now()
	wakeup, err := fdb.InsertChatWakeup(ctx, forkdb.InsertChatWakeupParams{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		CreatedBy: chat.OwnerID,
		Source:    "wait_for_events",
		Kind:      forkdb.ChatWakeupKindEvents,
		Prompt:    "continue",
		Timezone:  "UTC",
		Status:    forkdb.ChatWakeupStatusActive,
		NextRunAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		ExpiresAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	require.NoError(t, err)
	agentID := uuid.New()
	_, err = fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID:        uuid.New(),
		WakeupID:  wakeup.ID,
		Kind:      forkdb.ChatWakeupWatchKindProcess,
		AgentID:   uuid.NullUUID{UUID: agentID, Valid: true},
		ProcessID: sql.NullString{String: "watched", Valid: true},
		Spec:      json.RawMessage(`{"output_pattern": "ERR", "output_max_events": 5}`),
		CreatedAt: now,
	})
	require.NoError(t, err)
	getWatch := func() forkdb.ChatWakeupWatch {
		t.Helper()
		watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
		require.NoError(t, err)
		require.Len(t, watches, 1)
		return watches[0]
	}
	poked := make(chan uuid.UUID, 10)
	api := agentapi.New(agentapi.Options{
		AgentID:               agentID,
		AuthenticatedCtx:      ctx,
		Log:                   logger,
		Clock:                 mClock,
		Database:              authzDB,
		ForkChatProcessEvents: &agentapi.ForkChatProcessEvents{Poke: func(chatID uuid.UUID) { poked <- chatID }},
	}, database.Workspace{}, database.WorkspaceAgent{ID: agentID})
	event := func(wakeupID uuid.UUID, at time.Time, lines ...string) *agentproto.ReportChatProcessEventsRequest_Event {
		return &agentproto.ReportChatProcessEventsRequest_Event{
			ChatId:    chat.ID.String(),
			ProcessId: "watched",
			Kind: &agentproto.ReportChatProcessEventsRequest_Event_OutputMatch_{OutputMatch: &agentproto.ReportChatProcessEventsRequest_Event_OutputMatch{
				Lines:    lines,
				Matched:  int32(len(lines)), //nolint:gosec // A test sends a few lines.
				WakeupId: wakeupID.String(),
			}},
			At: timestamppb.New(at),
		}
	}
	match := func(at time.Time, lines ...string) *agentproto.ReportChatProcessEventsRequest {
		return &agentproto.ReportChatProcessEventsRequest{Events: []*agentproto.ReportChatProcessEventsRequest_Event{event(wakeup.ID, at, lines...)}}
	}
	var payload forkdb.ChatProcessFired

	resp, err := api.ReportChatProcessEvents(ctx, match(now, "ERR a"))
	require.NoError(t, err)
	require.Empty(t, resp.GetClosedOutputEvents())
	first := getWatch()
	require.NoError(t, json.Unmarshal(first.FiredPayload.RawMessage, &payload))
	require.Equal(t, 1, payload.Matched)
	require.Equal(t, chat.ID, testutil.TryReceive(ctx, t, poked))

	// The agent sends the batch again: nothing changes and nobody is poked.
	// The watch still takes lines, so the event is not closed.
	resp, err = api.ReportChatProcessEvents(ctx, match(now, "ERR a"))
	require.NoError(t, err)
	require.Empty(t, resp.GetClosedOutputEvents())
	require.Equal(t, first, getWatch())
	require.Empty(t, poked)

	// A later match adds up. A match for a wake-up without a watch is
	// closed.
	resp, err = api.ReportChatProcessEvents(ctx, &agentproto.ReportChatProcessEventsRequest{Events: []*agentproto.ReportChatProcessEventsRequest_Event{
		event(wakeup.ID, now.Add(time.Second), "ERR b"),
		event(uuid.New(), now.Add(time.Second), "ERR b"),
	}})
	require.NoError(t, err)
	require.Equal(t, []int32{1}, resp.GetClosedOutputEvents())
	require.NoError(t, json.Unmarshal(getWatch().FiredPayload.RawMessage, &payload))
	require.Equal(t, 2, payload.Matched)
	require.Equal(t, chat.ID, testutil.TryReceive(ctx, t, poked))

	// After the exit of the process, its watch takes no more lines.
	_, err = api.ReportChatProcessEvents(ctx, &agentproto.ReportChatProcessEventsRequest{Events: []*agentproto.ReportChatProcessEventsRequest_Event{{
		ChatId:    chat.ID.String(),
		ProcessId: "watched",
		Kind:      &agentproto.ReportChatProcessEventsRequest_Event_Exit_{Exit: &agentproto.ReportChatProcessEventsRequest_Event_Exit{}},
		At:        timestamppb.New(now.Add(2 * time.Second)),
	}}})
	require.NoError(t, err)
	require.Equal(t, chat.ID, testutil.TryReceive(ctx, t, poked))
	resp, err = api.ReportChatProcessEvents(ctx, match(now.Add(3*time.Second), "ERR c"))
	require.NoError(t, err)
	require.Equal(t, []int32{0}, resp.GetClosedOutputEvents())
	require.Empty(t, poked)
}
