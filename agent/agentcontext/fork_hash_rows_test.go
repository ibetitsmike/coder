package agentcontext_test

// This file is fork code: it tests hash-only context rows
// (fork-context-hash-rows) on the agent side of the push.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/agent/agentcontext"
	agentproto "github.com/coder/coder/v2/agent/proto"
)

// hashRowsClient records every push and answers from a script. An entry
// with a nil response and a nil error answers accepted with the capability
// set to on.
type hashRowsClient struct {
	agentproto.DRPCAgentClient210
	on      bool
	reqs    []*agentproto.PushContextStateRequest
	answers []hashRowsAnswer
}

type hashRowsAnswer struct {
	resp *agentproto.PushContextStateResponse
	err  error
}

func (c *hashRowsClient) PushContextState(_ context.Context, req *agentproto.PushContextStateRequest) (*agentproto.PushContextStateResponse, error) {
	//nolint:forcetypeassert // proto.Clone returns the type it is given.
	c.reqs = append(c.reqs, proto.Clone(req).(*agentproto.PushContextStateRequest))
	if len(c.answers) > 0 {
		a := c.answers[0]
		c.answers = c.answers[1:]
		if a.resp != nil || a.err != nil {
			return a.resp, a.err
		}
	}
	return &agentproto.PushContextStateResponse{Accepted: true, ForkContextHashRows: c.on}, nil
}

func (c *hashRowsClient) last() *agentproto.PushContextStateRequest {
	return c.reqs[len(c.reqs)-1]
}

// hashRowSkills returns n skills whose bodies are large enough to elide.
func hashRowSkills(n int) []agentcontext.Resource {
	out := make([]agentcontext.Resource, 0, n)
	for i := range n {
		name := fmt.Sprintf("skill-%03d", i)
		meta := "---\nname: " + name + "\ndescription: Does task " + name + ".\n---\n" + strings.Repeat("Step. ", 20)
		out = append(out, agentcontext.Resource{
			Kind:        agentcontext.KindSkill,
			Source:      "/home/coder/.agents/skills/" + name,
			ContentHash: [32]byte{byte(i), byte(i >> 8)},
			Payload:     []byte(meta),
			SizeBytes:   uint64(len(meta)),
			Status:      agentcontext.StatusOK,
			Name:        name,
			Description: "Does task " + name + ".",
		})
	}
	return out
}

// countBodies returns how many rows of req carry a body and how many carry
// a digest in its place.
func countBodies(t *testing.T, req *agentproto.PushContextStateRequest) (bodies, digests int) {
	t.Helper()
	for _, r := range req.GetResources() {
		require.NotNil(t, r.GetBody(), "row %q has no body variant", r.GetSource())
		if len(r.GetForkBodyDigest()) > 0 {
			require.Zero(t, proto.Size(agentproto.ForkContextBody(r)), "row %q carries a body and a digest", r.GetSource())
			digests++
			continue
		}
		bodies++
	}
	return bodies, digests
}

func pushSkills(t *testing.T, p *agentcontext.DRPCPusher, version uint64, skills []agentcontext.Resource) *agentcontext.PushResponse {
	t.Helper()
	resp, err := p.PushContextState(context.Background(), &agentcontext.PushRequest{Version: version, Resources: skills})
	require.NoError(t, err)
	return resp
}

// The acceptance test of #306 item 4: with the capability advertised, a
// push that changes one skill among 200 carries one body.
func TestDRPCPusher_HashRowsSendOneChangedBodyAmong200(t *testing.T) {
	t.Parallel()
	client := &hashRowsClient{on: true}
	p := agentcontext.NewDRPCPusher(client)

	skills := hashRowSkills(200)
	pushSkills(t, p, 1, skills)
	bodies, digests := countBodies(t, client.last())
	require.Equal(t, 200, bodies, "the first push on a connection carries every body")
	require.Zero(t, digests)
	first := client.last()

	changed := append([]agentcontext.Resource(nil), skills...)
	changed[42].Payload = append([]byte("---\nname: skill-042\n---\nNew text. "), changed[42].Payload...)
	changed[42].ContentHash = [32]byte{0xff}
	pushSkills(t, p, 2, changed)
	second := client.last()
	bodies, digests = countBodies(t, second)
	require.Equal(t, 1, bodies, "only the changed skill carries a body")
	require.Equal(t, 199, digests)

	require.Len(t, second.GetResources(), 200)
	for i, r := range second.GetResources() {
		full := first.GetResources()[i]
		require.Equal(t, full.GetSource(), r.GetSource())
		require.Equal(t, full.GetSourcePath(), r.GetSourcePath())
		require.Equal(t, full.GetStatus(), r.GetStatus())
		require.Equal(t, full.GetSizeBytes(), r.GetSizeBytes())
		require.IsType(t, full.GetBody(), r.GetBody(), "an elided row keeps its kind")
		if i == 42 {
			require.Empty(t, r.GetForkBodyDigest())
			require.Equal(t, changed[42].Payload, r.GetSkill().GetMeta())
			continue
		}
		require.Equal(t, full.GetContentHash(), r.GetContentHash())
		want, err := agentproto.ForkContextBodyDigest(agentproto.ForkContextBody(full))
		require.NoError(t, err)
		require.Equal(t, want, r.GetForkBodyDigest(), "row %d digests the body the server stored", i)
	}

	// The changed skill is acknowledged now, so a third push of the same
	// set carries no body.
	pushSkills(t, p, 3, changed)
	bodies, digests = countBodies(t, client.last())
	require.Zero(t, bodies)
	require.Equal(t, 200, digests)
}

// Every case here must send the next push in full.
func TestDRPCPusher_HashRowsFallBackToFullBodies(t *testing.T) {
	t.Parallel()

	t.Run("CapabilityOff", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: false}
		p := agentcontext.NewDRPCPusher(client)
		pushSkills(t, p, 1, hashRowSkills(5))
		pushSkills(t, p, 2, hashRowSkills(5))
		bodies, _ := countBodies(t, client.last())
		require.Equal(t, 5, bodies)
	})

	t.Run("CapabilityRetracted", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: true}
		p := agentcontext.NewDRPCPusher(client)
		pushSkills(t, p, 1, hashRowSkills(5))
		client.on = false
		pushSkills(t, p, 2, hashRowSkills(5))
		bodies, _ := countBodies(t, client.last())
		require.Zero(t, bodies, "the push that retracts the capability was sent before the server answered")
		pushSkills(t, p, 3, hashRowSkills(5))
		bodies, _ = countBodies(t, client.last())
		require.Equal(t, 5, bodies)
	})

	t.Run("NotAccepted", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: true}
		p := agentcontext.NewDRPCPusher(client)
		pushSkills(t, p, 1, hashRowSkills(5))
		client.answers = []hashRowsAnswer{{resp: &agentproto.PushContextStateResponse{Accepted: false, ForkContextHashRows: true}}}
		resp := pushSkills(t, p, 1, hashRowSkills(5))
		require.False(t, resp.Accepted)
		pushSkills(t, p, 2, hashRowSkills(5))
		bodies, _ := countBodies(t, client.last())
		require.Equal(t, 5, bodies, "a rejected push leaves the stored rows unknown")
	})

	t.Run("ElidedPushFailsResendsInFull", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: true}
		p := agentcontext.NewDRPCPusher(client)
		pushSkills(t, p, 1, hashRowSkills(5))
		client.answers = []hashRowsAnswer{{err: xerrors.New("stored body does not match")}}
		resp := pushSkills(t, p, 2, hashRowSkills(5))
		require.True(t, resp.Accepted)
		require.Len(t, client.reqs, 3)
		bodies, _ := countBodies(t, client.reqs[1])
		require.Zero(t, bodies)
		bodies, _ = countBodies(t, client.reqs[2])
		require.Equal(t, 5, bodies, "the same push is sent again in full")
		// The full resend was accepted, so the next push elides again.
		pushSkills(t, p, 3, hashRowSkills(5))
		bodies, _ = countBodies(t, client.last())
		require.Zero(t, bodies)
	})

	t.Run("FullPushFailsIsNotResent", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: true}
		p := agentcontext.NewDRPCPusher(client)
		client.answers = []hashRowsAnswer{{err: xerrors.New("boom")}}
		_, err := p.PushContextState(context.Background(), &agentcontext.PushRequest{Version: 1, Resources: hashRowSkills(5)})
		require.Error(t, err)
		require.Len(t, client.reqs, 1)
	})

	t.Run("BothAttemptsFail", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: true}
		p := agentcontext.NewDRPCPusher(client)
		pushSkills(t, p, 1, hashRowSkills(5))
		client.answers = []hashRowsAnswer{{err: xerrors.New("one")}, {err: xerrors.New("two")}}
		_, err := p.PushContextState(context.Background(), &agentcontext.PushRequest{Version: 2, Resources: hashRowSkills(5)})
		require.ErrorContains(t, err, "two")
		// pushWithRetry resends after a backoff, and that push goes in full.
		pushSkills(t, p, 2, hashRowSkills(5))
		bodies, _ := countBodies(t, client.last())
		require.Equal(t, 5, bodies)
	})

	t.Run("FailedFullPushClearsAcked", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: true}
		p := agentcontext.NewDRPCPusher(client)
		skills := hashRowSkills(5)
		pushSkills(t, p, 1, skills)
		// Every row changed, so this push goes in full. It fails, but
		// coderd may still have stored it.
		changed := hashRowSkills(5)
		for i := range changed {
			changed[i].Payload = append([]byte("changed "), changed[i].Payload...)
		}
		client.answers = []hashRowsAnswer{{err: xerrors.New("lost response")}}
		_, err := p.PushContextState(context.Background(), &agentcontext.PushRequest{Version: 2, Resources: changed})
		require.Error(t, err)
		require.Len(t, client.reqs, 2)
		bodies, _ := countBodies(t, client.last())
		require.Equal(t, 5, bodies)
		// The original rows match what the agent acknowledged first, but
		// the stored rows are unknown now.
		pushSkills(t, p, 3, skills)
		bodies, _ = countBodies(t, client.last())
		require.Equal(t, 5, bodies)
	})

	t.Run("NewConnection", func(t *testing.T) {
		t.Parallel()
		client := &hashRowsClient{on: true}
		pushSkills(t, agentcontext.NewDRPCPusher(client), 1, hashRowSkills(5))
		pushSkills(t, agentcontext.NewDRPCPusher(client), 2, hashRowSkills(5))
		bodies, _ := countBodies(t, client.last())
		require.Equal(t, 5, bodies, "a reconnect starts with nothing acknowledged")
	})
}

// A capability probe stores nothing, so it is sent in full and leaves the
// acknowledged set as the last settled push left it.
func TestDRPCPusher_HashRowsProbe(t *testing.T) {
	t.Parallel()
	client := &hashRowsClient{on: true}
	p := agentcontext.NewDRPCPusher(client)
	skills := hashRowSkills(5)
	pushSkills(t, p, 1, skills)

	changed := append([]agentcontext.Resource(nil), skills...)
	changed[0].Payload = append([]byte("changed "), changed[0].Payload...)
	_, err := p.PushContextState(context.Background(), &agentcontext.PushRequest{Version: 2, Resources: changed, CapabilityGeneration: 1})
	require.NoError(t, err)
	bodies, _ := countBodies(t, client.last())
	require.Equal(t, 5, bodies, "a probe is never elided")

	pushSkills(t, p, 3, changed)
	bodies, digests := countBodies(t, client.last())
	require.Equal(t, 1, bodies, "the probe did not acknowledge its changed body")
	require.Equal(t, 4, digests)
}

// A body smaller than its digest is worth is always sent.
func TestDRPCPusher_HashRowsKeepSmallBodies(t *testing.T) {
	t.Parallel()
	client := &hashRowsClient{on: true}
	p := agentcontext.NewDRPCPusher(client)
	rows := []agentcontext.Resource{
		{Kind: agentcontext.KindMCPConfig, Source: "/home/coder/.mcp.json", Status: agentcontext.StatusOK},
		{Kind: agentcontext.KindInstructionFile, Source: "/home/coder/AGENTS.md", Payload: []byte("short"), Status: agentcontext.StatusOK},
		{Kind: agentcontext.KindInstructionFile, Source: "/home/coder/CLAUDE.md", Payload: []byte(strings.Repeat("long ", 20)), Status: agentcontext.StatusOK},
	}
	pushSkills(t, p, 1, rows)
	pushSkills(t, p, 2, rows)
	got := client.last().GetResources()
	require.Empty(t, got[0].GetForkBodyDigest())
	require.Empty(t, got[1].GetForkBodyDigest())
	require.Equal(t, []byte("short"), got[1].GetInstructionFile().GetContent())
	require.NotEmpty(t, got[2].GetForkBodyDigest())
}
