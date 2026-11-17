package proto

import "context"

// DRPCForkChatProcessEventsClient is the fork RPC of the Agent API that
// reports the exits and the matching output of the background processes of
// chats. It is not part of a numbered Agent API version, so syncs with
// upstream keep their version numbers: a coderd without it fails the call
// with an unknown RPC error, and the agent backs off and retries.
type DRPCForkChatProcessEventsClient interface {
	ReportChatProcessEvents(ctx context.Context, in *ReportChatProcessEventsRequest) (*ReportChatProcessEventsResponse, error)
}
