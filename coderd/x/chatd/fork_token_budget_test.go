package chatd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
	"github.com/coder/coder/v2/testutil"
)

// forkUpstreamTestExperiments returns the experiments a test server runs
// with. fork-token-budget replaces automatic summary compaction, which the
// upstream tests check, so a list that holds an upstream experiment, as the
// upstream tests' ExperimentsKnown does, runs without it. The fork tests of
// the experiment list fork experiments only and keep it. newChatdServer
// calls this once.
func forkUpstreamTestExperiments(experiments codersdk.Experiments) codersdk.Experiments {
	upstream := slices.ContainsFunc(experiments, func(e codersdk.Experiment) bool {
		return !slices.Contains(codersdk.ForkExperiments, e)
	})
	if !upstream {
		return experiments
	}
	return slices.DeleteFunc(slices.Clone(experiments), func(e codersdk.Experiment) bool {
		return e == codersdk.ExperimentForkTokenBudget
	})
}

const (
	tokenBudgetHandoffHeader = `<context_budget request="handoff">`
	tokenBudgetFinalHeader   = `<context_budget request="final">`
	tokenBudgetSummaryPrompt = "You are performing a context compaction"
)

// anthropicRequestText returns every string of the request's messages,
// decoded, one per line, so the test can look for text that JSON escapes.
func anthropicRequestText(t *testing.T, req chattest.AnthropicRequest) string {
	t.Helper()
	var sb strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			_, _ = sb.WriteString(v)
			_, _ = sb.WriteString("\n")
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	for _, msg := range req.Messages {
		var content any
		require.NoError(t, json.Unmarshal(msg.Content, &content))
		walk(content)
	}
	return sb.String()
}

// toolCallWithTokens is a tool call whose prompt took inputTokens.
func toolCallWithTokens(name, input string, inputTokens int) chattest.AnthropicResponse {
	chunks := chattest.AnthropicToolCallChunks(name, input)
	for i := range chunks {
		if chunks[i].Type == "message_start" {
			chunks[i].Message.Usage = map[string]int{"input_tokens": inputTokens}
		}
	}
	return chattest.AnthropicStreamingResponse(chunks...)
}

func textWithTokens(text string, inputTokens int) chattest.AnthropicResponse {
	return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunksWithCacheUsage(chattest.AnthropicUsage{
		InputTokens:  inputTokens,
		OutputTokens: 5,
	}, text)...)
}

// tokenBudgetChat runs one turn of a chat whose model has a context limit
// of 1000 tokens and a compression threshold of 30%: the handoff request
// goes out at 300 tokens, the final request at 375, and the window rolls
// over at 750. step answers the stream requests in order, from 1.
func tokenBudgetChat(
	t *testing.T,
	experiments codersdk.Experiments,
	prompt string,
	step func(t *testing.T, n int32, text string) chattest.AnthropicResponse,
	overrides ...func(*testing.T, database.Store, *chatd.Config, *chatd.CreateOptions),
) (_ context.Context, _ database.Store, _ database.Chat, streamCount, summaryCount *atomic.Int32) {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps := dbtestutil.NewDB(t)
	var streams, summaries atomic.Int32
	anthropicURL := chattest.NewAnthropic(t, func(req *chattest.AnthropicRequest) chattest.AnthropicResponse {
		text := anthropicRequestText(t, *req)
		if strings.Contains(text, tokenBudgetSummaryPrompt) {
			summaries.Add(1)
			return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunks("summary of the window")...)
		}
		if !req.Stream {
			return chattest.AnthropicNonStreamingResponse("title")
		}
		return step(t, streams.Add(1), text)
	})
	user, org, model := seedAnthropicChatDependencies(t, db, anthropicURL)
	model = updateChatModelCompressionThreshold(t, db, model, 1000, 30)
	opts := chatd.CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		Title:              "token budget",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText(prompt)},
	}
	var cfgOverrides []func(*chatd.Config)
	cfgOverrides = append(cfgOverrides, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, anthropicURL, chattest.WithPreservePath()))
		cfg.Experiments = experiments
	})
	for _, override := range overrides {
		cfgOverrides = append(cfgOverrides, func(cfg *chatd.Config) { override(t, db, cfg, &opts) })
	}
	server := newActiveTestServer(t, db, ps, cfgOverrides...)
	chat, err := server.CreateChat(ctx, opts)
	require.NoError(t, err)
	chat = waitForChatStatus(ctx, t, db, chat.ID, database.ChatStatusWaiting)
	return ctx, db, chat, &streams, &summaries
}

// contextWindowAnchors returns the compressed model-only user rows that
// start a context window of the current prompt, in order.
func contextWindowAnchors(ctx context.Context, t *testing.T, db database.Store, chatID uuid.UUID) []database.ChatMessage {
	t.Helper()
	messages, err := db.GetChatMessagesForPromptByChatID(ctx, chatID)
	require.NoError(t, err)
	var anchors []database.ChatMessage
	for _, msg := range messages {
		if msg.Compressed && msg.Role == database.ChatMessageRoleUser && msg.Visibility == database.ChatMessageVisibilityModel {
			anchors = append(anchors, msg)
		}
	}
	return anchors
}

func TestTokenBudgetWindows(t *testing.T) {
	t.Parallel()

	experiments := codersdk.Experiments{codersdk.ExperimentForkTokenBudget}

	// The chat answers the handoff request: it saves a checkpoint and calls
	// new_context, and the next window starts from the notes, without a
	// summary, and can still read the first window.
	t.Run("Cooperative", func(t *testing.T) {
		t.Parallel()

		const marker = "zebra-marker-42"
		matchID := regexp.MustCompile(`"matches":\s*\[\{"id":\s*(\d+)`)
		var readID string
		ctx, db, chat, streams, summaries := tokenBudgetChat(t, experiments,
			"Remember the code word "+marker+". Then do the work.",
			func(t *testing.T, n int32, text string) chattest.AnthropicResponse {
				switch n {
				case 1:
					return toolCallWithTokens("notes", `{"action":"list"}`, 100)
				case 2:
					require.NotContains(t, text, tokenBudgetHandoffHeader)
					return toolCallWithTokens("notes", `{"action":"list"}`, 320)
				case 3:
					require.Equal(t, 1, strings.Count(text, tokenBudgetHandoffHeader))
					return toolCallWithTokens("notes", `{"action":"write","path":"checkpoint.md","content":"goal: finish the work"}`, 330)
				case 4:
					require.Equal(t, 1, strings.Count(text, tokenBudgetHandoffHeader), "the handoff request goes out once")
					require.NotContains(t, text, tokenBudgetFinalHeader)
					return toolCallWithTokens("new_context", `{}`, 340)
				case 5:
					require.Contains(t, text, `<context_window id="w:2">`)
					require.Contains(t, text, "- checkpoint.md (")
					require.Contains(t, text, "Read checkpoint.md")
					require.NotContains(t, text, marker, "the earlier window left the context")
					require.NotContains(t, text, tokenBudgetHandoffHeader)
					require.NotContains(t, text, "The following is a summary")
					return toolCallWithTokens("chat_history", fmt.Sprintf(`{"action":"search","query":%q}`, marker), 60)
				case 6:
					m := matchID.FindStringSubmatch(text)
					require.NotNil(t, m, "search must find the first window's message: %s", text)
					readID = m[1]
					return toolCallWithTokens("chat_history", fmt.Sprintf(`{"action":"read_item","id":%s}`, readID), 70)
				case 7:
					require.Contains(t, text, "Remember the code word "+marker)
					return textWithTokens("done", 80)
				default:
					t.Errorf("unexpected stream request %d", n)
					return textWithTokens("extra", 80)
				}
			})
		require.Equal(t, int32(7), streams.Load())
		require.Zero(t, summaries.Load(), "a rollover makes no summary call")

		messages := chatMessages(ctx, t, db, chat.ID)
		anchors := contextWindowAnchors(ctx, t, db, chat.ID)
		require.Len(t, anchors, 1)
		require.True(t, strings.HasPrefix(messageText(t, anchors[0]), `<context_window id="w:2">`))
		compressed := compressedChatSummarizedMessages(t, messages)
		require.Len(t, compressed.results, 1)
		result := singlePartOfType(t, compressed.results[0], codersdk.ChatMessagePartTypeToolResult)
		require.Contains(t, string(result.Result), "Context window w:2 started from the notes of this chat")
		requireContextWindow(t, messages, &chatloop.ContextWindowBoundary{ID: "w:2", Trigger: chatloop.ContextWindowTriggerNewContext})
		requireTextPart(t, messages[len(messages)-1], "done")
		require.NotEmpty(t, readID)
		require.Less(t, mustParseID(t, readID), anchors[0].ID)
	})

	// The chat ignores both requests, and the window rolls over at the
	// usable limit after the last call got its result.
	t.Run("ForcedRollover", func(t *testing.T) {
		t.Parallel()

		ctx, db, chat, streams, summaries := tokenBudgetChat(t, experiments, "do the work",
			func(t *testing.T, n int32, text string) chattest.AnthropicResponse {
				switch n {
				case 1:
					return toolCallWithTokens("notes", `{"action":"list"}`, 100)
				case 2:
					return toolCallWithTokens("notes", `{"action":"list"}`, 320)
				case 3:
					require.Equal(t, 1, strings.Count(text, tokenBudgetHandoffHeader))
					require.NotContains(t, text, tokenBudgetFinalHeader)
					return toolCallWithTokens("notes", `{"action":"list"}`, 400)
				case 4:
					require.Equal(t, 1, strings.Count(text, tokenBudgetHandoffHeader))
					require.Equal(t, 1, strings.Count(text, tokenBudgetFinalHeader))
					return toolCallWithTokens("notes", `{"action":"list"}`, 760)
				case 5:
					require.Contains(t, text, `<context_window id="w:2">`)
					require.Contains(t, text, "There is no checkpoint.md")
					require.NotContains(t, text, tokenBudgetHandoffHeader)
					require.NotContains(t, text, tokenBudgetFinalHeader)
					return textWithTokens("after rollover", 50)
				default:
					t.Errorf("unexpected stream request %d", n)
					return textWithTokens("extra", 50)
				}
			})
		require.Equal(t, int32(5), streams.Load())
		require.Zero(t, summaries.Load())

		messages := chatMessages(ctx, t, db, chat.ID)
		anchors := contextWindowAnchors(ctx, t, db, chat.ID)
		require.Len(t, anchors, 1)
		// The call that reached the limit and its result both stay in the
		// first window.
		var lastCall, lastResult int64
		for _, msg := range messages {
			if msg.Compressed {
				continue
			}
			if msg.Role == database.ChatMessageRoleAssistant && msg.InputTokens.Int64 == 760 {
				lastCall = msg.ID
			}
			if msg.Role == database.ChatMessageRoleTool && lastCall != 0 && lastResult == 0 {
				lastResult = msg.ID
			}
		}
		require.NotZero(t, lastCall)
		require.NotZero(t, lastResult)
		require.Less(t, lastCall, lastResult)
		require.Less(t, lastResult, anchors[0].ID)
		requireTextPart(t, messages[len(messages)-1], "after rollover")
		requireContextWindow(t, messages, &chatloop.ContextWindowBoundary{ID: "w:2", Trigger: chatloop.ContextWindowTriggerContextLimit})
	})

	// A plan mode chat has no notes, so it keeps summary compaction and
	// gets no requests.
	t.Run("PlanModeSummarizes", func(t *testing.T) {
		t.Parallel()

		_, _, _, streams, summaries := tokenBudgetChat(t, experiments, "plan the work",
			func(t *testing.T, n int32, text string) chattest.AnthropicResponse {
				require.NotContains(t, text, "<context_budget")
				switch n {
				case 1:
					return toolCallWithTokens("read_skill", `{"name":"missing"}`, 800)
				default:
					require.Contains(t, text, "summary of the window")
					return textWithTokens("planned", 50)
				}
			},
			func(_ *testing.T, _ database.Store, _ *chatd.Config, opts *chatd.CreateOptions) {
				opts.PlanMode = database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}
			})
		require.Equal(t, int32(2), streams.Load())
		require.Equal(t, int32(1), summaries.Load())
	})

	// With the experiment off, compaction summarizes as upstream does.
	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx, db, chat, streams, summaries := tokenBudgetChat(t, codersdk.Experiments{}, "read the file",
			func(t *testing.T, n int32, text string) chattest.AnthropicResponse {
				require.NotContains(t, text, "<context_budget")
				require.NotContains(t, text, "<context_window")
				switch n {
				case 1:
					return toolCallWithTokens("read_file", `{"path":"/tmp/a.txt"}`, 800)
				default:
					require.Contains(t, text, "summary of the window")
					return textWithTokens("read", 50)
				}
			},
			func(t *testing.T, db database.Store, cfg *chatd.Config, opts *chatd.CreateOptions) {
				ws, dbAgent := seedWorkspaceWithAgent(t, db, opts.OwnerID)
				ctrl := gomock.NewController(t)
				mockConn := agentconnmock.NewMockAgentConn(ctrl)
				setupToolExecutionAgentConn(t, mockConn)
				mockConn.EXPECT().ReadFileLines(gomock.Any(), "/tmp/a.txt", int64(1), int64(0), gomock.Any()).
					Return(workspacesdk.ReadFileLinesResponse{Success: true, FileSize: 12, TotalLines: 1, LinesRead: 1, Content: "1\tpackage main"}, nil).
					AnyTimes()
				cfg.AgentConn = func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
					return mockConn, func() {}, nil
				}
				opts.WorkspaceID = uuid.NullUUID{UUID: ws.ID, Valid: true}
				opts.AgentID = uuid.NullUUID{UUID: dbAgent.ID, Valid: true}
			})
		require.Equal(t, int32(2), streams.Load())
		require.Equal(t, int32(1), summaries.Load())
		requireContextWindow(t, chatMessages(ctx, t, db, chat.ID), nil)
	})
}

// requireContextWindow checks the context_window of the one compressed
// chat_summarized result in messages. A nil want means a summary, which has
// none.
func requireContextWindow(t *testing.T, messages []database.ChatMessage, want *chatloop.ContextWindowBoundary) {
	t.Helper()
	compressed := compressedChatSummarizedMessages(t, messages)
	require.Len(t, compressed.results, 1)
	part := singlePartOfType(t, compressed.results[0], codersdk.ChatMessagePartTypeToolResult)
	var got struct {
		Summary       string                          `json:"summary"`
		ContextWindow *chatloop.ContextWindowBoundary `json:"context_window"`
	}
	require.NoError(t, json.Unmarshal(part.Result, &got))
	require.NotEmpty(t, got.Summary)
	require.Equal(t, want, got.ContextWindow)
}

func mustParseID(t *testing.T, s string) int64 {
	t.Helper()
	var id int64
	_, err := fmt.Sscan(s, &id)
	require.NoError(t, err)
	return id
}
