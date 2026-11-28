package chatd_test

// This file is fork code: end-to-end tests of the fork-compaction-recovery
// experiment, which retries a compaction summary stream that spent its whole
// output cap on reasoning and returned no summary text.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
	"github.com/coder/coder/v2/testutil"
)

type compactionRecoveryFixture struct {
	ctx             context.Context
	db              database.Store
	chat            database.Chat
	compactionCalls *atomic.Int32
	streamCalls     *atomic.Int32
}

// startRecoveryChat drives a chat whose first assistant response reads a file
// at 80,000 input tokens, which puts the prompt over the 70% threshold of its
// 100,000 token window and triggers automatic compaction before the second
// assistant response. The compaction handler receives the summary requests.
// The Anthropic model config carries a reasoning effort, so the summary call
// requests thinking on the wire, where the handler sees it.
func startRecoveryChat(
	t *testing.T,
	compaction func(t *testing.T, req *chattest.AnthropicRequest) chattest.AnthropicResponse,
	cfgOverrides ...func(*chatd.Config),
) compactionRecoveryFixture {
	t.Helper()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps := dbtestutil.NewDB(t)
	var compactionCalls atomic.Int32
	var streamCalls atomic.Int32
	anthropicURL := chattest.NewAnthropic(t, func(req *chattest.AnthropicRequest) chattest.AnthropicResponse {
		body := anthropicRequestBody(t, *req)
		if strings.Contains(body, "You are performing a context compaction") {
			compactionCalls.Add(1)
			return compaction(t, req)
		}
		if !req.Stream {
			return chattest.AnthropicNonStreamingResponse("title")
		}
		switch streamCalls.Add(1) {
		case 1:
			return readFileResponseWithInputTokens("/tmp/recovery.txt", 80_000)
		default:
			return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunksWithCacheUsage(chattest.AnthropicUsage{
				InputTokens:  20,
				OutputTokens: 5,
			}, "continued after compaction")...)
		}
	})
	user, org, model := seedAnthropicChatDependencies(t, db, anthropicURL)
	model = updateChatModelCompressionThreshold(t, db, model, 100_000, 70)
	model = updateChatModelCallConfig(t, db, model, codersdk.ChatModelCallConfig{
		ReasoningEffort: &codersdk.ChatModelReasoningEffortConfig{
			Default: ptr.Ref("high"),
			Max:     ptr.Ref("high"),
		},
	})
	ws, dbAgent := seedWorkspaceWithAgent(t, db, user.ID)

	ctrl := gomock.NewController(t)
	mockConn := agentconnmock.NewMockAgentConn(ctrl)
	setupToolExecutionAgentConn(t, mockConn)
	mockConn.EXPECT().ReadFileLines(gomock.Any(), "/tmp/recovery.txt", int64(1), int64(0), gomock.Any()).
		Return(workspacesdk.ReadFileLinesResponse{Success: true, FileSize: 12, TotalLines: 1, LinesRead: 1, Content: "1\tpackage main"}, nil).
		Times(1)

	server := newActiveTestServer(t, db, ps, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, anthropicURL, chattest.WithPreservePath()))
		cfg.AgentConn = func(_ context.Context, agentID uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			require.Equal(t, dbAgent.ID, agentID)
			return mockConn, func() {}, nil
		}
		for _, override := range cfgOverrides {
			override(cfg)
		}
	})
	chat, err := server.CreateChat(ctx, chatd.CreateOptions{
		OrganizationID: org.ID,
		OwnerID:        user.ID,
		WorkspaceID:    uuid.NullUUID{UUID: ws.ID, Valid: true},
		AgentID:        uuid.NullUUID{UUID: dbAgent.ID, Valid: true},
		Title:          "compaction-recovery",
		ModelConfigID:  model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{
			codersdk.ChatMessageText("read the file and continue"),
		},
	})
	require.NoError(t, err)
	return compactionRecoveryFixture{
		ctx:             ctx,
		db:              db,
		chat:            chat,
		compactionCalls: &compactionCalls,
		streamCalls:     &streamCalls,
	}
}

// anthropicTruncatedReasoningResponse streams a summary response that is one
// thinking block finished at max_tokens, with no text block: the reasoning
// spent the whole output cap.
func anthropicTruncatedReasoningResponse() chattest.AnthropicResponse {
	chunks := chattest.AnthropicReasoningTextChunks([]chattest.AnthropicReasoningBlock{
		{Text: "reasoning that consumed the entire output cap"},
	}, "")
	// AnthropicReasoningTextChunks ends with an empty text block, its deltas,
	// a message_delta at end_turn, and message_stop; reshape the tail so the
	// summary stream truncates at the cap with no text at all. The tail is
	// text block start, text delta, text block stop, message_delta,
	// message_stop.
	chunks = chunks[:len(chunks)-5]
	chunks = append(chunks, chattest.AnthropicChunk{
		Type:       "message_delta",
		StopReason: "max_tokens",
		Usage: chattest.AnthropicUsage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}, chattest.AnthropicChunk{Type: "message_stop"})
	return chattest.AnthropicStreamingResponse(chunks...)
}

// anthropicThinkingBudget asserts the thinking configuration of a summary
// request carries the budget the legacy effort mapping derives from the
// request's max_tokens: 0.8 for high, 0.2 for low.
func anthropicThinkingBudget(t *testing.T, req *chattest.AnthropicRequest, effort string) {
	t.Helper()
	factor := 0.8
	if effort == "low" {
		factor = 0.2
	}
	wantBudget := int64(float64(req.MaxTokens) * factor)
	require.Contains(t, string(req.Thinking), `"type":"enabled"`)
	require.Contains(t, string(req.Thinking), fmt.Sprintf(`"budget_tokens":%d`, wantBudget))
}

// TestForkActiveServer_CompactionRecoversFromReasoningOnlySummary is the
// acceptance case: a summary stream that spent the output cap on reasoning
// and returned no text retries once with reasoning at low effort, and the
// compaction commits the recovered summary without anyone sending a message.
func TestForkActiveServer_CompactionRecoversFromReasoningOnlySummary(t *testing.T) {
	t.Parallel()

	summaryRequests := 0
	fixture := startRecoveryChat(t,
		func(t *testing.T, req *chattest.AnthropicRequest) chattest.AnthropicResponse {
			summaryRequests++
			if summaryRequests == 1 {
				// The summary request carries the configured high effort,
				// which spends the whole output cap on reasoning.
				anthropicThinkingBudget(t, req, "high")
				return anthropicTruncatedReasoningResponse()
			}
			// The retry requests less reasoning and succeeds.
			anthropicThinkingBudget(t, req, "low")
			return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunks("recovered compaction summary")...)
		},
		func(cfg *chatd.Config) {
			cfg.Experiments = codersdk.Experiments{codersdk.ExperimentForkCompactionRecovery}
		},
	)

	waitForChatStatus(fixture.ctx, t, fixture.db, fixture.chat.ID, database.ChatStatusWaiting)

	messages := chatMessages(fixture.ctx, t, fixture.db, fixture.chat.ID)
	promptMessages, err := fixture.db.GetChatMessagesForPromptByChatID(fixture.ctx, fixture.chat.ID)
	require.NoError(t, err)
	compressed := compressedChatSummarizedMessages(t, append(promptMessages, messages...))
	require.Len(t, compressed.summaries, 1, "the recovered summary must commit")
	require.Contains(t, messageText(t, compressed.summaries[0]), "recovered compaction summary")
	requireTextPart(t, messages[len(messages)-1], "continued after compaction")
	require.Equal(t, int32(2), fixture.compactionCalls.Load(), "the truncated summary retried once")
	require.Equal(t, int32(2), fixture.streamCalls.Load(), "the turn continued without a new user message")
}

// TestForkActiveServer_CompactionTruncationFailsWithoutExperiment is the
// negative control: with no fork experiment the truncation fails the chat
// terminally after one summary request, exactly as upstream.
func TestForkActiveServer_CompactionTruncationFailsWithoutExperiment(t *testing.T) {
	t.Parallel()

	fixture := startRecoveryChat(t,
		func(*testing.T, *chattest.AnthropicRequest) chattest.AnthropicResponse {
			return anthropicTruncatedReasoningResponse()
		},
		func(cfg *chatd.Config) {
			cfg.Experiments = codersdk.Experiments{}
		},
	)

	failed := waitForChatStatus(fixture.ctx, t, fixture.db, fixture.chat.ID, database.ChatStatusError)
	payload := requireChatLastErrorPayload(t, failed.LastError)
	require.Equal(t, codersdk.ChatErrorKindGeneric, payload.Kind)
	require.Contains(t, payload.Detail, "generate compaction")
	require.Contains(t, payload.Detail, "truncated at the output token cap")
	require.Equal(t, int32(1), fixture.compactionCalls.Load(), "without the experiment there is no retry")
}

// TestForkActiveServer_CompactionTruncationFailsOnUpstreamExperiments is the
// second negative control: the upstream experiment set that every upstream
// chatd test runs with must also keep the terminal failure.
func TestForkActiveServer_CompactionTruncationFailsOnUpstreamExperiments(t *testing.T) {
	t.Parallel()

	// codersdk.ExperimentsKnown and ForkExperiments are exported, so the
	// external test package can rebuild the upstream set the same way the
	// internal upstreamExperimentsKnown helper does.
	upstream := slices.DeleteFunc(slices.Clone(codersdk.ExperimentsKnown), func(e codersdk.Experiment) bool {
		return slices.Contains(codersdk.ForkExperiments, e)
	})

	fixture := startRecoveryChat(t,
		func(*testing.T, *chattest.AnthropicRequest) chattest.AnthropicResponse {
			return anthropicTruncatedReasoningResponse()
		},
		func(cfg *chatd.Config) {
			cfg.Experiments = upstream
		},
	)

	failed := waitForChatStatus(fixture.ctx, t, fixture.db, fixture.chat.ID, database.ChatStatusError)
	payload := requireChatLastErrorPayload(t, failed.LastError)
	require.Contains(t, payload.Detail, "truncated at the output token cap")
	require.Equal(t, int32(1), fixture.compactionCalls.Load())
}
