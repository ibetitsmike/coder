package coderd

// This file is fork code: it reads pull request activity as a chat owner for
// the wait_for_events tool of the fork-chat-wakeups experiment.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/externalauth"
	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
)

// forkChatPullRequestActivity reads the activity of the pull request at
// pullRequestURL since since, and its CI when includeChecks is true, with the
// external auth token that the chat owner ownerID linked for its provider. A
// non-empty viewerLogin is the ViewerLogin of an earlier result. The result's
// ViewerLogin is bound to the link, see forkViewerKey.
func (api *API) forkChatPullRequestActivity(ctx context.Context, ownerID uuid.UUID, pullRequestURL string, since time.Time, includeChecks bool, viewerLogin string) (*gitprovider.PullRequestActivity, error) {
	config, gp := api.forkPullRequestProvider(ctx, pullRequestURL)
	if gp == nil {
		return nil, xerrors.Errorf("no git provider is configured for %s", pullRequestURL)
	}
	ref, ok := gp.ParsePullRequestURL(pullRequestURL)
	if !ok {
		return nil, xerrors.Errorf("%s is not a pull request URL", pullRequestURL)
	}
	// Only the matched provider's token may be used: a token of another
	// provider would be sent to this provider's host.
	//nolint:gocritic // The wake-up worker reads the owner's link without a user actor.
	link, err := api.Database.GetExternalAuthLink(dbauthz.AsSystemRestricted(ctx), database.GetExternalAuthLinkParams{
		ProviderID: config.ID,
		UserID:     ownerID,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, xerrors.Errorf("get external auth link: %w", err)
	}
	if err == nil && !link.OAuthToken().Valid() {
		// RefreshToken also validates an unexpired token, which costs a
		// request per poll. A revoked token fails the read instead.
		//nolint:gocritic // System context carried through for token refresh.
		if refreshed, refreshErr := config.RefreshToken(dbauthz.AsSystemRestricted(ctx), api.Database, link); refreshErr == nil {
			link = refreshed
		}
	}
	token := strings.TrimSpace(link.OAuthAccessToken)
	if token == "" {
		host := pullRequestURL
		if parsed, parseErr := url.Parse(pullRequestURL); parseErr == nil && parsed.Host != "" {
			host = parsed.Host
		}
		return nil, xerrors.Errorf("the chat owner has no linked account for %s", host)
	}
	login, ok := strings.CutPrefix(viewerLogin, forkViewerKey(link, ""))
	if !ok {
		login = ""
	}
	activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, token, ref, since, includeChecks, login)
	if err != nil {
		return nil, err
	}
	activity.ViewerLogin = forkViewerKey(link, activity.ViewerLogin)
	return activity, nil
}

// forkViewerKey binds the viewer login of a read to the external auth link
// whose token it was read with. A relink or token refresh updates the link,
// so the next read asks the provider for the login again.
func forkViewerKey(link database.ExternalAuthLink, login string) string {
	return strconv.FormatInt(link.UpdatedAt.UnixMicro(), 10) + ":" + login
}

// forkPullRequestProvider returns the first external auth config whose regex
// matches pullRequestURL and that has a git provider, the same config
// resolveGitProvider picks, with that provider.
func (api *API) forkPullRequestProvider(ctx context.Context, pullRequestURL string) (*externalauth.Config, gitprovider.Provider) {
	for _, config := range api.ExternalAuthConfigs {
		if config.Regex == nil || !config.Regex.MatchString(pullRequestURL) {
			continue
		}
		gp, err := config.Git()
		if err != nil {
			api.Logger.Warn(ctx, "failed to construct git provider",
				slog.F("provider_id", config.ID),
				slog.Error(err),
			)
			continue
		}
		if gp != nil {
			return config, gp
		}
	}
	return nil, nil
}
