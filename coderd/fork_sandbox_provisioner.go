package coderd

// This file is fork code: it gates the sandbox provisioner type of the
// fork-sandbox-provisioner experiment. postTemplateVersionsByOrganization
// calls forkSandboxProvisionerAllowed once, so a deployment without the
// experiment rejects sandbox template versions and behaves like upstream.

import (
	"context"
	"net/http"

	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
)

// forkSandboxProvisionerAllowed reports whether a template version may use
// provisioner. The sandbox type needs the fork-sandbox-provisioner
// experiment. When it is off, the request fails with 400 and the function
// returns false.
func (api *API) forkSandboxProvisionerAllowed(ctx context.Context, rw http.ResponseWriter, provisioner codersdk.ProvisionerType) bool {
	if provisioner != codersdk.ProvisionerType(forkdb.ProvisionerTypeSandbox) ||
		api.Experiments.Enabled(codersdk.ExperimentForkSandboxProvisioner) {
		return true
	}
	httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
		Message: "The sandbox provisioner is not enabled.",
		Validations: []codersdk.ValidationError{{
			Field:  "provisioner",
			Detail: "The sandbox provisioner needs the " + string(codersdk.ExperimentForkSandboxProvisioner) + " experiment.",
		}},
	})
	return false
}
