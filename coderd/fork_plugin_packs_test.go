package coderd_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/audit"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/pluginpacks/pluginpackstest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkPluginPacks(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	repo := pluginpackstest.New(t)
	client, _, api := coderdtest.NewWithAPI(t, &coderdtest.Options{
		DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
			dv.Experiments = []string{string(codersdk.ExperimentForkPluginPacks)}
		}),
	})
	api.HTTPClient = repo.Client
	owner := coderdtest.CreateFirstUser(t, client)
	orgID := owner.OrganizationID
	member, _ := coderdtest.CreateAnotherUser(t, client, orgID)
	auditor, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAuditor(orgID))
	install := codersdk.CreateForkPluginPackRequest{Name: "demo", SourceURL: repo.URL, SourcePath: "pack", Ref: "main"}

	// No host is allowed until an admin allows one.
	_, err := client.CreateForkPluginPack(ctx, orgID, install)
	require.ErrorContains(t, requireSDKError(t, err, http.StatusBadRequest), "not an allowed plugin pack source")
	_, err = client.UpdateForkPluginPackSources(ctx, orgID, codersdk.ForkPluginPackSources{Hosts: []string{"https://github.com"}})
	_ = requireSDKError(t, err, http.StatusBadRequest)
	sources, err := client.UpdateForkPluginPackSources(ctx, orgID, codersdk.ForkPluginPackSources{Hosts: []string{"127.0.0.1", "GitHub.com", "github.com"}})
	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1", "github.com"}, sources.Hosts)

	// Members cannot see packs, and auditors cannot change them. Neither
	// makes the server fetch.
	before := repo.Requests()
	_, err = member.CreateForkPluginPack(ctx, orgID, install)
	_ = requireSDKError(t, err, http.StatusNotFound)
	_, err = member.ForkPluginPackSources(ctx, orgID)
	_ = requireSDKError(t, err, http.StatusNotFound)
	_, err = auditor.CreateForkPluginPack(ctx, orgID, install)
	_ = requireSDKError(t, err, http.StatusForbidden)
	require.Equal(t, before, repo.Requests())

	pack, err := client.CreateForkPluginPack(ctx, orgID, install)
	require.NoError(t, err)
	require.Equal(t, repo.Second, pack.CommitSHA)
	require.Equal(t, "main", pack.Ref)
	paths := make([]string, 0, len(pack.Files))
	for _, f := range pack.Files {
		paths = append(paths, f.Path)
	}
	require.ElementsMatch(t, []string{"agents/reviewer.md", "skills/demo/SKILL.md", "skills/demo/script.sh"}, paths)

	_, err = client.CreateForkPluginPack(ctx, orgID, install)
	_ = requireSDKError(t, err, http.StatusConflict)
	_, err = client.CreateForkPluginPack(ctx, orgID, codersdk.CreateForkPluginPackRequest{
		Name: "other", SourceURL: strings.Replace(repo.URL, "127.0.0.1", "localhost", 1), Ref: "main",
	})
	_ = requireSDKError(t, err, http.StatusBadRequest)

	// An update pins the commit of the new ref and replaces the files.
	updated, err := client.UpdateForkPluginPack(ctx, orgID, pack.ID, codersdk.UpdateForkPluginPackRequest{Ref: "v1"})
	require.NoError(t, err)
	require.Equal(t, repo.First, updated.CommitSHA)
	require.Equal(t, "v1", updated.Ref)
	require.Len(t, updated.Files, 3)
	require.NotEqual(t, skillHash(t, pack), skillHash(t, updated))

	packs, err := auditor.ForkPluginPacks(ctx, orgID)
	require.NoError(t, err)
	require.Len(t, packs, 1)
	require.Empty(t, packs[0].Files)
	_, err = auditor.UpdateForkPluginPack(ctx, orgID, pack.ID, codersdk.UpdateForkPluginPackRequest{})
	_ = requireSDKError(t, err, http.StatusForbidden)
	_, err = member.ForkPluginPack(ctx, orgID, pack.ID)
	_ = requireSDKError(t, err, http.StatusNotFound)

	require.NoError(t, client.DeleteForkPluginPack(ctx, orgID, pack.ID))
	_, err = client.ForkPluginPack(ctx, orgID, pack.ID)
	_ = requireSDKError(t, err, http.StatusNotFound)
}

// TestForkPluginPacksAudit checks that every change to the sources and packs
// of an organization is audited, and that a denied request is not.
func TestForkPluginPacksAudit(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	repo := pluginpackstest.New(t)
	auditor := audit.NewMock()
	client, _, api := coderdtest.NewWithAPI(t, &coderdtest.Options{
		Auditor: auditor,
		DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
			dv.Experiments = []string{string(codersdk.ExperimentForkPluginPacks)}
		}),
	})
	api.HTTPClient = repo.Client
	owner := coderdtest.CreateFirstUser(t, client)
	orgID := owner.OrganizationID
	member, _ := coderdtest.CreateAnotherUser(t, client, orgID)

	_, err := client.UpdateForkPluginPackSources(ctx, orgID, codersdk.ForkPluginPackSources{Hosts: []string{"127.0.0.1"}})
	require.NoError(t, err)
	install := codersdk.CreateForkPluginPackRequest{Name: "demo", SourceURL: repo.URL, SourcePath: "pack", Ref: "main"}
	_, err = member.CreateForkPluginPack(ctx, orgID, install)
	_ = requireSDKError(t, err, http.StatusNotFound)
	pack, err := client.CreateForkPluginPack(ctx, orgID, install)
	require.NoError(t, err)
	_, err = client.UpdateForkPluginPack(ctx, orgID, pack.ID, codersdk.UpdateForkPluginPackRequest{Ref: "v1"})
	require.NoError(t, err)
	require.NoError(t, client.DeleteForkPluginPack(ctx, orgID, pack.ID))

	sources := forkAuditEntries(t, auditor, orgID, "plugin_pack_sources")
	require.Len(t, sources, 1)
	require.Equal(t, "update", sources[0].Operation)
	require.JSONEq(t, `[]`, string(sources[0].Changes["hosts"].Old))
	require.JSONEq(t, `["127.0.0.1"]`, string(sources[0].Changes["hosts"].New))

	entries := forkAuditEntries(t, auditor, orgID, "plugin_pack")
	require.Len(t, entries, 3, "the denied install is not audited")
	for i, op := range []string{"create", "update", "delete"} {
		require.Equal(t, op, entries[i].Operation)
		require.Equal(t, pack.ID.String(), entries[i].ResourceID)
		require.Equal(t, "demo", entries[i].ResourceName)
		require.Less(t, entries[i].Log.StatusCode, int32(http.StatusMultipleChoices))
	}
	require.JSONEq(t, `"`+repo.Second+`"`, string(entries[0].Changes["commit_sha"].New))
	require.JSONEq(t, `"main"`, string(entries[1].Changes["ref"].Old))
	require.JSONEq(t, `"v1"`, string(entries[1].Changes["ref"].New))
	require.JSONEq(t, `"`+repo.First+`"`, string(entries[1].Changes["commit_sha"].New))
	require.JSONEq(t, `"demo"`, string(entries[2].Changes["name"].Old))
	require.Empty(t, entries[2].Changes["name"].New)
}

func skillHash(t *testing.T, pack codersdk.ForkPluginPack) string {
	t.Helper()
	for _, f := range pack.Files {
		if f.Path == "skills/demo/SKILL.md" {
			return f.ContentHash
		}
	}
	t.Fatalf("pack has no skills/demo/SKILL.md")
	return ""
}
