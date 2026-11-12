package chatd

// This file is fork code: it ports the run engine tests of the
// fork-workflows experiment from the upstream prototype coder/coder#31189
// (28f93181b6). The fixture plays each step child by hand: a passive server
// (no worker) runs the script, and the test writes the child's messages and
// status.

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
	"github.com/coder/coder/v2/testutil"
)

const reviewSchemaStarlark = `REVIEW = {"type": "object", "required": ["bugs"],
          "properties": {"bugs": {"type": "array", "items": {"type": "string"}}}}
`

// parentStatus creates the parent chat and pins its status, the way a turn
// in flight (running) or a finished turn (waiting) looks to the run.
func (f *forkWorkflowFixture) parentStatus(ctx context.Context, t *testing.T, status database.ChatStatus) database.Chat {
	t.Helper()
	parent := f.parent(ctx, t)
	setChatStatus(ctx, t, f.db, parent.ID, status, "")
	parent, err := f.db.GetChatByID(ctx, parent.ID)
	require.NoError(t, err)
	return parent
}

func (f *forkWorkflowFixture) start(
	ctx context.Context, t *testing.T, parent database.Chat, args runWorkflowArgs,
) *workflowRun {
	t.Helper()
	run, err := f.server.startWorkflowRun(ctx, parent, f.model.ID, args, uuid.New())
	require.NoError(t, err)
	return run
}

// steps waits until the run has n steps. A run that ends first fails the
// test cleanly with its error: a failed run never holds an awaiter, so a
// helper that only waits for progress would time out and hide the cause.
func (*forkWorkflowFixture) steps(ctx context.Context, t *testing.T, run *workflowRun, n int) []stepSummary {
	t.Helper()
	var steps []stepSummary
	testutil.Eventually(ctx, t, func(context.Context) bool {
		steps = run.snapshot().Steps
		if len(steps) >= n {
			return true
		}
		return run.snapshot().Status != workflowStatusRunning
	}, testutil.IntervalFast)
	snap := run.snapshot()
	require.Len(t, steps, n, "the run ended before %d steps: status %q, error %q", n, snap.Status, snap.Error)
	return steps
}

func (f *forkWorkflowFixture) insert(t *testing.T, chatID uuid.UUID, role database.ChatMessageRole, parts ...codersdk.ChatMessagePart) {
	t.Helper()
	data, err := json.Marshal(parts)
	require.NoError(t, err)
	_ = dbgen.ChatMessage(t, f.db, database.ChatMessage{
		ChatID:         chatID,
		ModelConfigID:  uuid.NullUUID{UUID: f.model.ID, Valid: true},
		Role:           role,
		Content:        pqtype.NullRawMessage{RawMessage: data, Valid: true},
		ContentVersion: chatprompt.ContentVersionV1,
	})
}

// endTurn sets the child to waiting and wakes awaitSubagentCompletion.
func (f *forkWorkflowFixture) endTurn(ctx context.Context, t *testing.T, chatID uuid.UUID) {
	t.Helper()
	setChatStatus(ctx, t, f.db, chatID, database.ChatStatusWaiting, "")
	require.NoError(t, f.ps.Publish(coderdpubsub.ChatStateUpdateChannel(chatID), []byte("{}")))
}

// report plays a child that calls workflow_report with value.
func (f *forkWorkflowFixture) report(ctx context.Context, t *testing.T, chatID string, value string) {
	t.Helper()
	id := uuid.MustParse(chatID)
	callID := "call-" + uuid.NewString()
	f.insert(t, id, database.ChatMessageRoleAssistant,
		codersdk.ChatMessageToolCall(callID, workflowReportToolName, json.RawMessage(value)))
	f.insert(t, id, database.ChatMessageRoleTool,
		codersdk.ChatMessageToolResult(callID, workflowReportToolName, json.RawMessage(`"Value accepted."`), false, false))
	f.endTurn(ctx, t, id)
}

// stopWithoutReport plays a child that ends its turn with text only.
func (f *forkWorkflowFixture) stopWithoutReport(ctx context.Context, t *testing.T, chatID string) {
	t.Helper()
	id := uuid.MustParse(chatID)
	f.insert(t, id, database.ChatMessageRoleAssistant, codersdk.ChatMessageText("I am done."))
	f.endTurn(ctx, t, id)
}

func (f *forkWorkflowFixture) userTexts(ctx context.Context, t *testing.T, chatID uuid.UUID) []string {
	t.Helper()
	msgs, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: chatID})
	require.NoError(t, err)
	var texts []string
	for _, msg := range msgs {
		if msg.Role != database.ChatMessageRoleUser {
			continue
		}
		parts, err := chatprompt.ParseContent(msg)
		require.NoError(t, err)
		for _, part := range parts {
			if part.Type == codersdk.ChatMessagePartTypeText {
				texts = append(texts, part.Text)
			}
		}
	}
	return texts
}

func (f *forkWorkflowFixture) hasNotice(ctx context.Context, t *testing.T, chatID uuid.UUID) bool {
	t.Helper()
	for _, text := range f.userTexts(ctx, t, chatID) {
		if strings.Contains(text, "ended with status") {
			return true
		}
	}
	return false
}

// awaitRegistered starts await in a goroutine and returns once the run
// counts it, so finish sees a waiting call. A run that already ended never
// holds an awaiter for long, so a finished run counts as registered too and
// the result waits in the buffered channel.
func awaitRegistered(ctx context.Context, t *testing.T, run *workflowRun) <-chan runWorkflowResult {
	t.Helper()
	out := make(chan runWorkflowResult, 1)
	go func() { out <- run.await(ctx, testutil.WaitLong) }()
	testutil.Eventually(ctx, t, func(context.Context) bool {
		run.mu.Lock()
		defer run.mu.Unlock()
		return run.awaiters == 1 || run.result != nil
	}, testutil.IntervalFast)
	return out
}

func TestForkWorkflowScriptPath(t *testing.T) {
	t.Parallel()
	const path = "/home/coder/workflow.star"
	connWithFile := func(t *testing.T, content string) func(context.Context) (workspacesdk.AgentConn, error) {
		conn := agentconnmock.NewMockAgentConn(gomock.NewController(t))
		// The limit is one byte over the cap, so an oversized file is caught.
		conn.EXPECT().ReadFile(gomock.Any(), path, int64(0), int64(maxScriptBytes+1)).
			Return(io.NopCloser(strings.NewReader(content)), "", nil)
		return func(context.Context) (workspacesdk.AgentConn, error) { return conn, nil }
	}
	noConn := func(context.Context) (workspacesdk.AgentConn, error) {
		return nil, xerrors.New("the workspace must not be read")
	}

	t.Run("ReadsTheFile", func(t *testing.T) {
		t.Parallel()
		const src = "def main(args):\n    return 1\n"
		got, err := workflowScript(context.Background(), runWorkflowArgs{ScriptPath: path}, connWithFile(t, src))
		require.NoError(t, err)
		assert.Equal(t, src, got)
	})
	t.Run("InlineScriptSkipsTheWorkspace", func(t *testing.T) {
		t.Parallel()
		got, err := workflowScript(context.Background(), runWorkflowArgs{Script: "x"}, noConn)
		require.NoError(t, err)
		assert.Equal(t, "x", got)
	})
	t.Run("RejectsBoth", func(t *testing.T) {
		t.Parallel()
		_, err := workflowScript(context.Background(), runWorkflowArgs{Script: "x", ScriptPath: path}, noConn)
		require.ErrorContains(t, err, "not both")
	})
	t.Run("RejectsAnOversizedFile", func(t *testing.T) {
		t.Parallel()
		big := strings.Repeat("#", maxScriptBytes+1)
		_, err := workflowScript(context.Background(), runWorkflowArgs{ScriptPath: path}, connWithFile(t, big))
		require.ErrorContains(t, err, "larger than")
	})
}

func TestForkWorkflowReplay(t *testing.T) {
	t.Parallel()
	f := newForkWorkflowFixture(t)
	ctx := chatdTestContext(t)
	parent := f.parentStatus(ctx, t, database.ChatStatusRunning)

	args := runWorkflowArgs{
		Script: reviewSchemaStarlark + `
def main(args):
    steps = [spawn("Review %s for bugs." % name, schema = REVIEW, title = name) for name in args["files"]]
    reviews = wait(steps)
    bugs = [b for r in reviews if r != None for b in r["bugs"]]
    log("%d bugs" % len(bugs))
    return {"bugs": bugs}
`,
		Args: map[string]any{"files": []string{"a.go", "b.go"}},
	}
	run := f.start(ctx, t, parent, args)
	assert.Equal(t, workflowStatusRunning, run.snapshot().Status, "run_workflow returns at once")
	done := awaitRegistered(ctx, t, run)

	steps := f.steps(ctx, t, run, 2)
	f.report(ctx, t, steps[0].ChatID, `{"bugs":["nil map write"]}`)
	f.report(ctx, t, steps[1].ChatID, `{"bugs":[]}`)

	res := testutil.RequireReceive(ctx, t, done)
	require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
	assert.JSONEq(t, `{"bugs":["nil map write"]}`, string(res.Result))
	assert.Equal(t, []string{"1 bugs"}, res.Logs)
	assert.False(t, f.hasNotice(ctx, t, parent.ID), "the waiting call got the result, so no message")

	// A restart loses the run. The model resumes it with the same script.
	f.server.workflows.remove(run.id)
	args.ResumeRunID = run.id.String()
	resumed := f.start(ctx, t, parent, args)
	res = resumed.await(ctx, testutil.WaitLong)
	require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
	assert.JSONEq(t, `{"bugs":["nil map write"]}`, string(res.Result))
	require.Len(t, res.Steps, 2)
	for i, step := range res.Steps {
		assert.True(t, step.Reused, "step %d must be reused", i)
		assert.Equal(t, steps[i].ChatID, step.ChatID)
	}
}

func TestForkWorkflowRemind(t *testing.T) {
	t.Parallel()
	f := newForkWorkflowFixture(t)

	t.Run("ReportAfterReminder", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    return agent("Say hi.", title = "hi")
`})
		step := f.steps(ctx, t, run, 1)[0]
		childID := uuid.MustParse(step.ChatID)
		f.stopWithoutReport(ctx, t, step.ChatID)
		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			return strings.Contains(strings.Join(f.userTexts(ctx, t, childID), "\n"), workflowReminder)
		}, testutil.IntervalFast)
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)

		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"hi"}`, string(res.Result))
	})

	t.Run("ReminderFlagOutlivesScanBlindness", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// A step child built by hand, so the test drives waitStep
		// directly.
		child := dbgen.Chat(t, f.db, database.Chat{
			OrganizationID:    f.org.ID,
			OwnerID:           f.user.ID,
			ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
			LastModelConfigID: f.model.ID,
			Status:            database.ChatStatusWaiting,
		})
		run := &workflowRun{
			p:      f.server,
			id:     uuid.New(),
			parent: parent,
			seen:   map[string]int{},
			done:   make(chan struct{}),
		}
		step := &workflowStep{chatID: child.ID, title: "hooked", status: stepStatusRunning}
		run.mu.Lock()
		run.steps = []*workflowStep{step}
		run.mu.Unlock()

		sentReminder := func() bool {
			for _, text := range f.userTexts(ctx, t, child.ID) {
				if strings.TrimSpace(text) == workflowReminder {
					return true
				}
			}
			return false
		}
		// A user_prompt_submit hook rewrote the reminder before chatd
		// persisted it, so the scan cannot recognize it. The run's own
		// flag must keep the reminder at one: waitStep fails the step
		// without sending another reminder.
		f.insert(t, child.ID, database.ChatMessageRoleUser,
			codersdk.ChatMessageText(workflowReminder+"\u200b"))
		run.mu.Lock()
		step.sentReminder = true
		run.mu.Unlock()
		require.NoError(t, run.waitStep(ctx, step))
		run.mu.Lock()
		status := step.status
		run.mu.Unlock()
		require.Equal(t, stepStatusFailed, status)
		assert.False(t, sentReminder(), "the flag must stop a second reminder")

		// With the flag unset, the same blind scan sends the reminder.
		// The server is passive, so only the test can end the child's
		// turn that the reminder queues; waitStep blocks on it.
		run.mu.Lock()
		step.status, step.sentReminder = stepStatusRunning, false
		run.mu.Unlock()
		waited := make(chan error, 1)
		go func() { waited <- run.waitStep(ctx, step) }()
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return sentReminder()
		}, testutil.IntervalFast)
		f.endTurn(ctx, t, child.ID)
		require.NoError(t, <-waited)
		assert.True(t, sentReminder(), "a blind scan without the flag sends the reminder")
	})

	t.Run("NoReportGivesNone", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    return wait([spawn("Say hi.")])
`})
		step := f.steps(ctx, t, run, 1)[0]
		childID := uuid.MustParse(step.ChatID)
		f.stopWithoutReport(ctx, t, step.ChatID)
		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			return strings.Contains(strings.Join(f.userTexts(ctx, t, childID), "\n"), workflowReminder)
		}, testutil.IntervalFast)
		f.stopWithoutReport(ctx, t, step.ChatID)

		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `[null]`, string(res.Result))
		require.Len(t, res.Steps, 1)
		assert.Equal(t, stepStatusFailed, res.Steps[0].Status)
	})
}

func TestForkWorkflowNotice(t *testing.T) {
	t.Parallel()
	f := newForkWorkflowFixture(t)
	script := runWorkflowArgs{Script: `
def main(args):
    return agent("Say hi.")
`}

	t.Run("IdleParentGetsMessage", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		// The parent turn ended after run_workflow returned.
		parent := f.parentStatus(ctx, t, database.ChatStatusWaiting)
		run := f.start(ctx, t, parent, script)
		f.report(ctx, t, f.steps(ctx, t, run, 1)[0].ChatID, `{"text":"hi"}`)

		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			return f.hasNotice(ctx, t, parent.ID)
		}, testutil.IntervalFast)
		res := run.await(ctx, 0)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
	})

	t.Run("AwaitDeletesQueuedMessage", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		// The parent is busy, so the message waits in the queue.
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, script)
		f.report(ctx, t, f.steps(ctx, t, run, 1)[0].ChatID, `{"text":"hi"}`)

		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			n, err := f.db.CountChatQueuedMessages(ctx, parent.ID)
			return err == nil && n == 1
		}, testutil.IntervalFast)
		res := run.await(ctx, 0)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		// await_workflow read the result, so the message goes. The sender can
		// still hold the message ID, and then it deletes the message itself.
		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			n, err := f.db.CountChatQueuedMessages(ctx, parent.ID)
			return err == nil && n == 0
		}, testutil.IntervalFast)
	})
}

func TestForkWorkflowLimit(t *testing.T) {
	t.Parallel()
	f := newForkWorkflowFixture(t)

	t.Run("Steps", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    for i in range(51):
        spawn("Step %d." % i)
    return {}
`})
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "can create 50 steps at most")
		assert.Len(t, res.Steps, maxWorkflowSteps)
	})

	t.Run("ResultSizeBeforeEncode", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// Doubling builds a huge value with few execution steps, so the
		// size error must come from the budget walk, not from json.encode
		// materializing the string first.
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    s = "x"
    for i in range(20):
        s = s + s
    return s
`})
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "the result encodes to more than")
	})

	t.Run("SchemaSizeBeforeEncode", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// Plain ASCII encodes one byte per character, so the payload must
		// pass 64 KiB on its own.
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    spawn("Big schema.", schema = {"type": "object", "properties": {"pad": {"description": "x" * 70000}}})
    return {}
`})
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "the schema encodes to more than")
	})

	t.Run("IntegersChargeTheirWidth", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    return [0] * 6000
`})
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
	})

	t.Run("LogLinesBounded", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("x" * 1000000)
    log("kept")
    return {}
`})
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		require.Len(t, res.Logs, 2)
		assert.Equal(t, maxWorkflowLogLine, len(res.Logs[0]))
		assert.Equal(t, "kept", res.Logs[1])
	})

	t.Run("UnwaitedStepsFail", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    spawn("Unwaited.")
    return {}
`})
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		require.Len(t, res.Steps, 1)
		assert.Equal(t, stepStatusFailed, res.Steps[0].Status)
		assert.Contains(t, res.Steps[0].Error, "ended before the step reported")
	})

	t.Run("CompileErrors", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		_, err := f.server.startWorkflowRun(ctx, parent, f.model.ID, runWorkflowArgs{Script: "def main(args):\n  return ("}, uuid.New())
		require.ErrorContains(t, err, "compile script")
		_, err = f.server.startWorkflowRun(ctx, parent, f.model.ID, runWorkflowArgs{Script: "x = 1\n"}, uuid.New())
		require.ErrorContains(t, err, "must define main(args)")
		_, err = f.server.startWorkflowRun(ctx, parent, f.model.ID, runWorkflowArgs{Script: "def main(args):\n  return time.now()\n"}, uuid.New())
		require.ErrorContains(t, err, "undefined: time")
	})

	t.Run("RuntimeErrorNamesLine", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: "def main(args):\n  fail(\"boom\")\n"})
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "boom")
		assert.Contains(t, res.Error, "workflow.star:2")
	})

	t.Run("AwaitTimeoutReturnsRunning", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: "def main(args):\n  return agent(\"Wait.\")\n"})
		f.steps(ctx, t, run, 1)
		res := run.await(ctx, 0)
		assert.Equal(t, workflowStatusRunning, res.Status)
		assert.Len(t, res.Steps, 1)
	})
}

// TestForkWorkflowToolsGate pins the turn gate of run_workflow and
// await_workflow: only an enabled experiment on a root chat outside plan
// mode and explore mode gets the tools, whatever role the chat owner holds.
func TestForkWorkflowToolsGate(t *testing.T) {
	t.Parallel()

	t.Run("ExperimentOffReturnsNothing", func(t *testing.T) {
		t.Parallel()
		db, ps := dbtestutil.NewDB(t)
		server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{})
		tools := server.forkWorkflowTools(chatdTestContext(t), database.Chat{ID: uuid.New()}, uuid.New(), nil)
		assert.Empty(t, tools)
	})

	t.Run("Guards", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		getConn := func(context.Context) (workspacesdk.AgentConn, error) { return nil, nil }
		parent := f.parent(ctx, t)

		child := parent
		child.ParentChatID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
		assert.Empty(t, f.server.forkWorkflowTools(ctx, child, uuid.Nil, getConn), "a child chat never runs workflows")

		explore := parent
		explore.Mode = database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true}
		assert.Empty(t, f.server.forkWorkflowTools(ctx, explore, uuid.Nil, getConn), "an explore chat never runs workflows")

		plan := parent
		plan.PlanMode = database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}
		assert.Empty(t, f.server.forkWorkflowTools(ctx, plan, uuid.Nil, getConn), "a plan-mode turn never runs workflows")
	})

	t.Run("MemberRunsAndStopsOnlyInOwnChat", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		// The fixture's chat owner is a plain member: no deployment role.
		require.Empty(t, f.user.RBACRoles)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		tools := f.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)
		require.Len(t, tools, 2, "a member's own chat runs workflows")

		input, err := json.Marshal(runWorkflowArgs{
			Script: journalReviewScript,
			Args:   map[string]any{"files": []string{"a.go"}},
		})
		require.NoError(t, err)
		resp, err := tools[0].Run(workspacesdk.WithToolCallID(ctx, uuid.New()), fantasy.ToolCall{Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		var started runWorkflowResult
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &started))
		runID, err := uuid.Parse(started.RunID)
		require.NoError(t, err)
		run := f.server.workflows.get(runID)
		require.NotNil(t, run)
		f.steps(ctx, t, run, 1)

		// Another member's chat neither sees nor stops the run.
		other := dbgen.User(t, f.db, database.User{})
		dbgen.OrganizationMember(t, f.db, database.OrganizationMember{UserID: other.ID, OrganizationID: f.org.ID})
		otherChat, err := f.server.CreateChat(ctx, CreateOptions{
			OrganizationID:     f.org.ID,
			OwnerID:            other.ID,
			Title:              "workflow-other-" + t.Name(),
			ModelConfigID:      f.model.ID,
			InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
		})
		require.NoError(t, err)
		otherTools := f.server.forkWorkflowTools(ctx, otherChat, f.model.ID, nil)
		require.Len(t, otherTools, 2)
		awaitInput := `{"run_id":"` + runID.String() + `","wait_seconds":0}`
		resp, err = otherTools[1].Run(ctx, fantasy.ToolCall{Input: awaitInput})
		require.NoError(t, err)
		assert.True(t, resp.IsError, "another chat's await must not read the run: %s", resp.Content)
		assert.ErrorIs(t, f.server.ForkStopWorkflowRun(ctx, otherChat.ID, runID), ErrForkWorkflowRunNotFound)

		require.NoError(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, runID))
		resp, err = tools[1].Run(ctx, fantasy.ToolCall{Input: awaitInput})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		var stopped runWorkflowResult
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &stopped))
		assert.Equal(t, workflowStatusStopped, stopped.Status, stopped.Error)
	})

	t.Run("RootTurnGetsBothTools", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		tools := f.server.forkWorkflowTools(chatdTestContext(t), f.parent(chatdTestContext(t), t), uuid.New(), nil)
		require.Len(t, tools, 2)
		assert.Equal(t, runWorkflowToolName, tools[0].Info().Name)
		assert.Equal(t, awaitWorkflowToolName, tools[1].Info().Name)
		// run_workflow serializes against the other tool calls of the batch,
		// so a script_path read sees a write from the same batch.
		assert.True(t, tools[0].(interface{ SerialToolCalls() bool }).SerialToolCalls())
	})

	t.Run("NilConnKeepsInlineScripts", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		tools := f.server.forkWorkflowTools(ctx, f.parent(ctx, t), uuid.New(), nil)
		require.Len(t, tools, 2)
		ctx = workspacesdk.WithToolCallID(ctx, uuid.New())
		resp, err := tools[0].Run(ctx, fantasy.ToolCall{Input: `{"script_path":"/home/coder/w.star"}`})
		require.NoError(t, err)
		assert.True(t, resp.IsError, resp.Content)
		assert.Contains(t, resp.Content, "script_path needs the workspace connection")
	})
}
