package chatd

// This file is fork code: it holds the pull request poller of the
// fork-chat-wakeups experiment. Each replica runs one poller, apart from the
// waker, so a slow provider never holds up a delivery. A pass claims the open
// pull_request watches that are due, reads each pull request once per chat
// owner, and fires the watches that have new activity with their message,
// which makes them ready for the waker.
//
// The provider sends a conditional request with the ETag of its last
// response for each endpoint, so a poll of a pull request that did not
// change costs no rate limit. Those ETags live in the memory of the replica,
// so the first poll after a restart reads in full. The login of the owner's
// token is kept in the spec of the watch, so a poll does not read it again.

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
	"github.com/coder/quartz"
)

const (
	// pullRequestPollerInterval is how often the poller looks for due
	// watches.
	pullRequestPollerInterval = 15 * time.Second
	// pullRequestPollerBatch is how many watches one claim takes. With
	// maxPullRequestReads reads of at most pullRequestReadTimeout, a batch
	// finishes within pullRequestPollerLease.
	pullRequestPollerBatch = 25
	// pullRequestPollerLease is how long a claimed watch is left to its
	// claimer before the poller of another replica may read it.
	pullRequestPollerLease = 5 * time.Minute
	// maxPullRequestReads is how many reads the poller runs at once.
	maxPullRequestReads = 4
)

// pullRequestFired is the fired_payload of a pull_request watch.
type pullRequestFired struct {
	Text string `json:"text"`
}

// pullRequestPollPatch is what a read merges into the spec of a watch.
type pullRequestPollPatch struct {
	ViewerLogin string    `json:"viewer_login,omitempty"`
	NextPollAt  time.Time `json:"next_poll_at,omitzero"`
	PolledAt    time.Time `json:"polled_at,omitzero"`
}

// runPullRequestPoller polls the due pull_request watches until ctx is done.
func (p *Server) runPullRequestPoller(ctx context.Context, store database.Store, clock quartz.Clock) {
	if p.forkPullRequestActivity == nil {
		return
	}
	//nolint:gocritic // The poller reads the pull requests of every chat.
	ctx = dbauthz.AsChatd(ctx)
	fdb, err := forkdb.From(store)
	if err != nil {
		p.logger.Warn(ctx, "pull request poller fork queries unavailable", slogError(err))
		return
	}
	ticker := clock.NewTicker(pullRequestPollerInterval, "chatd", "pr-poller")
	defer ticker.Stop("chatd", "pr-poller")
	for {
		p.pollPullRequests(ctx, fdb, dbtime.Time(clock.Now("chatd", "pr-poller")).UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pollPullRequests runs one pass of the poller at now: it claims the due
// watches in batches and reads each batch with at most maxPullRequestReads
// reads in flight. Every claim takes the watches due at now, so a pass never
// claims a watch it read again however long it runs, and leases them from
// now plus the time the pass took so far, so the lease covers its own
// reads. A full batch may end inside the group of one pull request and
// owner, so that group waits for the next claim and is read once with the
// rest of it.
func (p *Server) pollPullRequests(ctx context.Context, fdb forkdb.Store, now time.Time) {
	began := p.clock.Now("chatd", "pr-poller")
	var carry []forkdb.ClaimDuePullRequestWatchesRow
	for ctx.Err() == nil {
		claimNow := now.Add(p.clock.Since(began, "chatd", "pr-poller"))
		claimed, err := fdb.ClaimDuePullRequestWatches(ctx, forkdb.ClaimDuePullRequestWatchesParams{
			Now:        now,
			LimitCount: pullRequestPollerBatch,
			LeaseUntil: claimNow.Add(pullRequestPollerLease),
		})
		if err != nil {
			if ctx.Err() == nil {
				p.logger.Warn(ctx, "claim due pull request watches", slogError(err))
			}
			claimed = nil
		}
		last := len(claimed) < pullRequestPollerBatch
		rows := slices.Concat(carry, claimed)
		carry = nil
		var groups [][]forkdb.ClaimDuePullRequestWatchesRow
		// The rows are sorted by pull request and owner, so a group is a
		// run of rows.
		for start := 0; start < len(rows); {
			end := start + 1
			for end < len(rows) && rows[end].PullRequestUrl == rows[start].PullRequestUrl && rows[end].OwnerID == rows[start].OwnerID {
				end++
			}
			groups = append(groups, rows[start:end])
			start = end
		}
		if !last && len(groups) > 0 {
			carry, groups = groups[len(groups)-1], groups[:len(groups)-1]
		}
		var eg errgroup.Group
		eg.SetLimit(maxPullRequestReads)
		for _, group := range groups {
			eg.Go(func() error {
				p.pollPullRequest(ctx, fdb, group, claimNow)
				return nil
			})
		}
		_ = eg.Wait()
		if last || err != nil {
			return
		}
	}
}

// pollPullRequest reads one pull request as one owner for the watches in
// rows, and records the read on each of them. A failed read of the checks
// fails the whole read, so the watches that do not watch checks are read
// apart from those that do.
func (p *Server) pollPullRequest(ctx context.Context, fdb forkdb.Store, rows []forkdb.ClaimDuePullRequestWatchesRow, now time.Time) {
	var parts [2]struct {
		rows   []forkdb.ClaimDuePullRequestWatchesRow
		events []wakeupEvent
	}
	for _, row := range rows {
		var event wakeupEvent
		if err := json.Unmarshal(row.Spec, &event); err != nil {
			// The waker pauses the wake-up of an invalid watch.
			p.logger.Warn(ctx, "pull request watch has an invalid spec", slog.F("watch_id", row.ID), slogError(err))
			continue
		}
		event.Type, event.URL = wakeupEventPullRequest, row.PullRequestUrl.String
		part := &parts[0]
		if slices.Contains(event.On, pullRequestEventChecks) {
			part = &parts[1]
		}
		part.rows, part.events = append(part.rows, row), append(part.events, event)
	}
	for i, part := range parts {
		if len(part.rows) > 0 {
			p.readPullRequest(ctx, fdb, part.rows, part.events, i == 1, now)
		}
	}
}

// readPullRequest reads one pull request as one owner, with its checks when
// includeChecks is set, for the watches in rows with the parsed events, and
// records the read on each of them.
func (p *Server) readPullRequest(ctx context.Context, fdb forkdb.Store, rows []forkdb.ClaimDuePullRequestWatchesRow, events []wakeupEvent, includeChecks bool, now time.Time) {
	var (
		since       time.Time
		viewerLogin string
	)
	for _, event := range events {
		if since.IsZero() || event.Since.Before(since) {
			since = event.Since
		}
		viewerLogin = cmp.Or(viewerLogin, event.ViewerLogin)
	}
	url := rows[0].PullRequestUrl.String
	readCtx, cancel := context.WithTimeout(ctx, pullRequestReadTimeout)
	activity, err := p.forkPullRequestActivity(readCtx, rows[0].OwnerID, url, since, includeChecks, viewerLogin)
	cancel()
	if err != nil {
		next := now.Add(pullRequestErrorRetry)
		if rateLimit, ok := errors.AsType[*gitprovider.RateLimitError](err); ok {
			next = now.Add(pullRequestPollInterval)
			if rateLimit.RetryAfter.After(next) {
				next = rateLimit.RetryAfter
			}
		} else if ctx.Err() == nil {
			p.logger.Warn(ctx, "pull request poller could not read a pull request",
				slog.F("pull_request_url", url),
				slog.F("owner_id", rows[0].OwnerID),
				slogError(err),
			)
		}
		for _, row := range rows {
			p.recordPullRequestPoll(ctx, fdb, row, pullRequestPollPatch{NextPollAt: next}, "")
		}
		return
	}
	for i, row := range rows {
		patch := pullRequestPollPatch{ViewerLogin: activity.ViewerLogin, PolledAt: now}
		text := pullRequestWakeupText(events[i], activity, row.WakeupExpiresAt.Time)
		if text == "" {
			patch.NextPollAt = now.Add(pullRequestPollInterval)
			// The last read of the watch starts at its expiry, so it
			// covers the whole lifetime.
			if expiresAt := row.WakeupExpiresAt; expiresAt.Valid && now.Before(expiresAt.Time) && patch.NextPollAt.After(expiresAt.Time) {
				patch.NextPollAt = expiresAt.Time
			}
		}
		p.recordPullRequestPoll(ctx, fdb, row, patch, text)
	}
}

// recordPullRequestPoll merges patch into the spec of the watch in row and,
// with a text, fires the watch with it and pokes the waker for its chat.
func (p *Server) recordPullRequestPoll(ctx context.Context, fdb forkdb.Store, row forkdb.ClaimDuePullRequestWatchesRow, patch pullRequestPollPatch, text string) {
	err := func() error {
		specPatch, err := json.Marshal(patch)
		if err != nil {
			return xerrors.Errorf("encode spec patch: %w", err)
		}
		params := forkdb.RecordPullRequestWatchPollParams{ID: row.ID, WakeupID: row.WakeupID, SpecPatch: specPatch}
		if text != "" {
			payload, err := json.Marshal(pullRequestFired{Text: text})
			if err != nil {
				return xerrors.Errorf("encode fired payload: %w", err)
			}
			params.FiredAt = sql.NullTime{Time: patch.PolledAt, Valid: true}
			params.FiredPayload.RawMessage, params.FiredPayload.Valid = payload, true
		}
		fired, err := fdb.RecordPullRequestWatchPoll(ctx, params)
		if err != nil {
			return xerrors.Errorf("record pull request watch poll: %w", err)
		}
		if text != "" && fired == 1 {
			p.forkWaker().poke(row.ChatID)
		}
		return nil
	}()
	if err != nil && ctx.Err() == nil {
		p.logger.Warn(ctx, "pull request poller could not record a read",
			slog.F("watch_id", row.ID),
			slog.F("chat_id", row.ChatID),
			slogError(err),
		)
	}
}
