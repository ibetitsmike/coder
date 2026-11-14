package coderd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

const savedWorkflowScript = "def main(args):\n    return args\n"

func TestForkUserSavedWorkflows(t *testing.T) {
	t.Parallel()

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkWorkflows)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		orgID := owner.OrganizationID
		member, memberUser := coderdtest.CreateAnotherUser(t, client, orgID)
		other, _ := coderdtest.CreateAnotherUser(t, client, orgID)

		created, err := member.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{
			Name:        "triage",
			Description: "Triage new issues.",
			Script:      savedWorkflowScript,
			ArgsSchema:  json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"}}}`),
			Phases:      []string{"collect", "label"},
		})
		require.NoError(t, err)
		require.Equal(t, "triage", created.Name)
		require.Equal(t, orgID, created.OrganizationID)
		require.Equal(t, &memberUser.ID, created.UserID)
		require.Equal(t, &memberUser.ID, created.CreatedBy)
		require.Equal(t, savedWorkflowScript, created.Script)
		require.JSONEq(t, `{"type":"object","properties":{"repo":{"type":"string"}}}`, string(created.ArgsSchema))
		require.Equal(t, []string{"collect", "label"}, created.Phases)

		_, err = member.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "triage", Script: savedWorkflowScript})
		_ = requireSDKError(t, err, http.StatusConflict)
		for name, req := range map[string]codersdk.CreateForkSavedWorkflowRequest{
			"BadName":        {Name: "Triage", Script: savedWorkflowScript},
			"NoMain":         {Name: "no-main", Script: "x = 1\n"},
			"SyntaxError":    {Name: "syntax", Script: "def main(args):\n  return (\n"},
			"UnknownName":    {Name: "unknown", Script: "def main(args):\n    return open('x')\n"},
			"LargeScript":    {Name: "large", Script: savedWorkflowScript + strings.Repeat("#", 65536)},
			"ArraySchema":    {Name: "array-schema", Script: savedWorkflowScript, ArgsSchema: json.RawMessage(`[]`)},
			"StringSchema":   {Name: "string-schema", Script: savedWorkflowScript, ArgsSchema: json.RawMessage(`{"type":"string"}`)},
			"DuplicatePhase": {Name: "phases", Script: savedWorkflowScript, Phases: []string{"a", "a"}},
			"EmptyPhase":     {Name: "phases", Script: savedWorkflowScript, Phases: []string{""}},
		} {
			_, err = member.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, req)
			_ = requireSDKErrorStatus(t, err, http.StatusBadRequest, name)
		}

		list, err := member.ForkUserSavedWorkflows(ctx, orgID, memberUser.Username)
		require.NoError(t, err)
		require.Len(t, list, 1)
		requireSavedWorkflowMetadata(t, created.ForkSavedWorkflowMetadata, list[0])

		updated, err := member.UpdateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage", codersdk.UpdateForkSavedWorkflowRequest{
			Script: ptr.Ref("def main(args):\n    return 1\n"),
		})
		require.NoError(t, err)
		require.Equal(t, "def main(args):\n    return 1\n", updated.Script)
		require.Equal(t, "Triage new issues.", updated.Description, "a partial update keeps the other fields")
		require.JSONEq(t, string(created.ArgsSchema), string(updated.ArgsSchema))
		require.Equal(t, []string{"collect", "label"}, updated.Phases)

		updated, err = member.UpdateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage", codersdk.UpdateForkSavedWorkflowRequest{
			ArgsSchema: json.RawMessage(`null`),
			Phases:     &[]string{},
		})
		require.NoError(t, err)
		require.Empty(t, updated.ArgsSchema, "null removes the schema")
		require.Empty(t, updated.Phases)
		got, err := member.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err)
		require.Equal(t, updated, got)

		for _, req := range []codersdk.UpdateForkSavedWorkflowRequest{
			{},
			{Script: ptr.Ref("x = 1\n")},
		} {
			_, err = member.UpdateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage", req)
			_ = requireSDKError(t, err, http.StatusBadRequest)
		}
		_, err = member.UpdateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "missing", codersdk.UpdateForkSavedWorkflowRequest{Description: ptr.Ref("")})
		_ = requireSDKError(t, err, http.StatusNotFound)

		// Another member can see the user in the organization but not the
		// user's workflows, and cannot change them.
		_, err = other.ForkUserSavedWorkflows(ctx, orgID, memberUser.Username)
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = other.ForkUserSavedWorkflow(ctx, orgID, memberUser.Username, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = other.CreateForkUserSavedWorkflow(ctx, orgID, memberUser.Username, codersdk.CreateForkSavedWorkflowRequest{Name: "planted", Script: savedWorkflowScript})
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = other.UpdateForkUserSavedWorkflow(ctx, orgID, memberUser.Username, "triage", codersdk.UpdateForkSavedWorkflowRequest{Script: ptr.Ref(savedWorkflowScript)})
		_ = requireSDKError(t, err, http.StatusForbidden)
		err = other.DeleteForkUserSavedWorkflow(ctx, orgID, memberUser.Username, "triage")
		_ = requireSDKError(t, err, http.StatusForbidden)
		got, err = member.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err)
		require.Equal(t, updated.UpdatedAt, got.UpdatedAt, "the failed writes changed nothing")

		err = member.DeleteForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err)
		_, err = member.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, nil)
		owner := coderdtest.CreateFirstUser(t, client)

		_, err := client.ForkUserSavedWorkflows(ctx, owner.OrganizationID, codersdk.Me)
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = client.CreateForkUserSavedWorkflow(ctx, owner.OrganizationID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "triage", Script: savedWorkflowScript})
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}

// requireSavedWorkflowMetadata compares metadata whose args schemas the API
// may have indented differently.
func requireSavedWorkflowMetadata(t *testing.T, want, got codersdk.ForkSavedWorkflowMetadata) {
	t.Helper()
	if len(want.ArgsSchema) > 0 || len(got.ArgsSchema) > 0 {
		require.JSONEq(t, string(want.ArgsSchema), string(got.ArgsSchema))
	}
	want.ArgsSchema, got.ArgsSchema = nil, nil
	require.Equal(t, want, got)
}
