# Install pstack on Coder Agents

This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

1. If `~/pstack-src` exists from an earlier setup, update it: `git -C ~/pstack-src fetch origin`.
   The fetch exits without an error.
2. Otherwise, clone pstack in the workspace: `git clone https://github.com/cursor/plugins ~/pstack-src`.
   The clone creates `~/pstack-src/pstack/`.
3. Pin it: `git -C ~/pstack-src checkout c742dd61706fbacc1b95f1a20b7b988cf096ce6c`.
   `git -C ~/pstack-src rev-parse HEAD` then prints that commit.
   `<pstack>` is `~/pstack-src/pstack`.
4. Check for name clashes.
   Move any matching name out of `~/.coder/skills` or `~/.coder/agents` first.
5. Copy the skills that pstack calls.

   Run `mkdir -p ~/.coder/skills && cp -R ~/pstack-src/pstack/skills/. ~/pstack-src/cursor-team-kit/skills/deslop ~/pstack-src/cursor-team-kit/skills/control-ui ~/pstack-src/cursor-team-kit/skills/control-cli ~/.coder/skills/`.

   `ls ~/.coder/skills/*/SKILL.md | wc -l` then prints at least 61.

   The workspace agent reads `~/.coder/skills/<name>/SKILL.md`.

6. Copy pstack's agents into `~/.coder/agents`.

   Run `cp ~/pstack-src/pstack/agents/*.md ~/.coder/agents/`.

   `ls ~/.coder/agents/*.md | wc -l` then prints at least 2.

   With the `fork-agent-definitions` experiment, chats also get them as [custom agent types](custom-agent-types.md).
7. Download the adapter page and the pages it links: `rm -rf ~/.coder/pstack && mkdir -p ~/.coder/pstack && curl -fsSL -o ~/.coder/pstack-adapter.md https://raw.githubusercontent.com/ibetitsmike/coder/main/docs/fork/pstack-adapter.md && for p in attachments chat-history chat-transcripts context-windows custom-agent-tool-policy custom-agent-types family-messages install nested-subagents notes notifications pinned-skills questions setup-pstack subagent-limits todo-list view-images wake-ups workflows workspace-isolation worktree-isolation; do curl -fsSL -o ~/.coder/pstack/$p.md https://raw.githubusercontent.com/ibetitsmike/coder/main/docs/fork/pstack/$p.md || break; done`.
   `head -1 ~/.coder/pstack-adapter.md` then prints `# pstack on Coder Agents`, and `ls ~/.coder/pstack/*.md | wc -l` prints 21.
8. If the downloaded page pins another pstack commit than step 3, start over.
9. If `~/.coder/AGENTS.md` already holds a block between `<!-- pstack-adapter -->` and `<!-- /pstack-adapter -->`, delete that block.
10. Append the [AGENTS.md block](../pstack-adapter.md#agentsmd-block) to `~/.coder/AGENTS.md`:
    `sed -n '/^<!-- pstack-adapter -->$/,/^<!-- \/pstack-adapter -->$/p' ~/.coder/pstack-adapter.md >> ~/.coder/AGENTS.md`.
    `grep -c 'pstack-adapter -->' ~/.coder/AGENTS.md` then prints 2.

11. Start a new chat.

    A chat pins `AGENTS.md` and the skills when it starts.

    A chat that was already open keeps its old copy until you select **Refresh context** in it.

12. Ask the chat to quote the `pstack-adapter` block without any tool call.

    The chat replies with the block's lines.

    This message is also the plain first message the chat needs, because a chat's first message can't run a skill.
13. If the chat can't quote the block, switch to a model server that keeps every system message.

    Coder Agents sends `AGENTS.md` as its own system message, after the base system prompt.

    A model server that keeps only the first system message drops the block without an error.

    Ollama's `qwen3-coder` renderer (Ollama 0.34.4) does this, but the oMLX GLM-5.3-Flash chat template keeps every system message.
14. If you switched model servers, start again at step 11.
15. Select the skill in the composer's `/` menu, which inserts `/workspace/<name>`.
    A bare `/<name>` loads nothing.

The workspace keeps the copies from steps 5 to 10, so a change to the adapter page on fork `main` doesn't reach it.
When the adapter page changes, repeat steps 1 to 10, then start a new chat or select **Refresh context** in an open chat.
Steps 5 and 6 only add and replace files, so before step 3 of a repeat, while `~/pstack-src` is still at the earlier commit, delete the earlier copies: `cd ~/pstack-src/pstack && for n in skills/* agents/*.md; do rm -rf ~/.coder/"$n"; done && rm -rf ~/.coder/skills/deslop ~/.coder/skills/control-ui ~/.coder/skills/control-cli`.
Otherwise a skill or agent that the new pstack commit removes or renames stays in the workspace.

`execute` runs each command with `sh -c` in the workspace agent's environment, so it doesn't read `~/.bashrc` or `~/.profile`.
If a tool is on `PATH` only through one of those files, set `PATH` in the `env` of the template's `coder_agent`, such as `"$HOME/.local/bin:$PATH"`.
For a tool from `~/.profile`, you can instead start the command with `. ~/.profile &&`.
Use `.` and not `source`, because `source` fails in shells such as Dash, the `/bin/sh` of Debian and Ubuntu.
