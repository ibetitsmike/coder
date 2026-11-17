package agentapi

// This file is fork code: it fills hash-only context rows
// (fork-context-hash-rows). An agent sends a body digest in place of a body
// that the previous accepted push on its connection stored. The body is taken
// from the stored row only when that row has the same kind and its body has
// the digest, so the row written is exactly the row the agent would have sent
// in full.

import (
	"bytes"
	"context"

	"golang.org/x/xerrors"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"cdr.dev/slog/v3"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
)

// forkFillHashRows replaces every hash-only row of req with the body coderd
// stores for its source. It runs before the request is validated, so the
// filled bodies count against the body caps like bodies sent in full. Any
// row it cannot fill fails the push; the agent then sends it in full.
func (a *ContextAPI) forkFillHashRows(ctx context.Context, req *agentproto.PushContextStateRequest) error {
	var elided []*agentproto.ContextResource
	for _, r := range req.GetResources() {
		if r != nil && len(r.GetForkBodyDigest()) > 0 {
			elided = append(elided, r)
		}
	}
	if len(elided) == 0 {
		return nil
	}
	if !a.ForkContextHashRows {
		return xerrors.New("agentapi: PushContextState has hash-only rows, but fork-context-hash-rows is not enabled")
	}

	if a.Workspace != nil {
		if injected, err := a.Workspace.ContextInject(ctx); err == nil {
			ctx = injected
		} else {
			a.Log.Debug(ctx, "failed to inject cached workspace RBAC object", slog.Error(err))
		}
	}
	stored, err := a.Database.ListWorkspaceAgentContextResources(ctx, a.AgentID)
	if err != nil {
		return xerrors.Errorf("agentapi: list stored context resources: %w", err)
	}
	bySource := make(map[string]database.WorkspaceAgentContextResource, len(stored))
	for _, s := range stored {
		bySource[s.Source] = s
	}

	for _, r := range elided {
		if err := forkFillHashRow(r, bySource); err != nil {
			return xerrors.Errorf("agentapi: hash-only resource %q: %w", r.GetSource(), err)
		}
	}
	return nil
}

func forkFillHashRow(r *agentproto.ContextResource, stored map[string]database.WorkspaceAgentContextResource) error {
	digest := r.GetForkBodyDigest()
	if len(digest) != agentproto.ForkContextBodyDigestSize {
		return xerrors.Errorf("body digest is %d bytes, want %d", len(digest), agentproto.ForkContextBodyDigestSize)
	}
	body := agentproto.ForkContextBody(r)
	if body == nil {
		return xerrors.New("missing or unknown body variant")
	}
	if proto.Size(body) != 0 {
		return xerrors.New("carries both a body and a body digest")
	}
	kind, _, err := marshalContextResourceBody(r)
	if err != nil {
		return err
	}
	row, ok := stored[r.GetSource()]
	if !ok {
		return xerrors.New("no stored row")
	}
	if row.BodyKind != kind {
		return xerrors.Errorf("stored row has kind %s, want %s", row.BodyKind, kind)
	}

	filled := agentproto.ForkResetContextBody(r)
	if err := protojson.Unmarshal(row.Body, filled); err != nil {
		return xerrors.Errorf("decode stored body: %w", err)
	}
	got, err := agentproto.ForkContextBodyDigest(filled)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, digest) {
		return xerrors.New("stored body does not match the body digest")
	}
	r.ForkBodyDigest = nil
	return nil
}
