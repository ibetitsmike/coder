package gitprovider

// This file is fork code: it reads a GitHub pull request's reviews,
// comments, state and CI since a time, for the wait_for_events tool of
// ibetitsmike/coder.

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

// PullRequestActivity is what happened on a pull request since a time,
// leaving out what the token's own user did.
type PullRequestActivity struct {
	// State is open, closed or merged. A merged pull request is merged
	// even though GitHub reports it as closed.
	State   PRState
	HeadSHA string
	// Reviews are the reviews submitted at or after the time, oldest
	// first.
	Reviews []PullRequestEvent
	// Comments are the issue comments and review comments created at or
	// after the time, oldest first.
	Comments []PullRequestEvent
	// Checks summarizes the CI of HeadSHA.
	Checks CheckSummary
}

// PullRequestEvent is one review or comment on a pull request.
type PullRequestEvent struct {
	ID     int64
	Author string
	// State is the lower-case GitHub review state (approved,
	// changes_requested, commented, dismissed) for reviews and empty for
	// comments.
	State string
	// URL is the event's html_url.
	URL string
	// CreatedAt is submitted_at for reviews and created_at for comments.
	CreatedAt time.Time
}

// CheckState is the summarized CI state of a commit.
type CheckState string

const (
	// CheckStateNone means the commit has no check runs and no commit
	// statuses.
	CheckStateNone CheckState = "none"
	// CheckStatePending means a check run has not completed, or a commit
	// status is pending.
	CheckStatePending CheckState = "pending"
	// CheckStateSuccess means every check run and commit status passed.
	CheckStateSuccess CheckState = "success"
	// CheckStateFailure means a check run or commit status failed.
	CheckStateFailure CheckState = "failure"
)

// CheckSummary is the CI state of a commit over its check runs and commit
// statuses.
type CheckSummary struct {
	State CheckState
	// Total counts check runs and commit statuses.
	Total int
	// Failed counts failed check runs, plus one when the combined commit
	// status failed.
	Failed int
}

// ErrPullRequestActivityUnsupported is returned by FetchPullRequestActivity
// for providers other than GitHub.
var ErrPullRequestActivityUnsupported = xerrors.New("pull request activity is only available for GitHub")

// FetchPullRequestActivity reads the activity of the pull request ref since
// since. Reviews and comments by the token's own user are left out, so an
// agent that acts with the user's token does not see its own activity.
// Only GitHub is supported. Rate limit errors are *RateLimitError.
func FetchPullRequestActivity(
	ctx context.Context,
	p Provider,
	token string,
	ref PRRef,
	since time.Time,
) (*PullRequestActivity, error) {
	g, ok := p.(*githubProvider)
	if !ok {
		return nil, ErrPullRequestActivityUnsupported
	}

	var viewer struct {
		Login string `json:"login"`
	}
	if err := g.decodeJSON(ctx, g.apiBaseURL+"/user", token, &viewer); err != nil {
		return nil, xerrors.Errorf("fetch github viewer: %w", err)
	}

	repoEndpoint := fmt.Sprintf(
		"%s/repos/%s/%s",
		g.apiBaseURL,
		url.PathEscape(ref.Owner),
		url.PathEscape(ref.Repo),
	)
	pullEndpoint := fmt.Sprintf("%s/pulls/%d", repoEndpoint, ref.Number)

	var pull struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := g.decodeJSON(ctx, pullEndpoint, token, &pull); err != nil {
		return nil, xerrors.Errorf("fetch pull request: %w", err)
	}
	state := PRState(strings.ToLower(strings.TrimSpace(pull.State)))
	if pull.Merged {
		state = PRStateMerged
	}

	isOwn := func(login string) bool {
		return viewer.Login != "" && strings.EqualFold(login, viewer.Login)
	}

	var reviews []struct {
		ID          int64     `json:"id"`
		State       string    `json:"state"`
		HTMLURL     string    `json:"html_url"`
		SubmittedAt time.Time `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	// Only the first 100 reviews are read, like FetchPullRequestStatus.
	// GitHub lists reviews oldest first, so on a pull request with more
	// than 100 reviews the newest ones are missed.
	if err := g.decodeJSON(ctx, pullEndpoint+"/reviews?per_page=100", token, &reviews); err != nil {
		return nil, xerrors.Errorf("fetch pull request reviews: %w", err)
	}
	activity := &PullRequestActivity{
		State:   state,
		HeadSHA: pull.Head.SHA,
		Checks:  CheckSummary{State: CheckStateNone},
	}
	for _, r := range reviews {
		reviewState := strings.ToLower(strings.TrimSpace(r.State))
		// Pending reviews are drafts that are not submitted yet.
		if reviewState == "" || reviewState == "pending" ||
			r.SubmittedAt.Before(since) || isOwn(r.User.Login) {
			continue
		}
		activity.Reviews = append(activity.Reviews, PullRequestEvent{
			ID:        r.ID,
			Author:    r.User.Login,
			State:     reviewState,
			URL:       r.HTMLURL,
			CreatedAt: r.SubmittedAt,
		})
	}
	sortPullRequestEvents(activity.Reviews)

	commentsQuery := "?since=" + url.QueryEscape(since.UTC().Format(time.RFC3339)) + "&per_page=100"
	for _, endpoint := range []struct {
		name string
		url  string
	}{
		{name: "issue comments", url: fmt.Sprintf("%s/issues/%d/comments", repoEndpoint, ref.Number)},
		{name: "review comments", url: pullEndpoint + "/comments"},
	} {
		var comments []struct {
			ID        int64     `json:"id"`
			HTMLURL   string    `json:"html_url"`
			CreatedAt time.Time `json:"created_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := g.decodeJSON(ctx, endpoint.url+commentsQuery, token, &comments); err != nil {
			return nil, xerrors.Errorf("fetch pull request %s: %w", endpoint.name, err)
		}
		for _, c := range comments {
			// GitHub's since filters on updated_at, so an old comment
			// that was edited is listed too.
			if c.CreatedAt.Before(since) || isOwn(c.User.Login) {
				continue
			}
			activity.Comments = append(activity.Comments, PullRequestEvent{
				ID:        c.ID,
				Author:    c.User.Login,
				URL:       c.HTMLURL,
				CreatedAt: c.CreatedAt,
			})
		}
	}
	sortPullRequestEvents(activity.Comments)

	if pull.Head.SHA == "" {
		return activity, nil
	}
	commitEndpoint := repoEndpoint + "/commits/" + url.PathEscape(pull.Head.SHA)

	// GitHub keeps every run of a commit, so a check that was cancelled and
	// then run again appears twice. Only the newest run of each check name
	// and app counts, which matches GitHub's status rollup, but two
	// workflows with a job of the same name also count as one check. Only
	// the first 100 runs are read, a known limit like the reviews.
	var checkRuns struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			App        struct {
				ID int64 `json:"id"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	if err := g.decodeJSON(ctx, commitEndpoint+"/check-runs?per_page=100", token, &checkRuns); err != nil {
		return nil, xerrors.Errorf("fetch commit check runs: %w", err)
	}
	type checkKey struct {
		appID int64
		name  string
	}
	latest := make(map[checkKey]int, len(checkRuns.CheckRuns))
	for i, run := range checkRuns.CheckRuns {
		key := checkKey{appID: run.App.ID, name: run.Name}
		if j, ok := latest[key]; !ok || run.ID > checkRuns.CheckRuns[j].ID {
			latest[key] = i
		}
	}
	var combined struct {
		State      string `json:"state"`
		TotalCount int    `json:"total_count"`
	}
	if err := g.decodeJSON(ctx, commitEndpoint+"/status", token, &combined); err != nil {
		return nil, xerrors.Errorf("fetch commit status: %w", err)
	}

	checks := CheckSummary{Total: len(latest) + combined.TotalCount}
	pending := false
	for _, i := range latest {
		run := checkRuns.CheckRuns[i]
		if !strings.EqualFold(run.Status, "completed") {
			pending = true
			continue
		}
		switch strings.ToLower(run.Conclusion) {
		case "failure", "timed_out", "cancelled", "action_required", "startup_failure":
			checks.Failed++
		}
	}
	// The combined status is pending when the commit has no statuses at
	// all, so it only counts when there are statuses.
	if combined.TotalCount > 0 {
		switch strings.ToLower(combined.State) {
		case "failure", "error":
			checks.Failed++
		case "pending":
			pending = true
		}
	}
	switch {
	case checks.Failed > 0:
		checks.State = CheckStateFailure
	case pending:
		checks.State = CheckStatePending
	case checks.Total > 0:
		checks.State = CheckStateSuccess
	default:
		checks.State = CheckStateNone
	}
	activity.Checks = checks
	return activity, nil
}

func sortPullRequestEvents(events []PullRequestEvent) {
	slices.SortFunc(events, func(a, b PullRequestEvent) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}
