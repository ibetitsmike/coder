# Pinned skills

This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

With the `fork-pinned-skills` experiment, the chat owner can pin up to 8 skills to a chat, including skills hidden from the model.
To pin one, select the **Pinned skills** pill in the composer of an open chat, then select the skill.
The API takes the whole list with `PUT /api/v2/chats/fork/{chat}/pinned-skills`.
No chat tool pins or unpins a skill, and a child chat doesn't get its parent's pins.

Coder Agents puts the pins in the prompt as a model-only message that starts with `The user pinned these skills to this chat.`, with each pinned skill's body in a block such as `<pinned-skill name="workspace/poteto-mode">`.
Coder Agents sends a new message whenever the prompt no longer shows the current pins, such as after a compaction or a change to the pins or their bodies.
The newest message replaces any earlier list, and when no pinned skill is left to show, its list is `(none)`.
A pin that the owner can't use, such as a skill that was deleted, turned off, or unshared, is left out.
The bodies share 64&nbsp;KiB.
A skill that doesn't fit is only named after `Not shown because the pinned skills exceed the size limit:`.
Without the experiment, there are no pinned skills.

When a newer pinned skills message drops `poteto-mode`, the mode is off.
If the user wants `poteto-mode` to stay on, tell them that with the experiment they can pin it in the **Pinned skills** pill.
