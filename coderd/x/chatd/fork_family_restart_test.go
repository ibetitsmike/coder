package chatd_test

// This file is fork code: it tests that a busy chat family of the
// fork-nested-subagents experiment survives a chatd restart.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	dbpubsub "github.com/coder/coder/v2/coderd/database/pubsub"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

const (
	familyRestartCoordinator = "COORDINATE-RESTART"
	familyRestartWake        = "WAKE-ROOT"
)

var (
	familyRestartWorkerPattern = regexp.MustCompile(`WORKER-\d`)
	familyRestartResultPattern = regexp.MustCompile(`RESULT-WORKER-\d`)
)

// TestChatFamilySurvivesRestart stops chatd under a busy family and starts a
// new server on the same database. The root waits for its coordinator with
// wait_for_events, the coordinator is blocked in wait_agent for three
// workers, two workers are mid model call and the third waits for capacity,
// and each worker's family message is queued on the busy coordinator. Every
// chat must finish, and no result, message or child may be lost or doubled.
func TestChatFamilySurvivesRestart(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, 2*testutil.WaitSuperLong)

	var (
		restarted atomic.Bool
		rootID    atomic.Pointer[uuid.UUID]
		// blocked receives the name of each worker whose model call waits
		// for the restart.
		blocked   = make(chan string, 8)
		startedMu sync.Mutex
		started   = map[string]bool{}
		// waitsCommitted holds each worker's family message until the
		// coordinator committed its wait_agent batch, so the messages stay
		// queued instead of joining the coordinator's next model call.
		waitsCommitted = make(chan struct{})
	)
	coordinatorID := func(ctx context.Context) (uuid.UUID, error) {
		id := rootID.Load()
		if id == nil {
			return uuid.Nil, xerrors.New("root not created yet")
		}
		rows, err := db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{*id}})
		if err != nil || len(rows) != 1 {
			return uuid.Nil, xerrors.Errorf("coordinator not found: %v", err)
		}
		return rows[0].Chat.ID, nil
	}

	openAIURL := chattest.NewOpenAI(t, func(req *chattest.OpenAIRequest) chattest.OpenAIResponse {
		if !req.Stream {
			return chattest.OpenAINonStreamingResponse("title")
		}
		var userText string
		var users, toolResults []string
		for _, message := range req.Messages {
			switch message.Role {
			case "user":
				if userText == "" {
					userText = message.Content
				}
				users = append(users, message.Content)
			case "tool":
				toolResults = append(toolResults, message.Content)
			}
		}
		text := func(s string) chattest.OpenAIResponse {
			return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks(s)...)
		}
		switch {
		case familyRestartWorkerPattern.MatchString(userText):
			name := familyRestartWorkerPattern.FindString(userText)
			if len(toolResults) == 0 {
				startedMu.Lock()
				started[name] = true
				startedMu.Unlock()
				select {
				case <-waitsCommitted:
				case <-req.Context().Done():
					return chattest.OpenAIStreamingResponse()
				}
				parent, err := coordinatorID(req.Context())
				if err != nil {
					return text(err.Error())
				}
				return chattest.OpenAIStreamingResponse(familyRestartToolCalls(
					[2]string{"message_family_chat", fmt.Sprintf(`{"chat_id":%q,"message":%q}`, parent, "HELLO-"+name)},
				))
			}
			if !restarted.Load() {
				// The old server cancels this call when it stops.
				blocked <- name
				<-req.Context().Done()
				return chattest.OpenAIStreamingResponse()
			}
			return text("RESULT-" + name)
		case strings.Contains(userText, familyRestartCoordinator):
			switch {
			case len(toolResults) == 0:
				var calls [][2]string
				for i := 1; i <= 3; i++ {
					calls = append(calls, [2]string{"spawn_agent", fmt.Sprintf(`{"type":"general","prompt":"WORKER-%d","title":"worker-%d"}`, i, i)})
				}
				return chattest.OpenAIStreamingResponse(familyRestartToolCalls(calls...))
			case len(toolResults) == 3:
				var calls [][2]string
				ids := make([]string, 0, 3)
				for _, result := range toolResults {
					var spawned struct {
						ChatID string `json:"chat_id"`
					}
					if err := json.Unmarshal([]byte(result), &spawned); err != nil || spawned.ChatID == "" {
						return text("spawn failed: " + result)
					}
					ids = append(ids, spawned.ChatID)
				}
				for _, id := range ids {
					calls = append(calls, [2]string{"wait_agent", fmt.Sprintf(`{"chat_id":%q}`, id)})
				}
				return chattest.OpenAIStreamingResponse(familyRestartToolCalls(calls...))
			default:
				var seen []string
				for _, result := range toolResults[3:] {
					seen = append(seen, familyRestartResultPattern.FindAllString(result, -1)...)
				}
				slices.Sort(seen)
				return text("coordinator saw " + strings.Join(slices.Compact(seen), " "))
			}
		default:
			woken := slices.ContainsFunc(users, func(s string) bool { return strings.Contains(s, familyRestartWake) })
			switch {
			case len(toolResults) == 0:
				return chattest.OpenAIStreamingResponse(familyRestartToolCalls(
					[2]string{"spawn_agent", fmt.Sprintf(`{"type":"general","prompt":%q,"title":"coordinator"}`, familyRestartCoordinator)},
				))
			case len(toolResults) == 1:
				var spawned struct {
					ChatID string `json:"chat_id"`
				}
				if err := json.Unmarshal([]byte(toolResults[0]), &spawned); err != nil || spawned.ChatID == "" {
					return text("spawn failed: " + toolResults[0])
				}
				return chattest.OpenAIStreamingResponse(familyRestartToolCalls(
					[2]string{"wait_for_events", fmt.Sprintf(`{"child_chat_ids":[%q],"prompt":%q}`, spawned.ChatID, familyRestartWake)},
				))
			case len(toolResults) == 2 && !woken:
				return text("root waiting")
			case len(toolResults) == 2:
				parent, err := coordinatorID(req.Context())
				if err != nil {
					return text(err.Error())
				}
				return chattest.OpenAIStreamingResponse(familyRestartToolCalls(
					[2]string{"wait_agent", fmt.Sprintf(`{"chat_id":%q}`, parent)},
				))
			default:
				return text("root done")
			}
		}
	})
	user, org, model := seedChatDependenciesWithProvider(t, db, "openai-compat", openAIURL)
	configure := func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, openAIURL))
		cfg.Experiments = codersdk.Experiments{
			codersdk.ExperimentForkNestedSubagents,
			codersdk.ExperimentForkFamilyMessages,
			codersdk.ExperimentForkChatWakeups,
		}
		cfg.Limits.Fork.MaxConcurrentSubagentsPerFamily = 2
	}

	// The first server is closed by the test, like newActiveTestServer's
	// cleanup would, so it is started by hand.
	cfg := chatd.Config{
		Logger:                     slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		Database:                   db,
		ReplicaID:                  uuid.New(),
		PendingChatAcquireInterval: 10 * time.Millisecond,
		InFlightChatStaleAfter:     testutil.WaitSuperLong,
		MCPHTTPClient:              testMCPHTTPClient(),
	}
	configure(&cfg)
	subscriptions := &familyRestartPubsub{Pubsub: ps, active: map[string]int{}}
	first := newChatdServer(t, subscriptions, cfg)
	var firstClosed bool
	t.Cleanup(func() {
		if !firstClosed {
			require.NoError(t, first.Close())
		}
	})

	root, err := first.CreateChat(ctx, chatd.CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		Title:              "restart-family",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("run the family")},
	})
	require.NoError(t, err)
	// The model mock reads rootID, so the worker starts only after it is set.
	rootID.Store(&root.ID)
	first.Start()

	// Stop the server only once every part of the family is in its state.
	waitForChatStatus(ctx, t, db, root.ID, database.ChatStatusWaiting)
	var coordinator database.Chat
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		rows, err := db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{root.ID}})
		if err != nil || len(rows) != 1 {
			return false
		}
		coordinator = rows[0].Chat
		return true
	}, testutil.IntervalFast)
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		calls := familyRestartParts(t, chatMessages(ctx, t, db, coordinator.ID), codersdk.ChatMessagePartTypeToolCall, "wait_agent")
		rows, err := db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{coordinator.ID}})
		return err == nil && len(calls) == 3 && len(rows) == 3
	}, testutil.IntervalFast)
	close(waitsCommitted)
	var blockedWorkers []string
	for len(blockedWorkers) < 2 {
		select {
		case name := <-blocked:
			blockedWorkers = append(blockedWorkers, name)
		case <-ctx.Done():
			t.Fatalf("workers did not block: %v", ctx.Err())
		}
	}
	require.Zero(t, familyRestartCount(t, chatMessages(ctx, t, db, coordinator.ID), "HELLO-"), "family messages must stay queued")
	queued, err := db.GetChatQueuedMessages(ctx, coordinator.ID)
	require.NoError(t, err)
	require.Len(t, queued, 2, "each running worker's family message is queued")
	for _, worker := range familyRestartChild(ctx, t, db, coordinator.ID, 3) {
		name := familyRestartWorkerPattern.FindString(familyRestartPrompt(ctx, t, db, worker.ID))
		if name == "" || slices.Contains(blockedWorkers, name) {
			continue
		}
		startedMu.Lock()
		workerStarted := started[name]
		startedMu.Unlock()
		require.False(t, workerStarted, "%s must wait for capacity", name)
		require.Equal(t, database.ChatStatusRunning, worker.Status, "%s must wait for capacity", name)
		require.False(t, worker.WorkerID.Valid, "%s must wait for capacity", name)
	}
	// Each wait_agent call subscribes to the state of its worker while it
	// waits. A running worker's own runner holds one more subscription.
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		for _, worker := range familyRestartChild(ctx, t, db, coordinator.ID, 3) {
			want := 1
			if worker.WorkerID.Valid {
				want = 2
			}
			if subscriptions.count(coderdpubsub.ChatStateUpdateChannel(worker.ID)) < want {
				return false
			}
		}
		return true
	}, testutil.IntervalFast)

	require.NoError(t, first.Close())
	firstClosed = true
	restarted.Store(true)
	newActiveTestServer(t, db, ps, configure)

	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		chat, err := db.GetChatByID(ctx, root.ID)
		return err == nil && chat.Status == database.ChatStatusWaiting && !chat.WorkerID.Valid &&
			familyRestartCount(t, chatMessages(ctx, t, db, root.ID), "root done") == 1
	}, testutil.IntervalMedium)

	// Every chat finished without an error and holds no queued message.
	family := []database.Chat{waitForChatStatus(ctx, t, db, root.ID, database.ChatStatusWaiting), waitForChatStatus(ctx, t, db, coordinator.ID, database.ChatStatusWaiting)}
	workers := familyRestartChild(ctx, t, db, coordinator.ID, 3)
	for _, worker := range workers {
		family = append(family, waitForChatStatus(ctx, t, db, worker.ID, database.ChatStatusWaiting))
		familyRestartChild(ctx, t, db, worker.ID, 0)
		// The worker stored its result once, so a duplicated final response
		// fails here even though wait_agent reads only the newest one.
		name := familyRestartWorkerPattern.FindString(familyRestartPrompt(ctx, t, db, worker.ID))
		require.NotEmpty(t, name)
		require.Equal(t, 1, familyRestartCount(t, chatMessages(ctx, t, db, worker.ID), "RESULT-"+name), name)
	}
	for _, chat := range family {
		require.False(t, chat.LastError.Valid, "chat %s failed: %s", chat.Title, chat.LastError.RawMessage)
		queued, err := db.GetChatQueuedMessages(ctx, chat.ID)
		require.NoError(t, err)
		require.Empty(t, queued, "chat %s has queued messages", chat.Title)
	}
	familyRestartChild(ctx, t, db, root.ID, 1)

	// The root got one wake-up and one report of the coordinator, which saw
	// every worker's result.
	rootMessages := chatMessages(ctx, t, db, root.ID)
	require.Equal(t, 1, familyRestartCount(t, rootMessages, familyRestartWake))
	rootWaits := familyRestartParts(t, rootMessages, codersdk.ChatMessagePartTypeToolResult, "wait_agent")
	require.Len(t, rootWaits, 1)
	require.Contains(t, rootWaits[0], "coordinator saw RESULT-WORKER-1 RESULT-WORKER-2 RESULT-WORKER-3")

	// The coordinator got each worker's result and family message once.
	coordinatorMessages := chatMessages(ctx, t, db, coordinator.ID)
	waits := familyRestartParts(t, coordinatorMessages, codersdk.ChatMessagePartTypeToolResult, "wait_agent")
	require.Len(t, waits, 3)
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("WORKER-%d", i)
		require.Equal(t, 1, familyRestartCount(t, coordinatorMessages, "HELLO-"+name), name)
		require.Equal(t, 1, strings.Count(strings.Join(waits, "\n"), "RESULT-"+name), name)
	}
	require.Len(t, familyRestartParts(t, coordinatorMessages, codersdk.ChatMessagePartTypeToolResult, "spawn_agent"), 3)
}

// familyRestartPubsub counts the active subscriptions of each channel.
type familyRestartPubsub struct {
	dbpubsub.Pubsub
	mu     sync.Mutex
	active map[string]int
}

func (p *familyRestartPubsub) Subscribe(event string, listener dbpubsub.Listener) (func(), error) {
	return p.track(event, func() (func(), error) { return p.Pubsub.Subscribe(event, listener) })
}

func (p *familyRestartPubsub) SubscribeWithErr(event string, listener dbpubsub.ListenerWithErr) (func(), error) {
	return p.track(event, func() (func(), error) { return p.Pubsub.SubscribeWithErr(event, listener) })
}

func (p *familyRestartPubsub) track(event string, subscribe func() (func(), error)) (func(), error) {
	cancel, err := subscribe()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.active[event]++
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.active[event]--
			p.mu.Unlock()
		})
		cancel()
	}, nil
}

func (p *familyRestartPubsub) count(event string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active[event]
}

// familyRestartToolCalls returns one streaming chunk that calls each
// name/arguments pair in order, as one parallel tool batch.
func familyRestartToolCalls(calls ...[2]string) chattest.OpenAIChunk {
	chunk := chattest.OpenAIChunk{
		ID:      "chatcmpl-" + uuid.NewString()[:8],
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   "gpt-4",
		Choices: []chattest.OpenAIChunkChoice{{Index: 0}},
	}
	for i, call := range calls {
		chunk.Choices[0].ToolCalls = append(chunk.Choices[0].ToolCalls, chattest.OpenAIToolCall{
			Index:    i,
			ID:       "call_" + uuid.NewString()[:8],
			Type:     "function",
			Function: chattest.OpenAIToolCallFunction{Name: call[0], Arguments: call[1]},
		})
	}
	return chunk
}

// familyRestartChild returns the children of parentID and requires that it
// has want of them.
func familyRestartChild(ctx context.Context, t *testing.T, db database.Store, parentID uuid.UUID, want int) []database.Chat {
	t.Helper()
	rows, err := db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{parentID}})
	require.NoError(t, err)
	require.Len(t, rows, want)
	chats := make([]database.Chat, 0, len(rows))
	for _, row := range rows {
		chats = append(chats, row.Chat)
	}
	return chats
}

// familyRestartPrompt returns the text of the first message of chatID.
func familyRestartPrompt(ctx context.Context, t *testing.T, db database.Store, chatID uuid.UUID) string {
	t.Helper()
	for _, message := range chatMessages(ctx, t, db, chatID) {
		if message.Role == database.ChatMessageRoleUser {
			return strings.Join(familyRestartTexts(t, message), "\n")
		}
	}
	return ""
}

// familyRestartCount counts the text parts of messages that contain s.
func familyRestartCount(t *testing.T, messages []database.ChatMessage, s string) int {
	t.Helper()
	n := 0
	for _, message := range messages {
		for _, text := range familyRestartTexts(t, message) {
			n += strings.Count(text, s)
		}
	}
	return n
}

func familyRestartTexts(t *testing.T, message database.ChatMessage) []string {
	t.Helper()
	parts, err := chatprompt.ParseContent(message)
	require.NoError(t, err)
	var texts []string
	for _, part := range parts {
		if part.Type == codersdk.ChatMessagePartTypeText {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

// familyRestartParts returns the arguments of the tool calls, or the results
// of the tool results, of tool name in messages.
func familyRestartParts(t *testing.T, messages []database.ChatMessage, partType codersdk.ChatMessagePartType, name string) []string {
	t.Helper()
	var out []string
	for _, message := range messages {
		parts, err := chatprompt.ParseContent(message)
		require.NoError(t, err)
		for _, part := range parts {
			if part.Type != partType || part.ToolName != name {
				continue
			}
			if partType == codersdk.ChatMessagePartTypeToolCall {
				out = append(out, string(part.Args))
			} else {
				out = append(out, string(part.Result))
			}
		}
	}
	return out
}
