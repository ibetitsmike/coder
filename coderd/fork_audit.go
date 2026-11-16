package coderd

// This file is fork code: it audits changes to fork resources. Fork
// resources live in the fork schema and have no audit resource type, so a
// change is audited as a write of the organization that owns the resource.
// The additional fields name the resource and hold the fields that changed.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/audit"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
)

const (
	forkAuditCreate = "create"
	forkAuditUpdate = "update"
	forkAuditDelete = "delete"
)

// forkAuditFields are the additional fields of the audit entry of a fork
// resource change.
type forkAuditFields struct {
	Resource     string                     `json:"fork_resource"`
	Operation    string                     `json:"operation"`
	ResourceID   string                     `json:"resource_id,omitempty"`
	ResourceName string                     `json:"resource_name,omitempty"`
	Changes      map[string]forkAuditChange `json:"changes,omitempty"`
}

// forkAuditChange is one changed field. A secret field records only that
// it changed.
type forkAuditChange struct {
	Old    json.RawMessage `json:"old,omitempty"`
	New    json.RawMessage `json:"new,omitempty"`
	Secret bool            `json:"secret,omitempty"`
}

type forkAudit struct {
	req    *audit.Request[database.Organization]
	fields *forkAuditFields
}

// initForkAudit starts the audit entry of a fork resource change. Nothing is
// written until target names the organization, so a request rejected before
// that leaves no entry, like the upstream handlers.
func (api *API) initForkAudit(rw http.ResponseWriter, r *http.Request, resource, operation string) (*forkAudit, func()) {
	fields := &forkAuditFields{Resource: resource, Operation: operation}
	req, commit := audit.InitRequest[database.Organization](rw, &audit.RequestParams{
		Audit:            *api.Auditor.Load(),
		Log:              api.Logger,
		Request:          r,
		Action:           database.AuditActionWrite,
		AdditionalFields: fields,
	})
	return &forkAudit{req: req, fields: fields}, commit
}

// target names the organization that owns the resource and the resource.
func (a *forkAudit) target(org database.Organization, id uuid.UUID, name string) {
	a.req.Old = org
	a.req.New = org
	a.req.UpdateOrganizationID(org.ID)
	if id != uuid.Nil {
		a.fields.ResourceID = id.String()
	}
	a.fields.ResourceName = name
}

// diff records the fields that differ between two audit views of the
// resource. A nil view stands for a resource that does not exist.
func (a *forkAudit) diff(old, updated any) {
	oldFields, newFields := forkAuditJSONFields(old), forkAuditJSONFields(updated)
	for key, value := range oldFields {
		if string(newFields[key]) != string(value) {
			a.change(key, forkAuditChange{Old: value, New: newFields[key]})
		}
	}
	for key, value := range newFields {
		if _, ok := oldFields[key]; !ok {
			a.change(key, forkAuditChange{New: value})
		}
	}
}

// secret records that a secret field changed without its values.
func (a *forkAudit) secret(key string) {
	a.change(key, forkAuditChange{Secret: true})
}

func (a *forkAudit) change(key string, change forkAuditChange) {
	if a.fields.Changes == nil {
		a.fields.Changes = map[string]forkAuditChange{}
	}
	a.fields.Changes[key] = change
}

func forkAuditJSONFields(view any) map[string]json.RawMessage {
	if view == nil {
		return nil
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

// forkAuditOrganization reads the organization of a resource whose route has
// no organization parameter. A failed read still audits under the ID.
func (api *API) forkAuditOrganization(ctx context.Context, id uuid.UUID) database.Organization {
	//nolint:gocritic // The handler authorized the change; this read only names the audit target.
	org, err := api.Database.GetOrganizationByID(dbauthz.AsSystemRestricted(ctx), id)
	if err != nil {
		api.Logger.Warn(ctx, "read organization for fork audit", slog.F("organization_id", id), slog.Error(err))
		return database.Organization{ID: id}
	}
	return org
}

// forkAuditURL drops credentials and the query from a URL, which may carry
// tokens.
func forkAuditURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
