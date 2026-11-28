package coderd_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// TestForkSandboxProvisionerType checks that a template version may name the
// sandbox provisioner only when the fork-sandbox-provisioner experiment is
// enabled.
func TestForkSandboxProvisionerType(t *testing.T) {
	t.Parallel()

	create := func(t *testing.T, experiments ...string) error {
		t.Helper()
		dv := coderdtest.DeploymentValues(t)
		dv.Experiments = experiments
		client := coderdtest.New(t, &coderdtest.Options{DeploymentValues: dv})
		user := coderdtest.CreateFirstUser(t, client)
		ctx := testutil.Context(t, testutil.WaitLong)

		file, err := client.Upload(ctx, codersdk.ContentTypeTar, bytes.NewReader(make([]byte, 1024)))
		require.NoError(t, err)
		_, err = client.CreateTemplateVersion(ctx, user.OrganizationID, codersdk.CreateTemplateVersionRequest{
			StorageMethod: codersdk.ProvisionerStorageMethodFile,
			FileID:        file.ID,
			Provisioner:   codersdk.ProvisionerType("sandbox"),
		})
		return err
	}

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		err := create(t)
		var apiErr *codersdk.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.StatusCode())
		require.Len(t, apiErr.Validations, 1)
		require.Equal(t, "provisioner", apiErr.Validations[0].Field)
	})

	t.Run("ExperimentOn", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, create(t, string(codersdk.ExperimentForkSandboxProvisioner)))
	})
}
