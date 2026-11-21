# Workflows on Coder Agents

This page documents the workflow runs of this fork's `main`.
It is for a user who runs Coder Agents chats and wants a chat to fan work out to sub-agent steps and combine their results.
It is also for an organization admin who manages saved workflows.

Workflows exist only with the `fork-workflows` experiment.
Start the server with `--experiments=fork-workflows` or set `CODER_EXPERIMENTS` to include `fork-workflows`.
A fork experiment is not safe for production use and can change or be removed at any time.
This page is fork-only: it isn't in `docs/manifest.json`.

A workflow run is one call to the `run_workflow` tool.
The call starts a Starlark script, a Python dialect, that runs sub-agent steps and combines their results.
The script runs in the background, and the chat reads its result with `await_workflow`.
The server keeps the run in its database, so a run survives a restart and can resume.

## Who can run a workflow

A root chat has `run_workflow`, `await_workflow`, and `list_workflows` when the experiment is on.
Every chat owner gets them.
Plan-mode turns, `explore` chats, and child chats don't get them.
A chat whose client supplies a tool with one of those names keeps the workflow tools, and the client's tool of that name is dropped.

A run belongs to its chat.
Whoever can read the chat can read its runs with the API and in the web UI.
Stopping a run changes the chat, so only a user who can update the chat can stop one.

## Write a script

Send the script inline as `script`, or write it to a file in the workspace and send its absolute path as `script_path`.
Send one of the two.
The server reads the file one time, when the run starts, so a later edit of the file doesn't change a run that has started.
A file makes it easy to fix a script with small edits and start it again.

The script must define `main(args)`.
`args` is the JSON object the call sends, and `main` must return a JSON value: a dict, a list, a string, a number, a bool, or `None`.

These builtins exist inside the script:

- `agent(prompt, type="general", schema=None, title="")` starts one step, waits for it, and returns its value.
  A failed step fails the script.
- `spawn(prompt, type="general", schema=None, title="")` starts one step and returns a handle at once.
- `wait(handles)` waits for one handle or a list of handles, and returns the value or a list of values in input order.
  A failed step gives `None`.
- `log(message)` adds a line to the run log, and `print()` does the same.
- `json.encode(value)` and `json.decode(text)`.
- `phase(name)` sets the current phase of the run.
  Steps that start after the call carry that phase.
  A name the script never declared is accepted and shown as an observed phase.
  A `phase` call past the phase cap fails the run.

`type` is `"general"` or `"explore"`.
`schema` is a JSON Schema dict with `"type": "object"` at the top level, in draft-07 or draft 2020-12.
The step must call its `workflow_report` tool with a value that matches the schema, and that value is the step value.
Without a schema the value is `{"text": "..."}`.

Each step is a child chat of the chat that runs the workflow.
A step gets the `workflow_report` tool, the step prompt, and the workspace tools of its type.
The run can create 50 steps at most, reused ones included, and a further step fails the script.
One step can run 1&nbsp;hour at most, and a longer step fails.

A top-level `PHASES = ["plan", "build", "review"]` list is optional.
The server reads it before `main` runs and shows its names as pending before the first step.
Names first set by `phase()` are appended behind the declared ones.
A run has 32 phases at most, and one name is 128&nbsp;bytes at most.

The script has no `load()`, no clock, no file access, and no network.
`while` loops are allowed, and recursion is not.

These limits bound one call:

| Input or output           | Limit                     |
|---------------------------|---------------------------|
| `script`                  | 64&nbsp;KiB               |
| `args`, inline or by name | 64&nbsp;KiB               |
| Steps per run             | 50, reused ones included  |
| Starlark execution steps  | 10,000,000                |
| What `main` returns       | 64&nbsp;KiB               |
| One step value or report  | 256&nbsp;KiB              |
| Phases per run            | 32                        |
| One phase name            | 128&nbsp;bytes            |
| One step                  | 1&nbsp;hour               |
| Log lines in a result     | 50, one cut to 4&nbsp;KiB |

## Run and await a workflow

`run_workflow` returns at once with a `run_id` and status `"running"`.
Besides `script` or `script_path` it takes:

- `args`, a JSON object that `main(args)` receives.
- `resume_run_id`, to continue an interrupted run.
- `name`, to run a saved workflow by name instead of a script.

The tool runs after the other tool calls of the same step, so a `write_file` that changed `script_path` in that step is done first.

`await_workflow` takes the `run_id` and waits until the run ends, or until `wait_seconds` passes.
The default is 600&nbsp;seconds, and 780&nbsp;seconds is the most.
`wait_seconds: 0` returns the status at once.
A timeout returns status `"running"`, so call the tool again.

The result holds `status`, `result`, `error`, `phases`, `current_phase`, `steps`, and the last 50 log lines.
A status is `running`, `completed`, `failed`, `interrupted`, or `stopped`.
A step's status is `running`, `reminded`, `reported`, or `failed`.
`reminded` means the step got its one retry reminder and is still in flight.

When a run ends and no `await_workflow` waits for it, the chat gets a message that names the run and its status and asks for `await_workflow`.
The message goes away once an `await_workflow` call has delivered the result.

## Stop, interrupt, and resume

`POST /api/v2/chats/fork/{chat}/workflow-runs/{workflowrun}/stop` stops a running run.
The script unwinds, the open steps fail with `stopped by user`, their child chats stop, and the run reads `stopped`.
A run that already ended answers status 409.
An interrupted run is not stoppable, because the agent continues it with `resume_run_id`.
In the web UI, the run card and the Workflows panel have a **Stop** control that calls the same endpoint.

`interrupted` means the server restarted during the run.
Call `run_workflow` with `resume_run_id` set to the run's ID to continue it.
The `script` and `args` can be left out, because the server replays them from its journal, and changes to the script don't apply to a resumed run.
A finished step does not run again, and the web UI marks it **Reused**.

A call can also fail with a message that says the run is not found on this server replica.
The message says to call `run_workflow` again with the same script, the same args, and `resume_run_id` set to the run ID.

## Saved workflows

A saved workflow is a workflow a user stored with a name, so chats can run it by name.
A user saves and edits workflows in Coder, and an agent only reads them: no chat tool saves one.
Saving a workflow from the run card of a run is not built yet.

A workflow belongs to one organization and holds one tier:

- `personal`: it belongs to one user in that organization, and only that user's chats can run it.
- `organization`: the organization owns it, and the members its permissions allow read and run it.

A name is lowercase letters and digits in dash-separated words, such as `review-docs`, and it is 128&nbsp;bytes at most.
A personal workflow and an organization workflow can hold the same name.
A personal workflow hides the organization one of the same name, in `list_workflows` and when a chat runs one by name.

A saved workflow holds `description`, `script`, an optional `args_schema`, and an optional `phases` list.
The editor fields have these limits:

| Field         | Limit                                                     |
|---------------|-----------------------------------------------------------|
| `name`        | 128&nbsp;bytes, lowercase letters and digits with dashes  |
| `description` | 4096&nbsp;bytes                                           |
| `script`      | 64&nbsp;KiB, and it must define `main(args)`              |
| `args_schema` | 12&nbsp;KiB as compact JSON, nested 64 levels at most     |
| `phases`      | 32 names, one per line in the editor, each 128&nbsp;bytes |

The server checks the script as a workflow script and the args schema as JSON Schema, so an invalid script or schema is refused.
An organization has 100 saved workflows at most, and one user has 100 personal workflows in an organization at most.

### Run a saved workflow

`run_workflow` with `name` and `args` runs one.
The chat runs it as the chat's owner, so a personal workflow of another user and an organization workflow the owner can't read are both invisible.
If the workflow has an args schema, `args` must match it or the run doesn't start.
Missing args validate as an empty object.

`list_workflows` lists the saved workflows the chat can run.
Without `name`, it returns a page of workflows, each with its `name`, `tier`, a short description cut to 300&nbsp;bytes, and `has_args_schema`.
Send the `next_offset` of the previous page as `offset` for the next page.
With `name`, it returns that workflow's full description, args schema, and phases, and a note when some of it does not fit.
Agents cannot save or edit a workflow with this tool.

### Save and edit a workflow

A user creates and edits workflows in the web UI.
In **Settings** under Agents, the **Workflows** page lists your personal workflows and the organization workflows you can read.
**New workflow** opens an editor dialog with **Name**, **Description**, **Script**, **Args schema**, and **Phases** fields, where **Phases** holds one name per line.
**Create workflow** in the dialog stores it, and **Delete…** in a row's menu removes it.
At the limit of 100, the create button stays but is inert and explains the limit.
Selecting a workflow's name opens a read-only view of its script, args schema, and phases.

An organization admin manages the organization's workflows in **AI Settings** > **Workflows**.
The page shows the personal tier only for a member of the selected organization.
What a user can create, edit, and delete in the organization tier follows the organization's skill permissions.
A member who can share a workflow selects **Manage permissions** in its row and changes who reads it.

The editor protects against overwriting a newer version.
It records the workflow ID it loaded, and the save is rejected when the stored workflow has another ID.
The editor then blocks the save and asks you to copy your changes, then close and reopen it.
The save answers status 409 when the name holds a workflow the editor didn't load, and status 404 when the name holds nothing you can see, so a name you can't read never proves to exist.

### Share a personal workflow

**Share with organization…** in a personal workflow's row moves it to the organization tier under the same name.
Only the owner can share, and the owner keeps the right to edit and delete it.
An organization admin can change who has access.
Every member of the organization can read and run it after the share.
When the organization already has a workflow with that name, the move is refused with status 409 and asks you to rename your workflow.

## Web UI

While the experiment is on, a root chat with at least one run shows a **Workflows** tab in the right panel of the chat page.
The tab lists the chat's runs, newest first, with their status, script preview, age, duration, and step progress.
A run that is still running refreshes on the watch stream and at least once a minute.

Selecting a run opens its detail with its error, args, steps grouped by phase, a phase rail when the run has no steps, the result, and the logs.
Logs of a running run arrive live.
**Stop** stops a running run, and **Resume run** fills the composer with `Resume workflow run <id>` for you to send.
**Resume run** shows only for a user who can update the chat, and not in plan mode.
On an interrupted run you can't update, the panel says the server restarted during the run and that you resume it by asking the agent.

The conversation shows each `run_workflow` and `await_workflow` call as a card.
Its title follows the state, such as **Starting workflow…**, **Started workflow**, **Workflow still running**, **Workflow completed**, **Workflow failed**, **Workflow stopped**, or **Workflow interrupted**.
The card shows the run ID, the error, the steps with links to their chats and a **Reused** badge on reused ones, the phase rail, the result, the logs, and the script with its args.
**Stop** stops a running run, and shows on the card only while the run is running and you can update the chat.
The card of a run that ended between the confirm and the stop request answers with a notice that the run already ended.

The composer's `@` menu lists the saved workflows you can run next to the agent types.
It opens after `@` at the start of a line or after whitespace.
Each workflow row shows `@name`, its tier as `personal` or `org`, its description, and a summary of its args with required ones marked.
A workflow whose name an agent mention or a reserved built-in type, such as `general`, already takes is left out.
Selecting a row inserts `Run the <name> saved workflow (run_workflow name <name>)` with the args summary, and you send it as the message.
The menu offers workflows only in a root chat that is not in plan mode and not in explore mode.

## API

These endpoints need the experiment.
`{chat}` and `{workflowrun}` are IDs.
`{organization}` is an ID or a name, `{user}` is an ID, a username, or `me`, and `{workflow}` is the workflow's name.

| Endpoint                                                                            | What it does                                        |
|-------------------------------------------------------------------------------------|-----------------------------------------------------|
| `GET /api/v2/chats/fork/{chat}/workflow-runs`                                       | List a chat's runs, newest first                    |
| `GET /api/v2/chats/fork/{chat}/workflow-runs/{workflowrun}`                         | Read one run with its logs, phases, and steps       |
| `POST /api/v2/chats/fork/{chat}/workflow-runs/{workflowrun}/stop`                   | Stop a running run                                  |
| `GET /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows`      | List a user's personal workflows in an organization |
| `POST /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows`     | Save a personal workflow                            |
| `GET`, `PATCH`, `DELETE` on `.../members/{user}/workflows/{workflow}`               | Read, update, or delete one personal workflow       |
| `POST .../members/{user}/workflows/{workflow}/share`                                | Move a personal workflow to the organization tier   |
| `GET /api/v2/chats/fork/organizations/{organization}/workflows`                     | List the organization's workflows                   |
| `POST /api/v2/chats/fork/organizations/{organization}/workflows`                    | Save an organization workflow                       |
| `GET`, `PATCH`, `DELETE` on `.../organizations/{organization}/workflows/{workflow}` | Read, update, or delete one organization workflow   |
| `GET`, `PATCH` on `.../organizations/{organization}/workflows/{workflow}/acl`       | Read or change who reads an organization workflow   |
| `GET .../organizations/{organization}/workflows/{workflow}/acl/available`           | List the members and groups you can grant           |

The run list takes `limit`, at most 100 with a default of 50, and an `after` cursor of `<created_at>/<run ID>` for the next page.
A list item keeps a preview of the script of 200&nbsp;bytes instead of the script.
A `PATCH` of a workflow takes `expect_id` as a query parameter: the write goes through only when the stored workflow still has that ID, otherwise it answers 409.

## Not built yet

The run card has no **Save workflow** action, and no chat tool saves a workflow.
That action is planned in ibetitsmike/coder#326, which is still open.

## Evidence

Checked against fork `main` at `5c8c0e2c7a`:

- Tools and their gate: `run_workflow`, `await_workflow`, and `list_workflows` for root chats only, not in plan mode and not `explore`, `coderd/x/chatd/fork_workflow.go:174-197`, `:262`, wired at `coderd/x/chatd/fork_tools.go:90`.
- Script rules and limits: `coderd/x/chatd/fork_workflow.go:37-55`, the tool description, `:60-116`, the result, `:145-160`, `await` defaults, `:126-140`, the step cap, `:1235`, the step timeout, `:1356-1365`, and the log caps, `:46-47`, applied at `:930-936`, with the line cut in `truncateLogLine`, `:1207`.
- Builtins and phases: `coderd/x/chatd/fork_workflow_starlark.go:30`, `:35-38`, `:272-277`, `:309`, `:330`, `:347`, `:413-424`, `:504-511`, and `json` from the Starlark json module.
- Step children, the default schema, and the step prompt: `coderd/x/chatd/fork_workflow_step.go:27`, `:30-32`, `:52-70`, `:168-207`, the deterministic step IDs, `:34-42`, and the schema rules, `coderd/x/chatd/fork_workflow_report.go:313-336`.
- The report tool and its 256&nbsp;KiB limit: `coderd/x/chatd/fork_workflow_report.go:35-47`, `:110-128`, the one reminder, `:43` with the send in `coderd/x/chatd/fork_workflow.go:1404-1414`.
- Resume from the journal and the not-found help: the tool description, `coderd/x/chatd/fork_workflow.go:100-116`, and the replay, `fork_workflow_journal.go:656`.
- The end message and its removal after an await: `coderd/x/chatd/fork_workflow.go:780-816`, `:840-870`.
- Saved workflow rules and limits: `coderd/fork_saved_workflows.go:35-55`, `coderd/x/chatd/fork_saved_workflows.go:51`, `:64-78`, `:240-277`, and the table checks, `coderd/database/migrations/chatdmig/nodes/01a1db0e-3208-7130-8ea1-b956b53f0f6f_saved_workflows.up.sql`, with the limit of 100, `:42-79`.
- `list_workflows` and the name hiding: `coderd/x/chatd/fork_saved_workflows.go:28-49`, `:80-92`, `:142-205`, `:206-237`, read as the chat owner, `:95-113`.
- Routes: run routes, `coderd/fork_chat_routes.go:179-185`, saved workflow routes, `:187-212`, the stop authorization and errors, `coderd/fork_workflow_runs.go:182-215`, the run list limits and the script preview, `:28-33`, `:58-61`, `:290-298`, and the chat read gate, `coderd/httpmw/chatparam.go:26-63`.
- Types and SDK: `codersdk/fork_workflow_runs.go:13-95`, `:164-175`, `codersdk/fork_saved_workflows.go:14-80`, `:69`, and `site/src/api/forkSavedWorkflows.ts:78-93`.
- Share: the owner check, the conflict, and the move, `coderd/fork_saved_workflows_share.go:43-49`, `:98-104`, and the Everyone read grant, `coderd/database/forkdb/queries/saved_workflows.sql:40-59`.
- The editor's revision check: `site/src/pages/AISettingsPage/ForkWorkflowsPage/useForkSavedWorkflowEditor.ts:236-266`, with the block and its message, `:205-266`.
- Member page and nav: `site/src/pages/AgentsPage/AgentSettingsPage/ForkSavedWorkflowsPage/ForkSavedWorkflowsPage.tsx`, `site/src/forkRoutes.tsx:49`, and `site/src/pages/AgentsPage/components/ChatsSidebar/settings/ForkSettingsNavItems.tsx:38-43`.
- Admin page and permissions: `site/src/pages/AISettingsPage/ForkWorkflowsPage/ForkWorkflowsPage.tsx:24-66`, `:111-121`, and `site/src/forkRoutes.tsx:61`.
- Table and editor copy, the limit tooltip, and the delete dialog: `site/src/pages/AISettingsPage/ForkWorkflowsPage/ForkSavedWorkflowsTable.tsx:39-56`, `ForkSavedWorkflowsTableView.tsx:127-156`, `:329`, and the field limits, `savedWorkflowForm.ts:13-18`, `:73-90`.
- Workflows tab and panel: `site/src/pages/AgentsPage/AgentChatPage/AgentChatPageView.tsx:833-836`, `site/src/pages/AgentsPage/components/RightPanel/ForkWorkflowsPanel/useForkWorkflowsTab.ts`, the 60&nbsp;second refresh, `site/src/api/queries/forkPanelResync.ts:9`, `site/src/api/queries/forkWorkflows.ts:120-129`, and the interrupted note, `ForkWorkflowRunDetail.tsx:174`.
- Run card labels, stop, resume, and the reused note: `site/src/pages/AgentsPage/components/ChatElements/tools/ForkWorkflowTool.tsx:41-58`, `:237`, `site/src/pages/AgentsPage/components/ForkWorkflows/ForkWorkflowResumeButton.tsx`, `forkWorkflowResume.ts:7`, and the reused badge, `ForkWorkflowSteps.tsx:92`.
- The `@` menu: the trigger, `site/src/pages/AgentsPage/components/ChatMessageInput/ForkAgentMentionPlugin.tsx:58-68`, the gates, `:283-309`, the args summary, `:139-168`, the dedupe, `:170-199`, `:389-400`, the insertion, `:247-255`, and the menu group, `ForkAgentMentionMenu.tsx:189-191`.
- The experiment: `codersdk/fork_experiments.go:20`, `:58-61`, and not in the safe list, `codersdk/deployment.go:5638-5646`.
- No **Save workflow** on the run card: `site/src/pages/AgentsPage/components/ChatElements/tools/ForkWorkflowTool.tsx` has no save action, and ibetitsmike/coder#326 is open.
