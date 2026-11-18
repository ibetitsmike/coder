package agentcontext

// This file is fork code: hash-only context rows (fork-context-hash-rows).
// Once the serving coderd advertises fork_context_hash_rows, a push on the
// same connection sends a digest in place of each body that the previous
// accepted settled push already stored. coderd fills those bodies from its
// stored rows and checks the digest, so a push after one file changed carries
// one body instead of every body.

import (
	"bytes"
	"context"
	"sync"

	"google.golang.org/protobuf/proto"

	agentproto "github.com/coder/coder/v2/agent/proto"
)

// forkHashRowMinBodyBytes is the smallest encoded body worth eliding. A
// smaller body costs about as much on the wire as its digest.
const forkHashRowMinBodyBytes = 64

// forkHashRows holds the per-connection state of hash-only rows. A
// DRPCPusher lives for one connection, so a reconnect starts with nothing
// acknowledged and sends every body in full.
type forkHashRows struct {
	mu sync.Mutex
	// enabled mirrors fork_context_hash_rows from the latest response.
	enabled bool
	// acked maps a source to the body digest of the row the latest accepted
	// settled push stored for it.
	acked map[string][]byte
}

// push sends req to client, eliding the bodies coderd already stores. When a
// push with elided bodies fails, it is sent once more in full right away, so
// a server that cannot fill a body costs one round trip instead of a backoff.
func (h *forkHashRows) push(ctx context.Context, client agentproto.DRPCAgentClient210, req *agentproto.PushContextStateRequest) (*agentproto.PushContextStateResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digests := make([][]byte, len(req.GetResources()))
	for i, r := range req.GetResources() {
		if d, err := agentproto.ForkContextBodyDigest(agentproto.ForkContextBody(r)); err == nil {
			digests[i] = d
		}
	}

	sent := h.elide(req, digests)
	resp, err := client.PushContextState(ctx, sent)
	if err != nil && sent != req && ctx.Err() == nil {
		h.acked = nil
		resp, err = client.PushContextState(ctx, req)
	}
	h.record(req, digests, resp, err)
	return resp, err
}

// elide returns req with a digest in place of each body the server already
// stores. It returns req itself when nothing is elided. It never changes req:
// the rows it elides are copies, so push can resend req in full.
func (h *forkHashRows) elide(req *agentproto.PushContextStateRequest, digests [][]byte) *agentproto.PushContextStateRequest {
	if !h.enabled || len(h.acked) == 0 || req.GetCapabilityGeneration() != 0 {
		return req
	}
	var out *agentproto.PushContextStateRequest
	for i, r := range req.GetResources() {
		acked, ok := h.acked[r.GetSource()]
		if !ok || digests[i] == nil || !bytes.Equal(acked, digests[i]) {
			continue
		}
		if proto.Size(agentproto.ForkContextBody(r)) < forkHashRowMinBodyBytes {
			continue
		}
		if out == nil {
			out = &agentproto.PushContextStateRequest{
				Version:              req.GetVersion(),
				AggregateHash:        req.GetAggregateHash(),
				Initial:              req.GetInitial(),
				SnapshotError:        req.GetSnapshotError(),
				CapabilityGeneration: req.GetCapabilityGeneration(),
				Resources:            append([]*agentproto.ContextResource(nil), req.GetResources()...),
			}
		}
		row := &agentproto.ContextResource{
			Source:         r.GetSource(),
			SourcePath:     r.SourcePath,
			ContentHash:    r.GetContentHash(),
			SizeBytes:      r.GetSizeBytes(),
			Status:         r.GetStatus(),
			Error:          r.GetError(),
			Body:           r.GetBody(),
			ForkBodyDigest: digests[i],
		}
		agentproto.ForkResetContextBody(row)
		out.Resources[i] = row
	}
	if out == nil {
		return req
	}
	return out
}

// record updates the state from the outcome of a push. An accepted settled
// push stored every row of req, so their digests become the acknowledged
// set. A probe stores nothing and keeps it. A failed or rejected push leaves
// the stored rows unknown and clears it.
func (h *forkHashRows) record(req *agentproto.PushContextStateRequest, digests [][]byte, resp *agentproto.PushContextStateResponse, err error) {
	if err != nil || resp == nil {
		h.acked = nil
		return
	}
	h.enabled = resp.GetForkContextHashRows()
	switch {
	case !h.enabled || !resp.GetAccepted():
		h.acked = nil
	case req.GetCapabilityGeneration() != 0:
	default:
		h.acked = make(map[string][]byte, len(digests))
		for i, r := range req.GetResources() {
			if digests[i] != nil {
				h.acked[r.GetSource()] = digests[i]
			}
		}
	}
}
