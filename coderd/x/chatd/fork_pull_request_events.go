package chatd

// This file is fork code: it holds the pull request events of the
// wait_for_events tool of the fork-chat-wakeups experiment. The tool reads a
// baseline of the pull request, and the wake-up worker polls it until someone
// other than the chat owner acts on it.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
)

const (
	wakeupEventPullRequest = "pull_request"

	defaultPullRequestWaitPrompt = "Something you were waiting for happened. Check it, then continue."
	// pullRequestPollInterval keeps a wake-up well below the GitHub rate
	// limit of its owner's token.
	pullRequestPollInterval = time.Minute
	// pullRequestErrorRetry is the next poll after a failed read that is not
	// a rate limit, and the longest wait after a rate limit while child
	// entries of the same wake-up still need a run.
	pullRequestErrorRetry = 5 * time.Minute
	// pullRequestReadTimeout bounds one read by the worker, well within
	// wakeupLease, so a stalled provider cannot block the worker loop.
	pullRequestReadTimeout = 30 * time.Second
	// maxPullRequestSummaryItems caps the review and comment lines of one
	// wake-up message.
	maxPullRequestSummaryItems = 20

	pullRequestEventReview  = "review"
	pullRequestEventComment = "comment"
	pullRequestEventChecks  = "checks"
	pullRequestEventClosed  = "closed"
)

// pullRequestEventKinds are the valid pull_request_events values, in the
// order a wake-up stores them.
var pullRequestEventKinds = []string{pullRequestEventReview, pullRequestEventComment, pullRequestEventChecks, pullRequestEventClosed}

// ForkPullRequestActivityFunc reads the activity of a pull request since a
// time with the external auth token of the chat owner ownerID.
type ForkPullRequestActivityFunc func(ctx context.Context, ownerID uuid.UUID, pullRequestURL string, since time.Time) (*gitprovider.PullRequestActivity, error)

type waitForEventsPullRequest struct {
	URL         string   `json:"url"`
	On          []string `json:"on"`
	HeadSHA     string   `json:"head_sha"`
	ChecksState string   `json:"checks_state"`
}

// pullRequestWaitEvent reads the pull request at rawURL as the owner of chat
// and returns the pull_request event that watches it for kinds from now on.
func (p *Server) pullRequestWaitEvent(ctx context.Context, chat database.Chat, rawURL string, kinds []string) (wakeupEvent, error) {
	on := pullRequestEventKinds
	if len(kinds) > 0 {
		requested := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			kind = strings.TrimSpace(kind)
			if !slices.Contains(pullRequestEventKinds, kind) {
				return wakeupEvent{}, xerrors.Errorf("unknown pull_request_events value %q, valid values are %s", kind, strings.Join(pullRequestEventKinds, ", "))
			}
			requested = append(requested, kind)
		}
		on = slices.DeleteFunc(slices.Clone(pullRequestEventKinds), func(kind string) bool { return !slices.Contains(requested, kind) })
	}
	if p.forkPullRequestActivity == nil {
		return wakeupEvent{}, xerrors.New("pull request events are not available")
	}
	url := strings.TrimSpace(rawURL)
	since := p.clock.Now().UTC()
	activity, err := p.forkPullRequestActivity(ctx, chat.OwnerID, url, since)
	if err != nil {
		return wakeupEvent{}, xerrors.Errorf("read pull request %s: %w", url, err)
	}
	if activity.State != gitprovider.PRStateOpen {
		return wakeupEvent{}, xerrors.Errorf("pull request %s is already %s", url, activity.State)
	}
	// The provider counts events in the same second as since, so this read
	// can already list some. Remember them so a poll does not report them.
	var seen []string
	for _, e := range slices.Concat(activity.Reviews, activity.Comments) {
		seen = append(seen, e.URL)
	}
	return wakeupEvent{
		Type:        wakeupEventPullRequest,
		URL:         url,
		On:          on,
		Since:       since,
		HeadSHA:     activity.HeadSHA,
		ChecksState: string(activity.Checks.State),
		Seen:        seen,
	}, nil
}

// pullRequestRunKey names the run of a pull_request event, so each pull
// request wakes the chat once.
func pullRequestRunKey(url string) string {
	return wakeupEventPullRequest + ":" + url
}

// pullRequestWakeupText reads the pull request of event as the owner of chat
// and returns the summary of its activity, or "" when nothing event watches
// for happened.
func (p *Server) pullRequestWakeupText(ctx context.Context, chat database.Chat, event wakeupEvent) (string, error) {
	if p.forkPullRequestActivity == nil {
		return "", xerrors.New("pull request events are not available")
	}
	ctx, cancel := context.WithTimeout(ctx, pullRequestReadTimeout)
	defer cancel()
	activity, err := p.forkPullRequestActivity(ctx, chat.OwnerID, event.URL, event.Since)
	if err != nil {
		return "", err
	}
	isSeen := func(e gitprovider.PullRequestEvent) bool { return slices.Contains(event.Seen, e.URL) }
	activity.Reviews = slices.DeleteFunc(activity.Reviews, isSeen)
	activity.Comments = slices.DeleteFunc(activity.Comments, isSeen)
	if !pullRequestReady(event, activity) {
		return "", nil
	}
	return pullRequestSummary(event, activity), nil
}

// pullRequestReady reports whether activity has something event watches for.
// Checks count once they settle on a state the baseline did not have, either
// on a new head commit or with a new result.
func pullRequestReady(event wakeupEvent, activity *gitprovider.PullRequestActivity) bool {
	settled := activity.Checks.State == gitprovider.CheckStateSuccess || activity.Checks.State == gitprovider.CheckStateFailure
	changed := activity.HeadSHA != event.HeadSHA || string(activity.Checks.State) != event.ChecksState
	closed := activity.State == gitprovider.PRStateClosed || activity.State == gitprovider.PRStateMerged
	return (slices.Contains(event.On, pullRequestEventReview) && len(activity.Reviews) > 0) ||
		(slices.Contains(event.On, pullRequestEventComment) && len(activity.Comments) > 0) ||
		(slices.Contains(event.On, pullRequestEventChecks) && settled && changed) ||
		(slices.Contains(event.On, pullRequestEventClosed) && closed)
}

// pullRequestSummary lists the activity of the kinds event watches for. It
// leaves out review and comment bodies: anyone who can comment writes them,
// so they could inject instructions into the chat.
func pullRequestSummary(event wakeupEvent, activity *gitprovider.PullRequestActivity) string {
	type item struct {
		line      string
		createdAt time.Time
	}
	var items []item
	if slices.Contains(event.On, pullRequestEventReview) {
		for _, review := range activity.Reviews {
			items = append(items, item{fmt.Sprintf("- review by %s: %s (%s)", review.Author, review.State, review.URL), review.CreatedAt})
		}
	}
	if slices.Contains(event.On, pullRequestEventComment) {
		for _, comment := range activity.Comments {
			items = append(items, item{fmt.Sprintf("- comment by %s (%s)", comment.Author, comment.URL), comment.CreatedAt})
		}
	}
	slices.SortStableFunc(items, func(a, b item) int { return a.createdAt.Compare(b.createdAt) })

	lines := []string{fmt.Sprintf("Pull request %s has new activity:", event.URL)}
	for i, item := range items {
		if i == maxPullRequestSummaryItems {
			lines = append(lines, fmt.Sprintf("- and %d more", len(items)-i))
			break
		}
		lines = append(lines, item.line)
	}
	if slices.Contains(event.On, pullRequestEventChecks) {
		sha := activity.HeadSHA
		if len(sha) > 12 {
			sha = sha[:12]
		}
		lines = append(lines, fmt.Sprintf("- checks: %s, %d of %d failed, on %s", activity.Checks.State, activity.Checks.Failed, activity.Checks.Total, sha))
	}
	if slices.Contains(event.On, pullRequestEventClosed) && activity.State != gitprovider.PRStateOpen {
		lines = append(lines, fmt.Sprintf("- state: %s", activity.State))
	}
	return strings.Join(lines, "\n")
}

// pullRequestReadFailed ends the claim of wakeup after its pull request could
// not be read: it polls again later and never pauses or completes early. A
// rate limit waits until the limit resets, or at most pullRequestErrorRetry
// while childrenPending, so the children are still polled.
func (p *Server) pullRequestReadFailed(ctx context.Context, wakeup forkdb.ChatWakeup, now time.Time, url string, childrenPending bool, err error) wakeupFinish {
	if rateLimit, ok := errors.AsType[*gitprovider.RateLimitError](err); ok {
		next := now.Add(pullRequestPollInterval)
		if rateLimit.RetryAfter.After(next) {
			next = rateLimit.RetryAfter
		}
		if latest := now.Add(pullRequestErrorRetry); childrenPending && next.After(latest) {
			next = latest
		}
		return eventsWakeupRunAt(wakeup, now, 0, next)
	}
	p.logger.Warn(ctx, "chat wake-up could not read its pull request",
		slog.F("wakeup_id", wakeup.ID),
		slog.F("chat_id", wakeup.ChatID),
		slog.F("pull_request_url", url),
		slog.Error(err),
	)
	return eventsWakeupRunAt(wakeup, now, 0, now.Add(pullRequestErrorRetry))
}
