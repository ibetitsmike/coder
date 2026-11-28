# Workflows

This page is for a Coder Agents user who runs pstack skills, and shows how to run a saved workflow from a chat.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

The workflow tools exist only when the deployment enables the `fork-workflows` experiment, and only in a root chat that is not in plan mode and is not an `explore` chat.
A root chat then has `run_workflow`, `await_workflow`, and `list_workflows`, and every chat owner gets them.
A chat whose client supplies a tool with one of those names keeps the workflow tools, and the client's tool of that name is dropped.

`run_workflow` starts a run and returns at once with a `run_id` and status `"running"`.
Send the script inline as `script`, or write it to a file and send its absolute path as `script_path`.
The server reads the file one time, when the run starts.
Send `args` as a JSON object, and `name` to run a saved workflow by name instead of a script; `name` can take `args` too, and they must match the workflow's args schema.
The tool runs after the other tool calls of the same step, so a `write_file` that changed `script_path` in that step is done first.

The script must define `main(args)`, which receives `args` and returns a JSON value.
The builtins are `agent(prompt, type="general", schema=None, title="")`, which starts one step, waits for it, and returns its value or fails the script when the step fails, `spawn(...)` and `wait(handles)`, which start steps and collect handles, `log(message)` and `print()`, `json.encode` and `json.decode`, and `phase(name)`.
`type` is `"general"` or `"explore"`.
A step is a child chat of this chat that also gets the `workflow_report` tool, and its value must match `schema`, a JSON Schema dict with `"type": "object"` at the top level in draft-07 or draft 2020-12, with no composition keywords such as `allOf` or `oneOf`, no `additionalProperties: false`, and local `$refs` that inline without recursion.
Without a schema, the value is `{"text": "..."}`.
A step's status is `running`, `reminded`, `reported`, or `failed`, and `reminded` means it got its one retry reminder.

The script has no `load()`, no clock, no file access, and no network.
`while` loops are allowed, and recursion is not.
The limits are: script 64&nbsp;KiB, args 64&nbsp;KiB, 50 steps per run with reused ones included, where a further step fails the script, 10,000,000 Starlark execution steps, a result of 64&nbsp;KiB, a step value or report of 256&nbsp;KiB, 32 phases with names of 128&nbsp;bytes at most, a wait on one step of 1&nbsp;hour at most from when the script waits on it, and log lines in a result of 50 at most, one cut to 4&nbsp;KiB, and 64&nbsp;KiB together with the oldest dropped first.
An optional top-level `PHASES = ["plan", "build", "review"]` list names the phases before the first step, and names first set by `phase()` are appended behind the declared ones.
A `phase` call past the 32-phase cap fails the run.

`await_workflow` takes the `run_id` and waits until the run ends, or until `wait_seconds` passes, 600&nbsp;seconds by default and 780&nbsp;seconds at most, with `wait_seconds: 0` returning the status at once.
On a server replica that does not own the run, the call answers the run's saved state at once instead of waiting, so retry the call when the status is still `running`.
The result holds `status` (`running`, `completed`, `failed`, `interrupted`, or `stopped`), `result`, `error`, `phases`, `current_phase`, `steps`, and the last 50 log lines.
When a run ends and no `await_workflow` waits for it, the chat gets a message that names the run and its status and asks for `await_workflow`.
When the chat was busy, the message sat queued, and an `await_workflow` call that delivers the result deletes it.
When the chat was idle, the message starts a turn and stays in the transcript.

An `interrupted` status means the server restarted during the run.
Call `run_workflow` again with `resume_run_id` set to the run's ID, and the `script` and `args` can be omitted, because the server replays them from its journal, and changes to the script don't apply to a resumed run.
Finished steps are reused and don't run again.
A run that the server replica lost answers the next `await_workflow` call for it with a message that says the same thing.
A user who can update the chat can stop a running run through the API or the run card, which fails the open steps with `stopped by user`, and an already ended run answers status 409.

Saved workflows are workflows that users store in Coder, and a chat runs one with `run_workflow` and `name`.
The chat reads them as its owner.
A user has 100 personal workflows in an organization at most, and an organization has 100 at most.
A personal workflow hides the organization one of the same name, and a name can only be lowercase letters and digits in dash-separated words.
`list_workflows` lists them.
Without `name`, you get a page of entries with `name`, `tier` (`personal` or `organization`), a short description cut to 300&nbsp;bytes, and `has_args_schema`, with `next_offset` to send as `offset`.
With `name`, you get the full description, args schema, and phases, and a `note` when some of it does not fit.
The tool only reads, so never claim to have saved or edited one.
If the workflow has an args schema, `args` must match it or the run doesn't start, and missing args validate as an empty object.

Without the experiment, a `run_workflow` call fails with `Tool not active in this turn: run_workflow`.
Then spawn workers with `spawn_agent` and collect them with `wait_agent`, as the Tasks row says.
