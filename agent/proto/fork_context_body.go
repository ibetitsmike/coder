package proto

// This file is fork code: the agent and coderd share these helpers for
// hash-only context rows (ContextResource.fork_body_digest), so both sides
// compute the body digest the same way.

import (
	"crypto/sha256"

	"golang.org/x/xerrors"
	"google.golang.org/protobuf/proto"
)

// ForkContextBodyDigestSize is the length of a valid fork_body_digest.
const ForkContextBodyDigestSize = sha256.Size

// ForkContextBody returns the message of r's body variant. It returns nil
// when no variant is set or the variant is unknown.
func ForkContextBody(r *ContextResource) proto.Message {
	switch b := r.GetBody().(type) {
	case *ContextResource_InstructionFile:
		return orEmpty(b.InstructionFile)
	case *ContextResource_Skill:
		return orEmpty(b.Skill)
	case *ContextResource_McpConfig:
		return orEmpty(b.McpConfig)
	case *ContextResource_McpServer:
		return orEmpty(b.McpServer)
	case *ContextResource_AcpHarness:
		return orEmpty(b.AcpHarness)
	case *ContextResource_Subagent:
		return orEmpty(b.Subagent)
	default:
		return nil
	}
}

// ForkResetContextBody replaces the message of r's body variant with a new
// empty message of the same type and returns it. It returns nil when no
// variant is set or the variant is unknown.
func ForkResetContextBody(r *ContextResource) proto.Message {
	switch r.GetBody().(type) {
	case *ContextResource_InstructionFile:
		m := &InstructionFileBody{}
		r.Body = &ContextResource_InstructionFile{InstructionFile: m}
		return m
	case *ContextResource_Skill:
		m := &SkillMetaBody{}
		r.Body = &ContextResource_Skill{Skill: m}
		return m
	case *ContextResource_McpConfig:
		m := &MCPConfigBody{}
		r.Body = &ContextResource_McpConfig{McpConfig: m}
		return m
	case *ContextResource_McpServer:
		m := &MCPServerBody{}
		r.Body = &ContextResource_McpServer{McpServer: m}
		return m
	case *ContextResource_AcpHarness:
		m := &ACPHarnessBody{}
		r.Body = &ContextResource_AcpHarness{AcpHarness: m}
		return m
	case *ContextResource_Subagent:
		m := &SubagentBody{}
		r.Body = &ContextResource_Subagent{Subagent: m}
		return m
	default:
		return nil
	}
}

// ForkContextBodyDigest returns sha256 over the deterministic proto encoding
// of body.
func ForkContextBodyDigest(body proto.Message) ([]byte, error) {
	if body == nil {
		return nil, xerrors.New("context resource has no known body variant")
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(body)
	if err != nil {
		return nil, xerrors.Errorf("marshal context body: %w", err)
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

// orEmpty returns a new empty message for a nil variant message, so a body
// set on the wire with no payload digests the same as an empty one.
func orEmpty[T any, P interface {
	*T
	proto.Message
}](m P) proto.Message {
	if m == nil {
		return P(new(T))
	}
	return m
}
