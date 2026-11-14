package chatd_test

// This file is fork code: it pins the prompt-history loads of one generation
// step. Issue #298: a step with fork context loads
// GetChatMessagesForPromptByChatID once, in syncForkContext, and those rows
// feed the todo snapshot check, the pinned skills check, the token budget
// request check and the turn environment. A fork context insert restarts the
// step, and the restart loads fresh rows, so a prompt never builds from rows
// that miss a message the same step inserted.

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// promptLoadCountingStore counts the prompt-history loads that pass through it
// and delegates everything else to the store it wraps. It implements
// forkdb.Wrapper so forkdb.From keeps the fork queries behind it instead of
// failing on the unknown wrapper.
type promptLoadCountingStore struct {
	database.Store

	loads atomic.Int64
}

func (s *promptLoadCountingStore) GetChatMessagesForPromptByChatID(ctx context.Context, chatID uuid.UUID) ([]database.ChatMessage, error) {
	s.loads.Add(1)
	return s.Store.GetChatMessagesForPromptByChatID(ctx, chatID)
}

func (s *promptLoadCountingStore) ForkUnwrap() database.Store {
	return s.Store
}

func (*promptLoadCountingStore) ForkWrap(inner forkdb.Store) forkdb.Store {
	return inner
}

// promptLoadExperiments are the fork experiments whose checks read prompt rows.
var promptLoadExperiments = codersdk.Experiments{
	codersdk.ExperimentForkChatTodos,
	codersdk.ExperimentForkPinnedSkills,
}

// The preset argument of promptLoadTurn, as named values so a call site reads
// as what the chat gets.
const (
	promptLoadPreset = true
	promptLoadSeed   = false
)

// promptLoadTurn creates one chat with a pinned personal skill named deploy and
// a stored todo list, and, when withPresetRows is true, inserts the snapshot and
// pins rows the current list versions produce into its prompt before the turn, so
// the turn's step has nothing fork context is due to insert. The chat is
// created idle and the turn starts with SendMessage, so the worker cannot see
// the chat until every seed row is in. The model answers each streaming
// request with new text starting at one, and non-streaming requests with a
// title. It returns the context, the counting store, the finished chat and
// every recorded streaming request.
//
//nolint:revive // withPresetRows is a test fixture control flag, not API state.
func promptLoadTurn(
	t *testing.T,
	experiments codersdk.Experiments,
	withPresetRows bool,
	answers func(t *testing.T, n int32, text string) chattest.AnthropicResponse,
) promptLoadTurnResult {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps := dbtestutil.NewDB(t)
	counting := &promptLoadCountingStore{Store: db}

	recorder := newAnthropicRequestRecorder()
	var streamCount atomic.Int32
	anthropicURL := chattest.NewAnthropic(t, func(req *chattest.AnthropicRequest) chattest.AnthropicResponse {
		recorder.record(req)
		if !req.Stream {
			return chattest.AnthropicNonStreamingResponse("title")
		}
		return answers(t, streamCount.Add(1), anthropicRequestText(t, *req))
	})
	user, org, model := seedAnthropicChatDependencies(t, db, anthropicURL)
	const skillBody = "Run make deploy before you push."
	insertUserSkillByName(ctx, t, db, user.ID, "deploy", skillBody)

	server := newActiveTestServer(t, counting, ps, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, anthropicURL, chattest.WithPreservePath()))
		cfg.Experiments = experiments
	})
	// The chat is created without initial content, so it stays waiting and no
	// worker picks it up: the turn starts only at the SendMessage below, after
	// every seed row is in.
	chat, err := server.CreateChat(ctx, chatd.CreateOptions{
		OrganizationID: org.ID,
		OwnerID:        user.ID,
		Title:          "prompt rows once",
		ModelConfigID:  model.ID,
	})
	require.NoError(t, err)
	pinChatSkills(ctx, t, db, chat.ID)
	require.NoError(t, server.WriteChatTodosForTest(ctx, chat.ID, []codersdk.ChatTodo{
		{ID: "plan", Content: "Plan the change", Status: codersdk.ChatTodoStatusInProgress},
	}))
	if withPresetRows {
		presetForkContextRows(ctx, t, db, server, chat, model.ID, user.ID)
	}

	_, err = server.SendMessage(ctx, chatd.SendMessageOptions{
		ChatID: chat.ID,
		Content: []codersdk.ChatMessagePart{
			codersdk.ChatMessageText("hello"),
		},
		BusyBehavior: chatd.SendMessageBusyBehaviorQueue,
	})
	require.NoError(t, err)

	chat = waitForTerminalChat(ctx, t, db, chat.ID)
	require.Equal(t, database.ChatStatusWaiting, chat.Status)
	require.NoError(t, chatd.WaitUntilIdleForTest(ctx, server))

	streams := filterAnthropicStreamingRequests(recorder.all())
	require.NotEmpty(t, streams)
	return promptLoadTurnResult{ctx, db, counting, chat, streams}
}

// promptLoadTurnResult holds what promptLoadTurn built, so call sites name
// only the fields they use.
type promptLoadTurnResult struct {
	Ctx      context.Context
	DB       database.Store
	Counting *promptLoadCountingStore
	Chat     database.Chat
	Streams  []chattest.AnthropicRequest
}

// pinChatSkills replaces the pins of a chat with the personal deploy skill,
// the way the pinned skills route does.
func pinChatSkills(ctx context.Context, t *testing.T, db database.Store, chatID uuid.UUID) {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	params := forkdb.InsertChatPinnedSkillsParams{ChatID: chatID}
	params.SkillSources = append(params.SkillSources, string(codersdk.ChatSkillSourcePersonal))
	params.SkillNames = append(params.SkillNames, "deploy")
	require.NoError(t, fdb.InsertChatPinnedSkills(ctx, params))
}

// presetForkContextRows inserts the todo snapshot and the pinned skills
// messages the chat's current list versions produce, as model-only rows, so
// the next step finds nothing fork context is due to insert.
func presetForkContextRows(
	ctx context.Context,
	t *testing.T,
	db database.Store,
	server *chatd.Server,
	chat database.Chat,
	modelID uuid.UUID,
	createdBy uuid.UUID,
) {
	t.Helper()
	loaded, err := db.GetChatMessagesForPromptByChatID(ctx, chat.ID)
	require.NoError(t, err)
	snapshot, snapshotDue, err := server.TodoSnapshotMessageForTest(ctx, chat, loaded)
	require.NoError(t, err)
	if snapshotDue {
		loaded = append(loaded, promptLoadRowForTest(t, snapshot))
	}
	pins, pinsDue, err := server.PinnedSkillsMessageForTest(ctx, chat, loaded)
	require.NoError(t, err)
	require.True(t, snapshotDue, "a fresh todo list must be due")
	require.True(t, pinsDue, "fresh pins must be due")
	insertModelOnlyRows(ctx, t, db, chat.ID, modelID, createdBy, snapshot, pins)
}

// promptLoadRowForTest returns one fork context message as the chat message
// row inserting it produces, so the next check sees the row in promptRows.
func promptLoadRowForTest(t *testing.T, message chatstate.Message) database.ChatMessage {
	t.Helper()
	return database.ChatMessage{
		Role:           message.Role,
		Visibility:     message.Visibility,
		Content:        message.Content,
		ContentVersion: message.ContentVersion,
		ModelConfigID:  message.ModelConfigID,
	}
}

// insertModelOnlyRows appends fork context messages as model-only user rows,
// the way commitForkContextMessage stores them.
func insertModelOnlyRows(
	ctx context.Context,
	t *testing.T,
	db database.Store,
	chatID uuid.UUID,
	modelID uuid.UUID,
	createdBy uuid.UUID,
	messages ...chatstate.Message,
) {
	t.Helper()
	params := database.InsertChatMessagesParams{ChatID: chatID}
	for _, message := range messages {
		params.CreatedBy = append(params.CreatedBy, createdBy)
		params.ModelConfigID = append(params.ModelConfigID, modelID)
		params.ReasoningEffort = append(params.ReasoningEffort, "")
		params.Role = append(params.Role, message.Role)
		params.Content = append(params.Content, string(message.Content.RawMessage))
		params.ContentVersion = append(params.ContentVersion, message.ContentVersion)
		params.Visibility = append(params.Visibility, message.Visibility)
		params.InputTokens = append(params.InputTokens, 0)
		params.OutputTokens = append(params.OutputTokens, 0)
		params.TotalTokens = append(params.TotalTokens, 0)
		params.ReasoningTokens = append(params.ReasoningTokens, 0)
		params.CacheCreationTokens = append(params.CacheCreationTokens, 0)
		params.CacheReadTokens = append(params.CacheReadTokens, 0)
		params.ContextLimit = append(params.ContextLimit, 0)
		params.Compressed = append(params.Compressed, false)
		params.RuntimeMs = append(params.RuntimeMs, 0)
	}
	_, err := db.InsertChatMessages(ctx, params)
	require.NoError(t, err)
}

// insertUserSkillByName stores one personal skill the user can invoke.
func insertUserSkillByName(ctx context.Context, t *testing.T, db database.Store, userID uuid.UUID, name, body string) {
	t.Helper()
	_, err := db.InsertUserSkill(ctx, database.InsertUserSkillParams{
		ID:          uuid.New(),
		UserID:      userID,
		Name:        name,
		Description: name,
		Content:     "---\nname: " + name + "\ndescription: " + name + "\n---\n\n" + body + "\n",
	})
	require.NoError(t, err)
}

// TestGenerationStepLoadsPromptRowsOnce drives a full turn of a chat whose
// prompt already shows the current todo snapshot and pinned skills, so fork
// context has nothing to insert and the turn is one model call. Each
// generation pass of the turn must load the prompt history exactly once, in
// syncForkContext, and hand the rows to the todo check, the pins check and
// buildTurnEnvironment. Before the change, the todo check, the pins check and
// the turn environment each loaded their own on every pass, three per pass
// beyond the shared load.
func TestGenerationStepLoadsPromptRowsOnce(t *testing.T) {
	t.Parallel()

	turn := promptLoadTurn(t, promptLoadExperiments, promptLoadPreset,
		func(t *testing.T, n int32, text string) chattest.AnthropicResponse {
			require.Equal(t, int32(1), n, "unexpected extra model step")
			return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunks("I will deploy.")...)
		})
	// A one-call turn runs two generation passes: the pass that generates the
	// assistant reply, and the pass that decides the history is complete and
	// finishes the turn. Each pass loads the prompt history once, in
	// syncForkContext. Around them sit three upstream loads: the memory index
	// pre-read of the first pass, the end-of-turn status label, and the
	// background chat summary. Five in total, all named here.
	require.Equal(t, int64(5), turn.Counting.loads.Load(), "got %d loads", turn.Counting.loads.Load())
	// The step's model request holds the pre-inserted fork context rows.
	text := anthropicRequestText(t, turn.Streams[0])
	require.Contains(t, text, todoSnapshotHeaderForTest)
	require.Contains(t, text, pinnedSkillsHeaderForTest)
}

// TestGenerationStepExperimentsOffKeepsUpstreamLoads runs the same chat with
// every fork experiment off and nothing pre-inserted. Upstream loads the
// prompt history in buildTurnEnvironment, once for the step, and the same
// out-of-step loads sit around it. No fork context message was ever inserted
// into the prompt.
func TestGenerationStepExperimentsOffKeepsUpstreamLoads(t *testing.T) {
	t.Parallel()

	upstream := slices.DeleteFunc(slices.Clone(codersdk.ExperimentsKnown), func(e codersdk.Experiment) bool {
		return slices.Contains(codersdk.ForkExperiments, e)
	})
	turn := promptLoadTurn(t, upstream, promptLoadSeed,
		func(t *testing.T, n int32, text string) chattest.AnthropicResponse {
			require.Equal(t, int32(1), n, "unexpected extra model step")
			return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunks("no fork context here")...)
		})
	// Upstream loads in buildTurnEnvironment instead of syncForkContext, so
	// the count is the same five: the memory index pre-read, the two
	// turn-environment loads of the two passes, the end-of-turn status label
	// and the background chat summary.
	require.Equal(t, int64(5), turn.Counting.loads.Load(), "got %d loads", turn.Counting.loads.Load())

	// The pins and todos were seeded but no fork check ran, so the model
	// request shows none of them.
	text := anthropicRequestText(t, turn.Streams[0])
	require.NotContains(t, text, todoSnapshotHeaderForTest)
	require.NotContains(t, text, pinnedSkillsHeaderForTest)
}

// TestGenerationStepNoPinsNoTodosLoadsOnce drives a chat with no pins and no
// todo list under the fork experiments. Fork context has nothing to insert on
// any step, so the turn is one step that loads once and feeds the turn
// environment from the shared rows.
func TestGenerationStepNoPinsNoTodosLoadsOnce(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps := dbtestutil.NewDB(t)
	counting := &promptLoadCountingStore{Store: db}

	recorder := newAnthropicRequestRecorder()
	var streamCount atomic.Int32
	anthropicURL := chattest.NewAnthropic(t, func(req *chattest.AnthropicRequest) chattest.AnthropicResponse {
		recorder.record(req)
		if !req.Stream {
			return chattest.AnthropicNonStreamingResponse("title")
		}
		require.Equal(t, int32(1), streamCount.Add(1), "unexpected extra model step")
		return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunks("nothing due here")...)
	})
	user, org, model := seedAnthropicChatDependencies(t, db, anthropicURL)

	server := newActiveTestServer(t, counting, ps, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, anthropicURL, chattest.WithPreservePath()))
		cfg.Experiments = promptLoadExperiments
	})
	chat, err := server.CreateChat(ctx, chatd.CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		Title:              "no pins no todos",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
	})
	require.NoError(t, err)

	finished := waitForTerminalChat(ctx, t, db, chat.ID)
	require.Equal(t, database.ChatStatusWaiting, finished.Status)
	require.NoError(t, chatd.WaitUntilIdleForTest(ctx, server))

	// With no pins and no todo list, fork context is due to insert nothing,
	// so each pass loads once in syncForkContext and hands the rows to the
	// turn environment. The five loads are the memory index pre-read, the two
	// step loads, the end-of-turn status label and the background summary.
	require.Equal(t, int64(5), counting.loads.Load(), "got %d loads", counting.loads.Load())
	streams := filterAnthropicStreamingRequests(recorder.all())
	require.Len(t, streams, 1)
}

// TestForkContextInsertRestartsStepWithRows drives a chat whose pins and todo
// list are seeded but not yet shown in the prompt. The first pass of the step
// finds both due, commits them and restarts, and the restart must build the
// model request from freshly loaded rows, so the request carries the messages
// the same step inserted. That is the reload-on-insert contract: the shared
// rows are used only by the pass that loaded them, which committed nothing.
func TestForkContextInsertRestartsStepWithRows(t *testing.T) {
	t.Parallel()

	const skillBody = "Run make deploy before you push."
	turn := promptLoadTurn(t, promptLoadExperiments, promptLoadSeed,
		func(t *testing.T, n int32, text string) chattest.AnthropicResponse {
			require.Equal(t, int32(1), n, "unexpected extra model step")
			return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunks("deploying now")...)
		})

	// Both fork context messages landed as model-only user rows.
	rows, err := turn.DB.GetChatMessagesForPromptByChatID(turn.Ctx, turn.Chat.ID)
	require.NoError(t, err)
	var sawSnapshot, sawPins bool
	for _, row := range rows {
		if row.Role != database.ChatMessageRoleUser || row.Visibility != database.ChatMessageVisibilityModel {
			continue
		}
		parts, err := chatprompt.ParseContent(row)
		require.NoError(t, err)
		for _, part := range parts {
			if part.Type != codersdk.ChatMessagePartTypeText {
				continue
			}
			if strings.HasPrefix(part.Text, todoSnapshotHeaderForTest) {
				sawSnapshot = true
			}
			if strings.HasPrefix(part.Text, pinnedSkillsHeaderForTest) {
				sawPins = true
			}
		}
	}
	require.True(t, sawSnapshot, "todo snapshot row missing from the prompt rows")
	require.True(t, sawPins, "pinned skills row missing from the prompt rows")

	// The model request, built after the restart reloaded, carries them and
	// the pinned skill body.
	text := anthropicRequestText(t, turn.Streams[0])
	require.Contains(t, text, todoSnapshotHeaderForTest)
	require.Contains(t, text, pinnedSkillsHeaderForTest)
	require.Contains(t, text, skillBody)
}

const (
	// The headers the chatd package holds, repeated here because the test
	// package cannot reach the unexported constants.
	todoSnapshotHeaderForTest = "Your todo list for this chat, read from the store. It may differ from what this conversation shows, so treat this as current."
	pinnedSkillsHeaderForTest = "The user pinned these skills to this chat. Follow their instructions on every turn until they are unpinned. This list replaces any earlier list of pinned skills."
)
