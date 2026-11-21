# Saved workflow administration

This page is for an organization administrator who manages saved workflows for their members in Coder Agents.
It covers the editor, the permissions, and the sharing of workflows.
For running workflows in a chat, refer to [Workflows on Coder Agents](./workflows.md).

Saved workflows exist only with the `fork-workflows` experiment.
Start the server with `--experiments=fork-workflows` or set `CODER_EXPERIMENTS` to include `fork-workflows`.
A fork experiment is not safe for production use and can change or be removed at any time.

## Limits

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

## Edit a workflow

A user creates and edits workflows in the web UI.
In **Settings** under Agents, the **Workflows** page lists your personal workflows and the organization workflows you can read.
**New workflow** opens an editor dialog with **Name**, **Description**, **Script**, **Args schema**, and **Phases** fields, where **Phases** holds one name per line.
**Create workflow** in the dialog stores it, and **Delete…** in a row's menu removes it.
At the limit of 100, the create button stays but is inert and explains the limit.
Selecting a workflow's name opens a read-only view of its script, args schema, and phases.

The editor protects against overwriting a newer version.
It records the workflow ID it loaded, and the save is rejected when the stored workflow has another ID.
The editor then blocks the save and asks you to copy your changes, then close and reopen it.
The save answers status 409 when the name holds a workflow the editor didn't load, and status 404 when the name holds nothing you can see, so a name you can't read never proves to exist.

## Manage the organization's workflows

You manage your organization's workflows in **AI Settings** > **Workflows**.
The page shows the personal tier only for a member of the selected organization.
What you can create, edit, and delete in the organization tier follows the organization's skill permissions.
If you can share a workflow, select **Manage permissions** in its row and change who reads it.

## Share a personal workflow

**Share with organization…** in a personal workflow's row moves it to the organization tier under the same name.
Only the owner can share.
The share starts the owner with read, update, and delete, and every member of the organization with read and run.
These are the initial grants, not permanent rights: an organization admin can change them with **Manage permissions**, and can take the owner's edit and delete away or stop members from reading and running it.
When the organization already has a workflow with that name, the move is refused with status 409 and asks you to rename your workflow.
