package coderd

// This file is fork code: it reads pull request activity as a chat owner for
// the wait_for_events tool of the fork-chat-wakeups experiment.

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
	"github.com/coder/coder/v2/coderd/x/gitsync"
)

// forkChatPullRequestActivity reads the activity of the pull request at
// pullRequestURL since since, with the external auth token that the chat
// owner ownerID linked for its provider.
func (api *API) forkChatPullRequestActivity(ctx context.Context, ownerID uuid.UUID, pullRequestURL string, since time.Time) (*gitprovider.PullRequestActivity, error) {
	gp := api.resolveGitProvider(ctx, pullRequestURL)
	if gp == nil {
		return nil, xerrors.Errorf("no git provider is configured for %s", pullRequestURL)
	}
	ref, ok := gp.ParsePullRequestURL(pullRequestURL)
	if !ok {
		return nil, xerrors.Errorf("%s is not a pull request URL", pullRequestURL)
	}
	token, err := api.resolveChatGitAccessToken(ctx, ownerID, pullRequestURL)
	if errors.Is(err, gitsync.ErrNoTokenAvailable) || (err == nil && token == nil) {
		host := pullRequestURL
		if parsed, parseErr := url.Parse(pullRequestURL); parseErr == nil && parsed.Host != "" {
			host = parsed.Host
		}
		return nil, xerrors.Errorf("the chat owner has no linked account for %s", host)
	}
	if err != nil {
		return nil, xerrors.Errorf("resolve git access token: %w", err)
	}
	return gitprovider.FetchPullRequestActivity(ctx, gp, *token, ref, since)
}
