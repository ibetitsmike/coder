# Subagent limits

This page is for a Coder Agents user who runs pstack skills, and shows how to plan for the cap on subagents that run at once.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

By default, at most 10 subagent turns run at once across the deployment, and a child over that cap waits for a free slot.
An Enterprise deployment doesn't have that cap while its Agent Runtime Hours entitlement is enabled and below its hard limit.
A family is a root chat and all of its descendants.
With the `fork-nested-subagents` experiment, an administrator can also limit running subagents with `--chat-max-concurrent-subagents` across the deployment and `--chat-max-concurrent-subagents-per-family` in one family.
A child over one of these limits waits for a free slot, like a child over the default cap.
While a `running` child waits for a slot, `list_agents` shows it with `waiting_for_capacity: true`, and its card marks the child **waiting for capacity**.
When you expand your root chat in the Agents sidebar, each of its waiting subagents shows an hourglass and **Waiting for a free subagent slot**.
The top bar of a waiting subagent's own chat shows the same hourglass and label, or only the hourglass on a narrow screen.
With the experiment, a child's own ancestors don't count against the default cap or these limits, so a coordinator that waits in `wait_agent` doesn't block its workers.
Because of that, more than 10 subagent turns can run at once.

With the experiment, `--chat-max-live-subagents-per-family` limits how many subagents in one family are live at once.
A subagent is live while it isn't archived and its status is `running`, `interrupting`, `requires_action`, or `paused`.
A child that finished doesn't count.
At the limit, `spawn_agent` fails with `cannot create a child agent: this chat family has` and the count, instead of queueing the child.
With the experiment, the card of a failed `spawn_agent` call shows its error.
While the family is at the limit, the Agents sidebar shows a chip such as **live 2/2** next to the title of the expanded root chat.
For a root chat, `GET /api/v2/chats/fork/{chat}/family` returns the family's `live_subagents`, its `max_live_subagents` limit, where 0 means no limit, and the IDs of its subagents that wait for a slot in `waiting_for_capacity`.
For a child chat, it fails with status 400.
`message_agent` to a finished child fails the same way with `cannot resume the child agent:`.
After either error, call `list_agents`, and follow `has_more` with `offset` until you have every direct child.
If no child is live, retry one time without waiting.
If a child is `running`, `interrupting`, or `paused`, call `wait_agent` on it, and retry when it finishes.
A `paused` child waits for its owner to finish editing a queued message, so keep waiting on it unless the user says otherwise.
Otherwise, or if that retry fails, `requires_action` children or your children's own workers hold the limit.
`wait_agent` returns at once for a `requires_action` child, and `list_agents` shows your children's workers only with `recursive: true`.
In that case, call `interrupt_agent` on each child whose remaining work you don't need.
Whatever the child's status, `interrupt_agent` interrupts its `running`, `interrupting`, and `requires_action` descendants, and the child itself when it has a turn.
An interrupted `requires_action` child runs again before it stops, so call `wait_agent` on it before you retry.
Interrupted descendants count until they stop, and you can't wait on them, so if the retry fails, run `sleep 30` with `execute` and retry.
Tell the user that the family is at its live limit only after several retries fail.

These three flags default to 0, which means no limit.
An organization's `max_concurrent`, `max_concurrent_per_family`, and `max_live_per_family` subagent limits apply too, and the lower of the deployment and organization values wins.
An organization admin or auditor can read the flag values and the lower values in the `deployment` and `effective` fields of `GET /api/v2/chats/fork/organizations/{organization}/subagent-limits`.
Neither field includes the default cap of 10 subagent turns.
An organization admin can also change the organization values in the **Subagent limits** section of **AI Settings** > **Coder Agents**, and an auditor can view them there.
