package chatd

// This file is fork code: it ports the run engine of the fork-workflows
// experiment from the upstream prototype coder/coder#31189 (28f93181b6).
// run_workflow starts a Starlark program that spawns workflow step children
// (fork_workflow_step.go) and combines their workflow_report values
// (fork_workflow_report.go); await_workflow reads the result. The registry
// holds the runs of one replica in memory, and the journal
// (fork_workflow_journal.go) keeps the run and step state in the database,
// so a restart resumes a run and finished steps are never run again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	runWorkflowToolName   = "run_workflow"
	awaitWorkflowToolName = "await_workflow"

	maxWorkflowSteps   = 50         // child chats per run, reused ones included
	maxScriptBytes     = 64 << 10   // the script in the tool input
	maxScriptSteps     = 10_000_000 // Starlark execution steps per run
	maxResultBytes     = 64 << 10   // the JSON that main(args) returns
	maxWorkflowLogs    = 50         // log() lines that a result keeps
	maxWorkflowLogLine = 4 << 10    // bytes of one log() line that a result keeps
	// The result encodes the retained logs on every snapshot, so the line
	// and byte caps below bound that encoding to maxResultBytes too.
	defaultWorkflowWait = 600 * time.Second
	// maxWorkflowWait leaves 2 minutes below defaultTaskTimeout, because
	// turn setup uses the same task attempt.
	maxWorkflowWait = 780 * time.Second
	// workflowStepTimeout bounds one step. A step that runs longer fails.
	workflowStepTimeout = time.Hour

	workflowStatusRunning     = "running"
	workflowStatusCompleted   = "completed"
	workflowStatusFailed      = "failed"
	workflowStatusInterrupted = "interrupted"
	workflowStatusStopped     = "stopped"

	stepStatusRunning  = "running"
	stepStatusReminded = "reminded"
	stepStatusReported = "reported"
	stepStatusFailed   = "failed"
)

const runWorkflowDescription = `Start a workflow run: a Starlark script (a Python dialect) that runs sub-agent steps and combines their results. ` +
	`The tool returns at once with a run_id and status "running". The run continues in the background. ` +
	`Call await_workflow with the run_id to get the result. If your turn ends first, you get a message when the run ends. Then call await_workflow.

Send the script inline in "script", or write it to a file in the workspace and send its absolute path in "script_path". Send one of the two. ` +
	`chatd reads the file one time, when the run starts. A later edit of the file does not change a run that has started. ` +
	`With a file, you can fix a script with small edits and start it again without sending the whole script. ` +
	`run_workflow runs after the other tool calls in the same batch, so a write or edit of the file in that batch is done first.

The script must define main(args). args is the "args" input (a JSON object). main must return a JSON value (dict, list, string, number, bool or None).

Built-ins:
- agent(prompt, type="general", schema=None, title="") starts one step, waits for it, and returns its value. A failed step fails the script.
- spawn(prompt, type="general", schema=None, title="") starts one step and returns a handle at once.
- wait(handles) waits for one handle or a list of handles. It returns the value, or a list of values in input order. A failed step gives None.
- log(message) adds a line to the run log. print() does the same.
- json.encode(value) and json.decode(text).

type is "general" or "explore". schema is a JSON Schema dict with "type": "object" at the top level. ` +
	`The step must call its workflow_report tool with a value that matches the schema. That value is the step value. Without a schema the value is {"text": "..."}.

Rules: the script has no load(), no clock, no file access and no network. while loops are allowed. Recursion is not allowed. ` +
	`Limits: script 64 KiB, 50 steps per run, 10,000,000 execution steps, result 64 KiB. ` +
	`Each step is a child chat of this chat. chatd limits how many sub-agent chats run at the same time. Extra steps wait in a queue.

Resume: a run is journaled in the deployment database, so it survives a restart. ` +
	`If await_workflow returns status "interrupted", call run_workflow with resume_run_id set to the run_id; ` +
	`the script and args can be omitted, chatd replays them from the journal, and changes to the script do not apply to a resumed run. ` +
	`Finished steps are reused and do not run again.`

const awaitWorkflowDescription = `Wait for a workflow run that run_workflow started. ` +
	`It returns when the run ends, or after wait_seconds (default 600, at most 780). ` +
	`After a timeout the status is "running": call it again. ` +
	`The result has status (running, completed, failed or interrupted), result (what main returned), error, steps, and the last 50 log lines. ` +
	`On interrupted, for example after a restart, call run_workflow with resume_run_id to continue the run.`

const workflowNotFoundHelp = `The run is not found on this coderd replica, for example because coderd restarted. ` +
	`Call run_workflow again with the same script (or script_path), the same args and resume_run_id set to this run_id. Finished steps are reused.`

type runWorkflowArgs struct {
	Script      string `json:"script,omitempty" description:"Starlark source that defines main(args). Send this or script_path."`
	ScriptPath  string `json:"script_path,omitempty" description:"Absolute path of a file in the workspace that holds the Starlark source. Send this or script."`
	Args        any    `json:"args,omitempty" description:"A JSON object. main(args) receives it."`
	ResumeRunID string `json:"resume_run_id,omitempty" description:"Continue this run, for example after a restart. The script and args can be omitted; chatd replays them from the journal."`
}

type awaitWorkflowArgs struct {
	RunID       string `json:"run_id"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" description:"Defaults to 600. At most 780. 0 returns the status at once."`
}

func (a awaitWorkflowArgs) wait() time.Duration {
	if a.WaitSeconds == nil {
		return defaultWorkflowWait
	}
	d := time.Duration(*a.WaitSeconds) * time.Second
	return min(max(d, 0), maxWorkflowWait)
}

// runWorkflowResult is the output of both tools.
type runWorkflowResult struct {
	RunID  string          `json:"run_id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Steps  []stepSummary   `json:"steps"`
	Logs   []string        `json:"logs,omitempty"`
}

type stepSummary struct {
	ChatID string `json:"chat_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Reused bool   `json:"reused,omitempty"`
	Error  string `json:"error,omitempty"`
}

// forkWorkflowTools returns run_workflow and await_workflow for a root chat
// turn that is not in plan mode. The gate runs on every turn, so an admin
// can turn the experiment on at runtime without a restart. getConn is the
// workspace connection of the live turn; a nil getConn keeps inline scripts
// working and makes script_path fail with a clear error.
func (p *Server) forkWorkflowTools(
	ctx context.Context,
	chat database.Chat,
	modelConfigID uuid.UUID,
	getConn func(context.Context) (workspacesdk.AgentConn, error),
) []fantasy.AgentTool {
	if !p.experiments.Enabled(codersdk.ExperimentForkWorkflows) ||
		chat.ParentChatID.Valid ||
		isExploreSubagentMode(chat.Mode) ||
		(chat.PlanMode.Valid && chat.PlanMode.ChatPlanMode == database.ChatPlanModePlan) {
		return nil
	}
	// The experiment flag is deployment-wide, so while the engine is
	// experimental the tools stay limited to chats owned by a deployment
	// owner; widening to members is a later decision. One primary-key read
	// per gated turn, failing closed, and a role change applies on the
	// owner's next turn. The turn runs under the daemon subject, which has
	// only read_personal on users, so the owner row is read as the
	// system-restricted actor, the way UserRBACSubject reads roles.
	//nolint:gocritic // chatd reads the owner row to gate its own tools.
	owner, err := p.db.GetUserByID(dbauthz.AsSystemRestricted(ctx), chat.OwnerID)
	if err != nil {
		p.logger.Warn(ctx, "workflow owner gate: load chat owner",
			slog.F("chat_id", chat.ID), slog.F("owner_id", chat.OwnerID), slog.Error(err))
		return nil
	}
	if !slices.Contains(owner.RBACRoles, rbac.RoleOwner().String()) {
		return nil
	}
	if getConn == nil {
		getConn = func(context.Context) (workspacesdk.AgentConn, error) {
			return nil, xerrors.New("script_path needs the workspace connection of a live turn")
		}
	}
	return []fantasy.AgentTool{
		serialWorkflowTool{fantasy.NewAgentTool(
			runWorkflowToolName,
			runWorkflowDescription,
			func(ctx context.Context, args runWorkflowArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
				// chatd sets this ID for each call. It is stable on every retry.
				callID, ok := workspacesdk.ToolCallIDFromContext(ctx)
				if !ok {
					return fantasy.NewTextErrorResponse("run_workflow needs a tool call ID"), nil
				}
				script, err := workflowScript(ctx, args, getConn)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				args.Script = script
				run, err := p.startWorkflowRun(ctx, chat, modelConfigID, args, callID)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				return workflowJSON(run.snapshot()), nil
			},
		)},
		fantasy.NewAgentTool(
			awaitWorkflowToolName,
			awaitWorkflowDescription,
			func(ctx context.Context, args awaitWorkflowArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
				runID, err := uuid.Parse(strings.TrimSpace(args.RunID))
				if err != nil {
					return fantasy.NewTextErrorResponse("run_id must be a valid UUID"), nil
				}
				run := p.workflows.get(runID)
				if run != nil {
					if run.parent.ID != chat.ID {
						return fantasy.NewTextErrorResponse(workflowNotFoundHelp), nil
					}
					return workflowJSON(run.await(ctx, args.wait())), nil
				}
				// The registry lost the run, for example to a restart. The
				// journal answers with the result of a run that ended, or
				// the state of one that a resume continues.
				res, found, err := p.workflowResultFromJournal(ctx, chat.ID, runID)
				if err != nil {
					return fantasy.NewTextErrorResponse("load the workflow run: " + err.Error()), nil
				}
				if !found {
					return fantasy.NewTextErrorResponse(workflowNotFoundHelp), nil
				}
				return workflowJSON(res), nil
			},
		),
	}
}

// serialWorkflowTool makes chatloop run run_workflow after the other tool
// calls of the same step. A script_path call then reads the file after a
// write_file or edit_files call in that step has changed it.
type serialWorkflowTool struct{ fantasy.AgentTool }

func (serialWorkflowTool) SerialToolCalls() bool { return true }

// workflowScript returns args.Script, or the content of the workspace file
// at args.ScriptPath. The run compiles this text one time, so a later edit
// of the file changes only the next run.
func workflowScript(
	ctx context.Context,
	args runWorkflowArgs,
	getConn func(context.Context) (workspacesdk.AgentConn, error),
) (string, error) {
	path := strings.TrimSpace(args.ScriptPath)
	if path == "" {
		return args.Script, nil
	}
	if args.Script != "" {
		return "", xerrors.New("send script or script_path, not both")
	}
	conn, err := getConn(ctx)
	if err != nil {
		return "", xerrors.Errorf("read script_path: %w", err)
	}
	// One byte more than the limit tells an oversized file from a full one.
	rc, _, err := conn.ReadFile(ctx, path, 0, maxScriptBytes+1)
	if err != nil {
		return "", xerrors.Errorf("read script_path %q: %w", path, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", xerrors.Errorf("read script_path %q: %w", path, err)
	}
	if len(data) > maxScriptBytes {
		return "", xerrors.Errorf("the file %q is larger than %d bytes", path, maxScriptBytes)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", xerrors.Errorf("the file %q is empty; write the script to it first", path)
	}
	return string(data), nil
}

func workflowJSON(res runWorkflowResult) fantasy.ToolResponse {
	out, err := json.Marshal(res)
	if err != nil {
		return fantasy.NewTextErrorResponse("marshal workflow result: " + err.Error())
	}
	return fantasy.NewTextResponse(string(out))
}

// workflowRegistry holds the runs of this replica.
type workflowRegistry struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*workflowRun
}

func (g *workflowRegistry) get(id uuid.UUID) *workflowRun {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.runs[id]
}

// add stores run unless a run with its ID exists. It returns the stored run.
func (g *workflowRegistry) add(run *workflowRun) (*workflowRun, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.runs[run.id]; ok {
		return existing, false
	}
	if g.runs == nil {
		g.runs = map[uuid.UUID]*workflowRun{}
	}
	g.runs[run.id] = run
	return run, true
}

func (g *workflowRegistry) remove(id uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.runs, id)
}

type workflowStep struct {
	id      uuid.UUID // the deterministic step ID, which is the child chat ID
	chatID  uuid.UUID
	title   string
	reused  bool
	specKey string
	// occurrence is the number of earlier calls of the same spec in the
	// script, the way the first process numbered the step. ordinal is the
	// creation order of the step inside the run. Both journal the step.
	occurrence int
	ordinal    int
	// The fields below are guarded by workflowRun.mu.
	status string
	value  json.RawMessage
	err    string
	// sentReminder records that this run sent the one retry reminder. The
	// scan in workflowStepReport also detects a reminder, but it matches the
	// message text, which a user_prompt_submit hook can rewrite.
	sentReminder bool
}

type workflowRun struct {
	p             *Server
	id            uuid.UUID
	parent        database.Chat // the parent at start
	modelConfigID uuid.UUID     // the model of the turn that started the run
	program       *workflowProgram
	script        string // the script of the run, journaled at start
	argsJSON      string

	// seen is owned by the script goroutine.
	seen map[string]int

	mu        sync.Mutex
	steps     []*workflowStep
	logs      []string
	logBytes  int // the bytes of the retained logs
	result    *runWorkflowResult
	done      chan struct{}
	awaiters  int
	delivered bool // an await_workflow call returned the end
	notified  bool // the parent got the end message
	// stopRequested marks a run that stop_workflow is unwinding. finish
	// turns its outcome into a stopped run, not a failed one. cancel is the
	// cancel of the script context, set before the script starts.
	stopRequested bool
	cancel        context.CancelFunc
	queuedNotice  int64 // the queued end message, 0 if none
}

func (p *Server) startWorkflowRun(
	ctx context.Context,
	chat database.Chat,
	modelConfigID uuid.UUID,
	args runWorkflowArgs,
	callID uuid.UUID,
) (*workflowRun, error) {
	if len(args.Script) > maxScriptBytes {
		return nil, xerrors.Errorf("the script is %d bytes; at most %d bytes are allowed", len(args.Script), maxScriptBytes)
	}
	runID := workflowRunID(callID)
	if strings.TrimSpace(args.ResumeRunID) != "" {
		id, err := uuid.Parse(strings.TrimSpace(args.ResumeRunID))
		if err != nil {
			return nil, xerrors.New("resume_run_id must be a valid UUID")
		}
		runID = id
	}
	if run := p.workflows.get(runID); run != nil {
		if run.parent.ID != chat.ID {
			return nil, xerrors.New("the run belongs to another chat")
		}
		return run, nil
	}
	// The journal decides between a resume and a fresh start. A row exists
	// once run_workflow journaled the run, so a retry of the same call, a
	// second call with resume_run_id, and a resume after a restart all land
	// here. The journal replays the script and args it holds; a submitted
	// script only names the run.
	row, found, err := p.workflowJournal().getRun(ctx, runID)
	if err != nil {
		return nil, xerrors.Errorf("load the workflow journal: %w", err)
	}
	if found {
		if row.ChatID != chat.ID {
			return nil, xerrors.New("the run belongs to another chat")
		}
		return p.resumeWorkflowRun(ctx, chat, row)
	}
	if strings.TrimSpace(args.Script) == "" {
		return nil, xerrors.New("script is required")
	}
	parent, err := p.loadSubagentSpawnParentChat(ctx, func() database.Chat { return chat })
	if err != nil {
		return nil, err
	}
	argsJSON := "{}"
	if args.Args != nil {
		raw, err := json.Marshal(args.Args)
		if err != nil {
			return nil, xerrors.Errorf("marshal args: %w", err)
		}
		argsJSON = string(raw)
	}
	program, err := compileWorkflow(args.Script)
	if err != nil {
		return nil, err
	}
	run, added := p.workflows.add(&workflowRun{
		p:             p,
		id:            runID,
		parent:        parent,
		modelConfigID: modelConfigID,
		program:       program,
		script:        args.Script,
		argsJSON:      argsJSON,
		seen:          map[string]int{},
		done:          make(chan struct{}),
	})
	if !added {
		return run, nil
	}
	if err := p.workflowJournal().insertRun(ctx, run); err != nil {
		p.workflows.remove(runID)
		return nil, xerrors.Errorf("journal the workflow run: %w", err)
	}
	// The run outlives the tool call. A fresh context drops the tool call
	// ID: the workspace agent answers a repeated ID with a saved response.
	// The run does the same chatd work as spawn_agent and wait_agent, which
	// also run as the chatd subject.
	runCtx, stop := p.inflightChatContext(dbauthz.AsChatd(context.Background()), parent) //nolint:gocritic // see above
	run.mu.Lock()
	run.cancel = stop
	run.mu.Unlock()
	err = p.goInflight(func() {
		defer stop()
		run.finish(runCtx, run.execute(runCtx))
	})
	if err != nil {
		stop()
		p.workflows.remove(runID)
		p.workflowJournal().updateRunStatus(ctx, runID, workflowStatusFailed, "start the run: "+err.Error(), nil)
		return nil, xerrors.Errorf("start workflow run: %w", err)
	}
	return run, nil
}

// execute runs the script and builds the final result.
func (r *workflowRun) execute(ctx context.Context) runWorkflowResult {
	if r.isStopRequested() {
		return r.stoppedResult()
	}
	value, err := r.program.run(ctx, r)
	// Whatever the script outcome, a step that is still running has nobody
	// waiting for its report, so the run stops it instead of leaving the
	// child chat (and its descendants) working to a deadline. A canceled
	// context (a stop or a shutdown) cannot run this phase, so it runs on a
	// fresh one.
	ictx, stop := r.p.inflightChatContext(dbauthz.AsChatd(context.Background()), r.parent)
	defer stop()
	r.interruptOpenSteps(ictx, r.stopReason())
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.snapshotLocked()
	if err != nil {
		if r.stopRequested && ctx.Err() != nil {
			res.Status = workflowStatusStopped
			res.Error = workflowStopError
			return res
		}
		res.Status = workflowStatusFailed
		res.Error = err.Error()
		return res
	}
	res.Status = workflowStatusCompleted
	res.Result = value
	return res
}

// stoppedResult is the snapshot of a run that stop_workflow unwound before
// or instead of the script. It fails the open steps on a fresh context, the
// way execute does for a script outcome.
func (r *workflowRun) stoppedResult() runWorkflowResult {
	ictx, stop := r.p.inflightChatContext(dbauthz.AsChatd(context.Background()), r.parent)
	defer stop()
	r.interruptOpenSteps(ictx, workflowStopError)
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.snapshotLocked()
	res.Status = workflowStatusStopped
	res.Error = workflowStopError
	return res
}

// stopReason is the failure reason of the open steps that the run ends: the
// engine's own text, or the stop text when stop_workflow unwound the run.
func (r *workflowRun) stopReason() string {
	if r.isStopRequested() {
		return workflowStopError
	}
	return "the workflow ended before the step reported"
}

// requestStop marks the run for a stop and cancels the script context. It
// returns false when the run already ended.
func (r *workflowRun) requestStop() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.result != nil {
		return false
	}
	r.stopRequested = true
	if r.cancel != nil {
		r.cancel()
	}
	return true
}

func (r *workflowRun) isStopRequested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopRequested
}

// finish stores the result. If no await_workflow call waits, it tells the
// parent that the run ended.
func (r *workflowRun) finish(ctx context.Context, res runWorkflowResult) {
	// The journal keeps the result for the resumes and awaits of a later
	// process, best effort like every journal update of a live run. A
	// canceled context would fail the write, so a user stop journals on a
	// fresh one: a stopped run must read as stopped. A shutdown keeps its
	// canceled context, leaving the run open for the startup sweep to mark
	// interrupted, which is what resume_run_id waits for.
	//
	// The write lands before done closes: stop_workflow waits on done and
	// then reads the journal for its response, so closing done first could
	// answer a stop from a row that still says running.
	jctx := ctx
	if r.isStopRequested() && ctx.Err() != nil {
		fresh, stop := r.p.inflightContext(dbauthz.AsSystemRestricted(context.Background()))
		defer stop()
		jctx = fresh
	}
	r.p.workflowJournal().updateRunStatus(jctx, r.id, res.Status, res.Error, &res)

	r.mu.Lock()
	r.result = &res
	close(r.done)
	waiting := r.awaiters > 0
	r.notified = !waiting
	r.mu.Unlock()
	// A stopped server must not send a message. The model resumes the run.
	if waiting || ctx.Err() != nil {
		return
	}
	r.notify(ctx)
}

func (r *workflowRun) notify(ctx context.Context) {
	r.mu.Lock()
	status := r.result.Status
	r.mu.Unlock()
	text := fmt.Sprintf("Workflow run %s ended with status %q. Call %s with run_id %q to read the result.",
		r.id, status, awaitWorkflowToolName, r.id.String())
	sent, err := r.p.SendMessage(ctx, SendMessageOptions{
		ChatID:       r.parent.ID,
		CreatedBy:    r.parent.OwnerID,
		Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText(text)},
		BusyBehavior: SendMessageBusyBehaviorQueue, // an idle parent starts a new turn
	})
	if err != nil {
		r.p.logger.Warn(ctx, "send workflow end message",
			slog.F("chat_id", r.parent.ID), slog.F("run_id", r.id), slog.Error(err))
		return
	}
	if !sent.Queued || sent.QueuedMessage == nil {
		return
	}
	r.mu.Lock()
	deleteNow := r.delivered
	if !deleteNow {
		r.queuedNotice = sent.QueuedMessage.ID
	}
	r.mu.Unlock()
	if deleteNow {
		r.deleteNotice(ctx, sent.QueuedMessage.ID)
	}
}

func (r *workflowRun) deleteNotice(ctx context.Context, id int64) {
	if err := r.p.DeleteQueued(ctx, r.parent.ID, id); err != nil {
		r.p.logger.Debug(ctx, "delete workflow end message",
			slog.F("chat_id", r.parent.ID), slog.F("run_id", r.id), slog.Error(err))
	}
}

// await waits for the end of the run, at most for wait.
func (r *workflowRun) await(ctx context.Context, wait time.Duration) runWorkflowResult {
	r.mu.Lock()
	r.awaiters++
	r.mu.Unlock()

	timer := r.p.clock.NewTimer(wait, "chatd", "workflow_await")
	defer timer.Stop()
	select {
	case <-r.done:
	case <-timer.C:
	case <-ctx.Done():
	}

	r.mu.Lock()
	r.awaiters--
	ended := r.result != nil
	var res runWorkflowResult
	if ended {
		res = *r.result
	} else {
		res = r.snapshotLocked()
	}
	var noticeID int64
	sendNotice := false
	switch {
	case ended && ctx.Err() == nil:
		r.delivered = true
		noticeID, r.queuedNotice = r.queuedNotice, 0
	case ended && !r.delivered && !r.notified && r.awaiters == 0:
		// finish saw this call and sent no message, but the call stopped.
		r.notified = true
		sendNotice = true
	}
	r.mu.Unlock()

	if noticeID != 0 {
		r.deleteNotice(ctx, noticeID)
	}
	if sendNotice {
		// The message outlives the tool call, as in startWorkflowRun.
		noticeCtx, stop := r.p.inflightChatContext(dbauthz.AsChatd(context.Background()), r.parent) //nolint:gocritic // see startWorkflowRun
		if err := r.p.goInflight(func() {
			defer stop()
			r.notify(noticeCtx)
		}); err != nil {
			stop()
		}
	}
	return res
}

func (r *workflowRun) snapshot() runWorkflowResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.result != nil {
		return *r.result
	}
	return r.snapshotLocked()
}

func (r *workflowRun) snapshotLocked() runWorkflowResult {
	res := runWorkflowResult{
		RunID:  r.id.String(),
		Status: workflowStatusRunning,
		Steps:  make([]stepSummary, 0, len(r.steps)),
		Logs:   append([]string(nil), r.logs...),
	}
	for _, s := range r.steps {
		res.Steps = append(res.Steps, stepSummary{
			ChatID: s.chatID.String(),
			Title:  s.title,
			Status: s.status,
			Reused: s.reused,
			Error:  s.err,
		})
	}
	return res
}

func (r *workflowRun) log(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendLogLocked(msg)
}

func (r *workflowRun) appendLogLocked(msg string) {
	msg = truncateLogLine(msg, maxWorkflowLogLine)
	r.logs = append(r.logs, msg)
	r.logBytes += len(msg)
	for len(r.logs) > maxWorkflowLogs || r.logBytes > maxResultBytes {
		r.logBytes -= len(r.logs[0])
		r.logs = r.logs[1:]
	}
}

// truncateLogLine cuts msg to at most max bytes on a rune boundary, so the
// result JSON keeps valid UTF-8.
func truncateLogLine(msg string, max int) string {
	if len(msg) <= max {
		return msg
	}
	for max > 0 && !utf8.RuneStart(msg[max]) {
		max--
	}
	return msg[:max]
}

// startStep reuses the child chat of a step that an earlier runner started,
// or it creates the child chat.
func (r *workflowRun) startStep(ctx context.Context, spec stepSpec) (*workflowStep, error) {
	// The defaults apply before the step key, so the same call derives the
	// same step chat ID on every run of the same script.
	switch spec.Type {
	case "":
		spec.Type = subagentTypeGeneral
	case subagentTypeGeneral, subagentTypeExplore:
	default:
		return nil, xerrors.Errorf("type must be %q or %q", subagentTypeGeneral, subagentTypeExplore)
	}
	if len(spec.Schema) == 0 {
		spec.Schema = json.RawMessage(defaultStepSchema)
	}
	r.mu.Lock()
	count := len(r.steps)
	r.mu.Unlock()
	if count >= maxWorkflowSteps {
		return nil, xerrors.Errorf("a run can create %d steps at most", maxWorkflowSteps)
	}

	key, err := spec.key()
	if err != nil {
		return nil, err
	}
	occurrence := r.seen[key]
	r.seen[key]++
	id := workflowStepID(r.id, key, occurrence)

	// A journal row with this ID belongs to a step that a previous process
	// started. A finished step replays its journaled value; an open one
	// re-runs in its child chat, which waitStep drives as usual.
	r.mu.Lock()
	var journaled *workflowStep
	for _, s := range r.steps {
		if s.id == id {
			journaled = s
			break
		}
	}
	r.mu.Unlock()
	if journaled != nil {
		// The step came from the journal: whether it finished before the
		// restart or is still open, the script does not run it again, so
		// the await result reports it as reused.
		r.mu.Lock()
		journaled.reused = true
		r.mu.Unlock()
		return journaled, nil
	}

	child, reused, err := createWorkflowStepChat(ctx, r.p, r.parent, r.modelConfigID, r.id, id, spec)
	if err != nil {
		return nil, err
	}
	step := &workflowStep{
		id:         id,
		chatID:     child.ID,
		title:      child.Title,
		reused:     reused,
		specKey:    key,
		occurrence: occurrence,
		ordinal:    count,
		status:     stepStatusRunning,
	}
	if err := r.p.workflowJournal().insertStep(ctx, r.id, step); err != nil {
		// The child chat runs without a journal row. Stop it: a step that
		// the script never reads again must not consume model capacity.
		r.interruptStepChild(ctx, step)
		return nil, xerrors.Errorf("journal the workflow step: %w", err)
	}
	r.mu.Lock()
	r.steps = append(r.steps, step)
	r.mu.Unlock()
	return step, nil
}

// waitStep blocks until the step has a value or fails. A child that stops
// without a value gets one reminder.
func (r *workflowRun) waitStep(ctx context.Context, step *workflowStep) error {
	deadline := r.p.clock.Now().Add(workflowStepTimeout)
	for {
		r.mu.Lock()
		status := step.status
		r.mu.Unlock()
		if status == stepStatusReported || status == stepStatusFailed {
			return nil
		}
		if !r.p.clock.Now().Before(deadline) {
			r.failStep(ctx, step, "the step ran longer than "+workflowStepTimeout.String())
			// Nobody reads the child's value now, so stop it instead of
			// leaving it running to its own end: interruptOpenSteps no
			// longer sees a failed step.
			r.interruptStepChild(ctx, step)
			return nil
		}
		_, _, err := r.p.awaitSubagentCompletion(ctx, r.parent.ID, step.chatID, defaultSubagentWaitTimeout)
		if errors.Is(err, ErrSubagentWaitTimeout) {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if statusErr, ok := errors.AsType[*subagentStatusError](err); ok {
			r.failStep(ctx, step, "the child chat failed: "+statusErr.reason)
			return nil
		}
		if err != nil {
			r.failStep(ctx, step, err.Error())
			return nil
		}
		value, reminded, err := workflowStepReport(ctx, r.p.db, step.chatID)
		if err != nil {
			return err
		}
		switch {
		case value != nil:
			r.mu.Lock()
			step.status, step.value = stepStatusReported, value
			r.mu.Unlock()
			r.p.workflowJournal().updateStep(ctx, step)
		case !reminded && !step.sentReminder:
			if _, err := r.p.sendSubagentMessage(ctx, r.parent.ID, step.chatID,
				workflowReminder); err != nil {
				r.failStep(ctx, step, "send reminder: "+err.Error())
				return nil
			}
			r.mu.Lock()
			step.status, step.sentReminder = stepStatusReminded, true
			r.mu.Unlock()
			r.p.workflowJournal().updateStep(ctx, step)
		default:
			r.failStep(ctx, step, "the step stopped without a "+workflowReportToolName+" call")
		}
	}
}

func (r *workflowRun) failStep(ctx context.Context, step *workflowStep, reason string) {
	r.mu.Lock()
	step.status, step.err = stepStatusFailed, reason
	r.appendLogLocked(fmt.Sprintf("step %q failed: %s", step.title, reason))
	r.mu.Unlock()
	r.p.workflowJournal().updateStep(ctx, step)
}

// interruptOpenSteps stops the child chats of steps that the script left
// unreported: a failed, timed-out, stopped, or returning script never reads
// their value, so they must not keep consuming model capacity until their
// step timeout. waitStep has already returned when this runs, so the step
// fields have no other writer.
func (r *workflowRun) interruptOpenSteps(ctx context.Context, reason string) {
	r.mu.Lock()
	var open []*workflowStep
	for _, s := range r.steps {
		if s.status == stepStatusRunning || s.status == stepStatusReminded {
			s.status, s.err = stepStatusFailed, reason
			open = append(open, s)
		}
	}
	r.mu.Unlock()
	for _, step := range open {
		r.interruptStepChild(ctx, step)
		r.p.workflowJournal().updateStep(ctx, step)
	}
}

// interruptStepChild stops the child chat of a step the run no longer waits
// for. InterruptChat cascades to the child's own descendants.
func (r *workflowRun) interruptStepChild(ctx context.Context, step *workflowStep) {
	child, err := r.p.db.GetChatByID(ctx, step.chatID)
	if err != nil {
		r.p.logger.Warn(ctx, "load workflow step chat",
			slog.F("chat_id", step.chatID), slog.F("run_id", r.id), slog.Error(err))
		return
	}
	if child.Status == database.ChatStatusWaiting || child.Status == database.ChatStatusError ||
		child.Status == database.ChatStatusPaused {
		return // nothing runs in the child
	}
	if _, _, err := r.p.InterruptChat(ctx, child); err != nil {
		r.p.logger.Warn(ctx, "interrupt workflow step chat",
			slog.F("chat_id", step.chatID), slog.F("run_id", r.id), slog.Error(err))
	}
}

// stepValue returns the value of a finished step. ok is false for a
// failed step.
func (r *workflowRun) stepValue(step *workflowStep) (value json.RawMessage, reason string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return step.value, step.err, step.status == stepStatusReported
}
