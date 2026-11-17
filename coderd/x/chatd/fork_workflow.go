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

	"github.com/coder/quartz"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
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
- phase(name) sets the current phase of the run. Steps started after the call are tagged with that phase, and the run card shows a phase rail. ` +
	`A name not in PHASES is accepted and shown as an observed phase. A phase() call past the phase cap fails the run.

A top-level PHASES = ["plan", "build", "review"] list is optional. chatd reads it before main runs and shows its names on the card before the first step, as pending. ` +
	`Names already declared stay in declared order; names first set by phase() are appended.

type is "general" or "explore". schema is a JSON Schema dict with "type": "object" at the top level. ` +
	`The step must call its workflow_report tool with a value that matches the schema. That value is the step value. Without a schema the value is {"text": "..."}.

Rules: the script has no load(), no clock, no file access and no network. while loops are allowed. Recursion is not allowed. ` +
	`Limits: script 64 KiB, 50 steps per run, 10,000,000 execution steps, result 64 KiB, step value 256 KiB. ` +
	`Each step is a child chat of this chat. chatd limits how many sub-agent chats run at the same time. Extra steps wait in a queue.

Resume: a run is journaled in the deployment database, so it survives a restart. ` +
	`If await_workflow returns status "interrupted", call run_workflow with resume_run_id set to the run_id; ` +
	`the script and args can be omitted, chatd replays them from the journal, and changes to the script do not apply to a resumed run. ` +
	`Finished steps are reused and do not run again.

Saved workflows: users save workflows in Coder. To run one, send its name in "name" instead of script or script_path, with its args. ` +
	`Call list_workflows to see the saved workflows you can run, with their descriptions and args schemas. ` +
	`A personal workflow hides an organization workflow with the same name. ` +
	`If the workflow has an args schema, args must match it or the run does not start.`

const awaitWorkflowDescription = `Wait for a workflow run that run_workflow started. ` +
	`It returns when the run ends, or after wait_seconds (default 600, at most 780). ` +
	`After a timeout the status is "running": call it again. ` +
	`The result has status (running, completed, failed or interrupted), result (what main returned), error, steps, and the last 50 log lines. ` +
	`On interrupted, for example after a restart, call run_workflow with resume_run_id to continue the run.`

const workflowNotFoundHelp = `The run is not found on this coderd replica, for example because coderd restarted. ` +
	`Call run_workflow again with the same script (or script_path or name), the same args and resume_run_id set to this run_id. Finished steps are reused.`

type runWorkflowArgs struct {
	Script      string `json:"script,omitempty" description:"Starlark source that defines main(args). Send this or script_path."`
	ScriptPath  string `json:"script_path,omitempty" description:"Absolute path of a file in the workspace that holds the Starlark source. Send this or script."`
	Args        any    `json:"args,omitempty" description:"A JSON object. main(args) receives it."`
	ResumeRunID string `json:"resume_run_id,omitempty" description:"Continue this run, for example after a restart. The script and args can be omitted; chatd replays them from the journal."`
	Name        string `json:"name,omitempty" description:"The name of a saved workflow to run instead of script or script_path. list_workflows lists them."`
	// rawArgs is args as the model sent them, before Args decoded it into
	// Go values. startWorkflowRun journals these bytes, so main(args) and
	// the run detail read the exact value the caller sent.
	rawArgs json.RawMessage
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
	// Phases lists the phases of the run in display order and CurrentPhase
	// names the one the script set last. Both are empty for a run whose
	// script uses no phases. Phases is a JSON array, never null.
	Phases       []string      `json:"phases"`
	CurrentPhase string        `json:"current_phase,omitempty"`
	Steps        []stepSummary `json:"steps"`
	Logs         []string      `json:"logs,omitempty"`
	// interruptedSteps holds the steps that interruptOpenSteps failed on
	// the way to a stopped result. finish logs their failure lines; the
	// lines of steps failed earlier already sit in the logs, so a resume
	// does not log them twice. The field is not serialized.
	interruptedSteps []*workflowStep
}

type stepSummary struct {
	ChatID string `json:"chat_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Reused bool   `json:"reused,omitempty"`
	// Phase is the phase that was current when the step started, '' before
	// the first phase() call.
	Phase string `json:"phase,omitempty"`
	Error string `json:"error,omitempty"`
}

// forkWorkflowTools returns run_workflow, await_workflow and list_workflows
// for a root chat turn that is not in plan mode. Every chat owner gets them:
// a run belongs to its chat, so the chat's own access rules cover the run.
// The gate runs on every turn, so an admin can turn the experiment on at
// runtime without a restart. getConn is the workspace connection of the live
// turn; a nil getConn keeps inline scripts working and makes script_path fail
// with a clear error.
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
	if getConn == nil {
		getConn = func(context.Context) (workspacesdk.AgentConn, error) {
			return nil, xerrors.New("script_path needs the workspace connection of a live turn")
		}
	}
	return []fantasy.AgentTool{
		serialWorkflowTool{fantasy.NewAgentTool(
			runWorkflowToolName,
			runWorkflowDescription,
			func(ctx context.Context, args runWorkflowArgs, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
				// chatd sets this ID for each call. It is stable on every retry.
				callID, ok := workspacesdk.ToolCallIDFromContext(ctx)
				if !ok {
					return fantasy.NewTextErrorResponse("run_workflow needs a tool call ID"), nil
				}
				if strings.TrimSpace(args.Name) != "" && (args.Script != "" || strings.TrimSpace(args.ScriptPath) != "") {
					return fantasy.NewTextErrorResponse("send name, script or script_path, not more than one"), nil
				}
				var input struct {
					Args json.RawMessage `json:"args"`
				}
				if json.Unmarshal([]byte(call.Input), &input) == nil {
					args.rawArgs = input.Args
				}
				script, err := workflowScript(ctx, args, getConn)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				args.Script = script
				run, err := p.startWorkflowRun(ctx, chat, modelConfigID, args, callID)
				if elsewhere, ok := errors.AsType[*workflowRunElsewhereError](err); ok {
					// Another chatd process executes the run; its journal
					// state is the answer.
					return workflowJSON(elsewhere.result), nil
				}
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
		p.listWorkflowsTool(chat),
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

// workflowRegistry holds the runs of this replica and its owner identity.
type workflowRegistry struct {
	mu    sync.Mutex
	runs  map[uuid.UUID]*workflowRun
	owner workflowOwnership
	// resumeMu serializes the claim and the rehydration of the resumes of
	// this process (fork_workflow_journal.go): both callers of one run
	// would pass the claim, which accepts the runs this process owns.
	resumeMu sync.Mutex
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
	// phase is the phase that was current when the step started, '' before
	// the first phase() call. It journals with the step.
	phase string
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

	// phases is the display order of the phases: the declared order of the
	// script's PHASES list first, then every undeclared name that phase()
	// reached, in first-reach order. currentPhase is the phase that the
	// script set last, and it tags the steps that start after the call.
	// Writes come from the script goroutine; reads take the run lock, the
	// way the steps and the logs do.
	phases       []string
	currentPhase string

	mu       sync.Mutex
	steps    []*workflowStep
	logs     []string
	logBytes int // the bytes of the retained logs
	// logSeq counts the appended lines. journalMu guards journaledLogSeq, the
	// seq the journal last accepted, and journalLogWrite, which serializes the
	// journal writes so the write of an older snapshot cannot replace newer
	// lines after a stalled one.
	logSeq          int
	journalMu       sync.Mutex
	journaledLogSeq int
	journalLogWrite chan struct{}
	// logEchoLeft is the number of replayed lines the echo still
	// suppresses, rehydrated as max(fork_seq, len(baseline)): the replayed
	// script prints the live lines again, and appendLogLocked suppresses
	// one replayed line per pre-restart line, by count and not by content,
	// so the view and the journal keep one copy of the pre-restart
	// history. The count, not the content, decides: a capped baseline
	// starts mid-stream, so the replayed prefix does not match it line for
	// line, and a repeated line would otherwise consume the wrong entry.
	// The replay of a journaled failed step re-emits its failure line
	// (failJournaledStep), so the count holds. mu guards the field. The
	// engine's own failure lines (a stop, a timeout, a failed step) are not
	// script lines and cannot be replayed, so they never suppress.
	logEchoLeft int
	// The write-through of the logs paces itself: lastLogFlush is the last
	// attempt the journal made, accepted or not. Exactly two paths stamp
	// it, the immediate attempt of scheduleLogFlush and the firing of the
	// paced fallback timer; the failStep flush and the terminal flush of
	// the end do not, so a line that lands after them still paces behind
	// its own interval. logFlushTimer holds the pending flush of a
	// burst, logFlushInFlight marks the goroutine that flushes now, and
	// flushClosing marks a run whose terminal flush has started:
	// cancelLogFlush sets it on the run-ending path, so a timer or
	// goroutine that raced the stop stamps nothing and re-arms nothing.
	// journalMu guards all four.
	lastLogFlush     time.Time
	logFlushTimer    *quartz.Timer
	logFlushInFlight bool
	flushClosing     bool
	// flushAttempts counts the flush attempts, taken token or not. It is
	// test observability: the pacing tests count attempts directly instead
	// of sampling logFlushInFlight, which a poll can miss entirely.
	flushAttempts int
	result        *runWorkflowResult
	done          chan struct{}
	awaiters      int
	delivered     bool // an await_workflow call returned the end
	notified      bool // the parent got the end message
	// stopRequested marks a run that stop_workflow is unwinding. finish
	// turns its outcome into a stopped run, not a failed one. cancel is the
	// cancel of the script context, set before the script starts.
	stopRequested bool
	cancel        context.CancelFunc
	queuedNotice  int64 // the queued end message, 0 if none
	// lost marks a run that another process took over or a sweep
	// interrupted (fork_workflow_owner.go). Its script is canceled, and
	// finish ends it without a journal write or an end message.
	lost bool
	// terminalPending marks a run whose terminal journal write failed. The
	// heartbeat keeps its row alive and retries the write
	// (fork_workflow_owner.go). mu guards the field.
	terminalPending bool
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
	// A saved workflow's declared phases seed the run before the script
	// parses, so the rail shows them without the script repeating the list
	// in a PHASES global. The phase cap counts the saved list together with
	// any PHASES the script declares; readDeclaredPhases fails the run when
	// the two sources pass it.
	savedPhases := []string(nil)
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
	if name := strings.TrimSpace(args.Name); name != "" {
		if args.Script != "" {
			return nil, xerrors.New("send name or script, not both")
		}
		saved, script, err := p.loadSavedWorkflow(ctx, chat, name)
		if err != nil {
			return nil, err
		}
		if err := validateSavedWorkflowArgs(saved, args.Args, args.rawArgs); err != nil {
			return nil, err
		}
		args.Script = script
		savedPhases = saved.Phases
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
		if len(args.rawArgs) > 0 {
			// rawArgs is the args bytes as the caller sent them. Marshaling
			// args.Args instead would re-encode the value the Go decode built,
			// and a number outside the float64 exact range would round: the
			// script and the journal would hold a value main(args) never
			// received. A raw null decodes to Args nil, so it falls through to
			// the empty object like a missing args field.
			argsJSON = string(args.rawArgs)
		} else {
			raw, err := json.Marshal(args.Args)
			if err != nil {
				return nil, xerrors.Errorf("marshal args: %w", err)
			}
			argsJSON = string(raw)
		}
	}
	program, err := compileWorkflow(args.Script)
	if err != nil {
		return nil, err
	}
	run, added := p.workflows.add(&workflowRun{
		p:               p,
		id:              runID,
		parent:          parent,
		modelConfigID:   modelConfigID,
		program:         program,
		script:          args.Script,
		argsJSON:        argsJSON,
		seen:            map[string]int{},
		phases:          savedPhases,
		done:            make(chan struct{}),
		journalLogWrite: make(chan struct{}, 1),
	})
	if !added {
		return run, nil
	}
	// The write token starts in the channel, so the first flush takes
	// it at once.
	run.journalLogWrite <- struct{}{}
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
		run.mu.Lock()
		phases := run.phasesSnapshot()
		run.mu.Unlock()
		p.workflowJournal().updateRunStatus(ctx, runID, workflowStatusFailed, "start the run: "+err.Error(), nil, phases)
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
	interrupted := r.interruptOpenSteps(ictx, r.stopReason())
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.snapshotLocked()
	if err != nil {
		if r.stopRequested && ctx.Err() != nil {
			res.Status = workflowStatusStopped
			res.Error = workflowStopError
			res.interruptedSteps = interrupted
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
	interrupted := r.interruptOpenSteps(ictx, workflowStopError)
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.snapshotLocked()
	res.Status = workflowStatusStopped
	res.Error = workflowStopError
	res.interruptedSteps = interrupted
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
	if r.isLost() {
		r.finishLost()
		return
	}
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
	// The result of a stopped run carries the failed steps but not the
	// failure log lines, so the terminal flush of a stop would write no
	// new lines. Append the lines of the steps this stop failed under mu
	// before the snapshot journals: a step that failed earlier already
	// logged its line, and a resume must not log it twice.
	if res.Status == workflowStatusStopped {
		r.mu.Lock()
		for _, s := range res.interruptedSteps {
			if s.status == stepStatusFailed && s.err != "" {
				r.appendEngineLogLocked(fmt.Sprintf("step %q failed: %s", s.title, s.err))
			}
		}
		res.Logs = append([]string(nil), r.logs...)
		r.mu.Unlock()
	}
	// finish runs on the script goroutine, so the phase state it snapshots
	// cannot change while the write starts: phase() runs on this goroutine.
	// A fenced write means another process owns the run now. A failed
	// write leaves the row running: the heartbeat keeps it alive and
	// retries the write, so a sweep cannot reopen a run that ended.
	fenced, err := r.p.workflowJournal().writeRunStatus(jctx, r.id, res.Status, res.Error, &res, r.phasesSnapshot())
	if fenced {
		r.markLost()
		r.finishLost()
		return
	}
	if err != nil {
		r.p.logger.Warn(ctx, "journal the end of a workflow run", slog.F("workflow_id", r.id), slog.Error(err))
	}

	r.mu.Lock()
	r.result = &res
	r.terminalPending = err != nil && jctx.Err() == nil
	waiting := r.awaiters > 0
	r.notified = !waiting
	r.mu.Unlock()
	// The end lands the last log burst before the run closes: the terminal
	// flush waits for an in-flight writer, and a flush scheduled after
	// close would read the result instead of the live logs.
	r.cancelLogFlush()
	r.flushLogs(true)
	close(r.done)
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
		RunID:        r.id.String(),
		Status:       workflowStatusRunning,
		Phases:       append([]string{}, r.phases...),
		CurrentPhase: r.currentPhase,
		Steps:        make([]stepSummary, 0, len(r.steps)),
		Logs:         append([]string(nil), r.logs...),
	}
	for _, s := range r.steps {
		res.Steps = append(res.Steps, stepSummary{
			ChatID: s.chatID.String(),
			Title:  s.title,
			Status: s.status,
			Reused: s.reused,
			Phase:  s.phase,
			Error:  s.err,
		})
	}
	return res
}

func (r *workflowRun) log(msg string) {
	r.mu.Lock()
	r.appendLogLocked(msg)
	r.mu.Unlock()
	r.scheduleLogFlush()
}

// appendEngineLogLocked appends a line the engine generated, not the script:
// the failure line of a step the run stopped, timed out, or failed. A
// replayed script cannot print these lines again, so they bypass the echo
// suppression and always land, or a stop or failure that races the replay
// would lose them to the count. mu must be held.
func (r *workflowRun) appendEngineLogLocked(msg string) {
	r.appendLineLocked(msg)
}

// appendLineLocked appends one line and keeps the cap. It carries no echo
// suppression, so it serves both the script path (after appendLogLocked
// spent the echo credit) and the engine path. mu must be held.
func (r *workflowRun) appendLineLocked(msg string) {
	msg = truncateLogLine(msg, maxWorkflowLogLine)
	r.logs = append(r.logs, msg)
	r.logBytes += len(msg)
	r.logSeq++
	for len(r.logs) > maxWorkflowLogs || r.logBytes > maxResultBytes {
		r.logBytes -= len(r.logs[0])
		r.logs = r.logs[1:]
	}
}

func (r *workflowRun) appendLogLocked(msg string) {
	// The replay of a restored run suppresses the first logEchoLeft
	// replayed lines, one per pre-restart line, no matter their content:
	// the baseline is the pre-restart history, and the replay prints a
	// live copy of it. A capped baseline starts mid-stream, so the
	// replayed prefix is longer than the baseline and lines do not match
	// one for one; a repeated line would otherwise consume the wrong
	// entry and duplicate or drop lines. The replay of a journaled failed
	// step re-emits its failure line (failJournaledStep), so the count
	// holds. Once the count is used up, the replay output is live again.
	if r.logEchoLeft > 0 {
		r.logEchoLeft--
		return
	}
	r.appendLineLocked(msg)
}

// workflowLogFlushInterval paces the log write-through: a script that logs
// in a loop must not issue a journal write per line. A run flushes at most
// once per interval, and once more after it ends, so a mid-run read lags the
// live lines at most by the interval.
const workflowLogFlushInterval = 2 * time.Second

// workflowLogFlushTimeout bounds one journal write of the logs. The write is
// best effort, so a stalled store holds the flush goroutine at most this
// long instead of outliving the run or the server close.
const workflowLogFlushTimeout = 5 * time.Second

// scheduleLogFlush flushes now when the last flush is older than the
// interval, and otherwise arms one flush at the end of the interval. The
// timer holds the flush, so a burst of lines lands in one write. A flush in
// flight counts as pending, so the first burst cannot launch one goroutine
// per line.
func (r *workflowRun) scheduleLogFlush() {
	if r.journalLogWrite == nil {
		return
	}
	r.mu.Lock()
	echoing := r.logEchoLeft > 0
	r.mu.Unlock()
	if echoing {
		// The replay is echoing the journal lines: the column already holds
		// them, and a write of the replay prefix would move it backward.
		// The flushes resume when the replay has echoed the baseline, and
		// the terminal snapshot always writes the final lines.
		return
	}
	r.journalMu.Lock()
	defer r.journalMu.Unlock()
	if r.logFlushTimer != nil || r.logFlushInFlight {
		return
	}
	if r.flushClosing {
		// The terminal flush of the run has started: the end lands the last
		// burst itself, and a paced flush here would write a snapshot the
		// terminal flush already carried or will carry.
		return
	}
	now := r.p.clock.Now()
	wait := r.lastLogFlush.Add(workflowLogFlushInterval).Sub(now)
	if wait <= 0 {
		// The stamp moves with the attempt, not the write: a write held up
		// by another writer (terminal flush, a stop's flush) paces the next
		// attempt to the interval instead of spinning on the taken token.
		r.lastLogFlush = now
		r.logFlushInFlight = true
		go func() {
			outcome := r.flushLogs(false)
			r.journalMu.Lock()
			r.logFlushInFlight = false
			pending := r.logFlushTimer == nil
			closing := r.flushClosing
			if outcome == flushMissedToken && !closing {
				// The token was taken: lines may still be pending, and
				// nothing else re-arms. Arm one paced timer instead of
				// re-arming immediately, which would spin on the holder.
				// A closing run re-arms nothing: its terminal flush
				// carries the lines. A nothing-pending drop re-arms
				// nothing either: the journal already holds every line.
				r.armLogFlushTimerLocked()
				pending = false
			}
			r.journalMu.Unlock()
			// Lines that landed while the write was in flight dropped their
			// own request, so a completed flush re-arms one for them. A
			// token miss is the exception: it already armed the paced
			// fallback above. rearmLogFlush re-checks the pending seq under
			// journalMu, so a nothing-pending outcome re-arms nothing and
			// this cannot loop. A closing run re-arms nothing: its terminal
			// flush already carried the burst.
			if outcome != flushMissedToken && pending && !closing {
				r.rearmLogFlush()
			}
		}()
		return
	}
	if r.logFlushTimer != nil || r.logFlushInFlight {
		return
	}
	r.armLogFlushTimerLocked()
}

// armLogFlushTimerLocked arms one paced flush at the end of the interval.
// journalMu must be held, and no flush may be pending or in flight. It is
// the fallback of an attempt that could not write: the timer flushes when
// the holder is gone, so a line cannot stay unjournaled until the next
// log() or the end of the run.
func (r *workflowRun) armLogFlushTimerLocked() {
	if r.logFlushTimer != nil || r.logFlushInFlight || r.flushClosing {
		return
	}
	wait := r.lastLogFlush.Add(workflowLogFlushInterval).Sub(r.p.clock.Now())
	if wait <= 0 {
		wait = time.Nanosecond
	}
	r.logFlushTimer = r.p.clock.AfterFunc(
		wait,
		func() {
			now := r.p.clock.Now()
			r.journalMu.Lock()
			r.logFlushTimer = nil
			// The attempt is the timer firing, not the write: the flush
			// below may write (or drop) at once, so a line that lands
			// right after the write must pace to the next interval and
			// not start a second write back to back.
			r.lastLogFlush = now
			r.journalMu.Unlock()
			outcome := r.flushLogs(false)
			if outcome == flushWrote {
				r.rearmLogFlush()
				return
			}
			if outcome == flushNothingPending {
				// The journal already holds every line: re-arming would
				// keep a timer firing every interval until the end of the
				// run for nothing.
				return
			}
			// The holder may still own the token when this fires. Arm one
			// more paced timer, or the pending lines would stay unjournaled
			// until the next log() or the end of the run. The firing above
			// already stamped the attempt, so each re-arm paces to the
			// interval and this cannot spin on the holder. A closing run
			// re-arms nothing: cancelLogFlush set the flag, and its
			// terminal flush carries the lines.
			r.journalMu.Lock()
			if !r.flushClosing {
				r.armLogFlushTimerLocked()
			}
			r.journalMu.Unlock()
		},
		"chatd",
		"workflow_log_flush",
	)
}

// cancelLogFlush drops a pending paced flush and marks the log flushes of
// the run closed. finish calls it, so the last write of the run is its own
// terminal flushLogs call. A flush already in flight cannot write again:
// the terminal flush waits for its write token and then wins the seq check
// that both flushes run under journalMu. The flag also stops the re-arm of
// a timer whose Stop() lost the race with its own firing: without it, a
// callback that fires between cancelLogFlush and the terminal flush would
// stamp and re-arm forever on an already journaled sequence.
func (r *workflowRun) cancelLogFlush() {
	r.journalMu.Lock()
	r.flushClosing = true
	if r.logFlushTimer != nil {
		r.logFlushTimer.Stop("chatd", "workflow_log_flush")
		r.logFlushTimer = nil
	}
	r.journalMu.Unlock()
}

// rearmLogFlush schedules one more flush when lines landed while a flush
// was in flight. scheduleLogFlush drops the request of such a line, so the
// completed flush must recover it, or the line would stay unjournaled until
// the next log call or the end of the run.
func (r *workflowRun) rearmLogFlush() {
	r.mu.Lock()
	seq := r.logSeq
	r.mu.Unlock()
	if seq == 0 {
		return
	}
	r.journalMu.Lock()
	pending := seq > r.journaledLogSeq
	r.journalMu.Unlock()
	if !pending {
		return
	}
	r.scheduleLogFlush()
}

// flushLogs writes the retained log lines of a live run through to the
// journal, so a mid-run read of the run detail and a restart keep the lines
// the script printed so far. It reports what kept it from writing:
// flushWrote on an attempted write (best effort: setLogs swallows its
// errors, so an attempted write counts), flushMissedToken when another
// writer holds the write token, and flushNothingPending when the journal
// already holds every line. Only a token miss re-arms the fallback: a
// nothing-pending drop would otherwise re-arm every interval until the
// end of the run. The callers take the write channel before the seq check
// and release it after the write, so a stalled write of an older snapshot
// cannot replace newer lines: the newer caller waits for the channel and
// then wins the seq check. The journal query drops a payload older than
// the stored sequence on top.
type flushOutcome int

const (
	flushWrote flushOutcome = iota
	flushMissedToken
	flushNothingPending
)

func (r *workflowRun) flushLogs(terminal bool) flushOutcome {
	if r.journalLogWrite == nil {
		return flushNothingPending
	}
	// A terminal flush must wait for an in-flight writer, or the end of
	// the run would skip its final write. A paced flush keeps its drop
	// behavior: the terminal flush of the run still carries the lines.
	r.journalMu.Lock()
	r.flushAttempts++
	r.journalMu.Unlock()
	if terminal {
		<-r.journalLogWrite
	} else {
		select {
		case <-r.journalLogWrite:
		default:
			return flushMissedToken
		}
	}
	defer func() { r.journalLogWrite <- struct{}{} }()
	r.mu.Lock()
	logs := append([]string(nil), r.logs...)
	seq := r.logSeq
	r.mu.Unlock()
	if seq == 0 {
		return flushNothingPending
	}
	r.journalMu.Lock()
	if seq <= r.journaledLogSeq {
		r.journalMu.Unlock()
		return flushNothingPending
	}
	r.journalMu.Unlock()
	// The write is best effort and must not outlive the run or the server
	// close, so a bounded context stands in for the caller's.
	ctx, cancel := context.WithTimeout(dbauthz.AsSystemRestricted(context.Background()),
		workflowLogFlushTimeout)
	defer cancel()
	r.p.workflowJournal().setLogs(ctx, r.id, int64(seq), logs)
	// journaledLogSeq advances with the attempt, not the store's answer:
	// setLogs swallows its errors, so the run has no failure signal to
	// branch on. The seq check above keeps a later flush from writing an
	// older snapshot over a newer one, and the next paced flush sends the
	// full retained tail again.
	r.journalMu.Lock()
	r.journaledLogSeq = seq
	r.journalMu.Unlock()
	return flushWrote
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
		// the await result reports it as reused. A finished row keeps its
		// status, value and finished_at; only the flag flips in the
		// journal, so the run detail reads it after the resume. An open row
		// flips the flag alone too: a report the child sent while the run
		// was interrupted can land between the rehydration and the replay,
		// and the full update would reset that row to running.
		r.mu.Lock()
		journaled.reused = true
		r.mu.Unlock()
		r.p.workflowJournal().markStepReused(ctx, journaled.id)
		r.failJournaledStep(ctx, journaled)
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
		phase:      r.currentPhase,
		occurrence: occurrence,
		ordinal:    count,
		status:     stepStatusRunning,
	}
	if err := r.p.workflowJournal().insertStep(ctx, r.id, step); err != nil {
		if database.IsUniqueViolation(err, workflowStepsPkey) {
			return r.adoptJournaledStep(ctx, id)
		}
		if errors.Is(err, errWorkflowRunLost) {
			// The new owner of the run adopts the child chat.
			r.markLost()
			return nil, err
		}
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

// adoptJournaledStep takes over the journal row of a step that another
// runner of the same run inserted after this run was rehydrated. The claim
// of a resume fences the inserts of the previous owner, but an insert that
// started before the claim committed can still land after the rehydration
// read the steps. Both runners derive the same step ID, so the later insert
// hits the primary key. The step is the
// journaled one from here on and replays the way startStep replays a
// rehydrated step. Its child chat belongs to that row and keeps running.
func (r *workflowRun) adoptJournaledStep(ctx context.Context, id uuid.UUID) (*workflowStep, error) {
	row, err := r.p.workflowJournal().getStep(ctx, id)
	if err != nil {
		return nil, xerrors.Errorf("read the workflow step another runner journaled: %w", err)
	}
	if row.RunID != r.id {
		return nil, xerrors.Errorf("journal the workflow step: step %s belongs to run %s", id, row.RunID)
	}
	step := journaledWorkflowStep(row)
	r.mu.Lock()
	r.steps = append(r.steps, step)
	r.mu.Unlock()
	r.p.workflowJournal().markStepReused(ctx, step.id)
	r.failJournaledStep(ctx, step)
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
		err := r.awaitStepChild(ctx, step.chatID, deadline)
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

// awaitStepChild waits for the step's child chat like
// awaitSubagentCompletion. That function reads the child before it
// subscribes, so a child that ends its turn in between is seen only at the
// fallback tick. This subscribes first and restarts the wait when the
// child's state changes, and the restarted wait reads the child at once.
// No wait runs past deadline, so a child that changes state often still
// returns control to waitStep, which enforces the step timeout.
func (r *workflowRun) awaitStepChild(ctx context.Context, chatID uuid.UUID, deadline time.Time) error {
	waitTimeout := func() time.Duration {
		return min(defaultSubagentWaitTimeout, deadline.Sub(r.p.clock.Now()))
	}
	changed := make(chan struct{}, 1)
	unsubscribe, err := r.p.pubsub.SubscribeWithErr(coderdpubsub.ChatStateUpdateChannel(chatID),
		func(context.Context, []byte, error) {
			select {
			case changed <- struct{}{}:
			default:
			}
		})
	if err != nil {
		timeout := waitTimeout()
		if timeout <= 0 {
			return ErrSubagentWaitTimeout
		}
		// Without a subscription awaitSubagentCompletion polls fast itself.
		_, _, err = r.p.awaitSubagentCompletion(ctx, r.parent.ID, chatID, timeout)
		return err
	}
	defer unsubscribe()
	for {
		timeout := waitTimeout()
		if timeout <= 0 {
			return ErrSubagentWaitTimeout
		}
		waitCtx, cancel := context.WithCancel(ctx)
		restart := make(chan struct{})
		go func() {
			select {
			case <-changed:
				close(restart)
				cancel()
			case <-waitCtx.Done():
			}
		}()
		_, _, err := r.p.awaitSubagentCompletion(waitCtx, r.parent.ID, chatID, timeout)
		cancel()
		select {
		case <-restart:
			// The cancel may surface as any error of the read it stopped.
			// The restarted wait reports a real error again.
			if err != nil && ctx.Err() == nil {
				continue
			}
		default:
		}
		return err
	}
}

func (r *workflowRun) failStep(ctx context.Context, step *workflowStep, reason string) {
	r.mu.Lock()
	step.status, step.err = stepStatusFailed, reason
	r.appendEngineLogLocked(fmt.Sprintf("step %q failed: %s", step.title, reason))
	r.mu.Unlock()
	// Not terminal: the run continues after a failed step, so this flush
	// must not close the log flushes. It waits for the write token like a
	// terminal flush, because the failure line must land before the step
	// row that reports the failure.
	r.flushLogs(true)
	r.p.workflowJournal().updateStep(ctx, step)
}

// failJournaledStep re-emits the failure line of a journaled failed step
// during a replay. The baseline already holds the line, so the echo counter
// suppresses this copy: the re-emit keeps the line count aligned with the
// baseline, which a plain replay that never prints the failure line would
// break. A step failed after the baseline was taken logs its line live, the
// way failStep does.
func (r *workflowRun) failJournaledStep(_ context.Context, step *workflowStep) {
	if step.status != stepStatusFailed || step.err == "" {
		return
	}
	r.mu.Lock()
	r.appendLogLocked(fmt.Sprintf("step %q failed: %s", step.title, step.err))
	r.mu.Unlock()
}

// interruptOpenSteps stops the child chats of steps that the script left
// unreported: a failed, timed-out, stopped, or returning script never reads
// their value, so they must not keep consuming model capacity until their
// step timeout. waitStep has already returned when this runs, so the step
// fields have no other writer. It returns the steps it failed, which finish
// logs: the failure lines of steps failed earlier already sit in the logs.
// A run that this process lost leaves the steps alone: their child chats
// belong to the new owner.
func (r *workflowRun) interruptOpenSteps(ctx context.Context, reason string) []*workflowStep {
	if r.isLost() {
		return nil
	}
	r.mu.Lock()
	hasOpen := slices.ContainsFunc(r.steps, func(s *workflowStep) bool {
		return s.status == stepStatusRunning || s.status == stepStatusReminded
	})
	r.mu.Unlock()
	if hasOpen && !r.confirmOwnership(ctx) {
		return nil
	}
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
	return open
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
