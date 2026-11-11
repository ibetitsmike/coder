// This file is fork code: the "@" agent menu of the fork-agent-definitions
// experiment, so ChatMessageInput.tsx, an upstream file, needs one line.

import { useLexicalComposerContext } from "@lexical/react/LexicalComposerContext";
import {
	LexicalTypeaheadMenuPlugin,
	type TriggerFn,
} from "@lexical/react/LexicalTypeaheadMenuPlugin";
import {
	COMMAND_PRIORITY_CRITICAL,
	KEY_ENTER_COMMAND,
	type TextNode,
} from "lexical";
import { useEffect, useEffectEvent, useState } from "react";
import { createPortal } from "react-dom";
import { useQueries, useQuery } from "react-query";
import { useParams } from "react-router";
import { chat } from "#/api/queries/chats";
import { agentDefinitions } from "#/api/queries/forkAgentDefinitions";
import type { ChatContext, ChatSkillSource } from "#/api/typesGenerated";
import { useForkExperiment } from "../ChatElements/tools/forkToolUtils";
import {
	type AgentMention,
	AgentMentionOption,
	ForkAgentMentionMenu,
} from "./ForkAgentMentionMenu";

// The built-in types spawn_agent always offers, named and described as in
// coderd/x/chatd/subagent_catalog.go.
const builtinAgentTypes = [
	{
		name: "general",
		description:
			"substantial delegated research, analysis, reasoning, review, planning support, and implementation",
	},
	{
		name: "explore",
		description:
			"narrow repository-local read-only code discovery and code tracing",
	},
];

// chatd reserves the built-in names, computer_use included, in every tier.
const reservedAgentTypeNames = new Set(["general", "explore", "computer_use"]);

// Only an "@" at the start of a line or after whitespace opens the menu, so
// email addresses stay plain text.
const matchAgentMention: TriggerFn = (text) => {
	const match = /(?:^|\s)@(\S*)$/.exec(text);
	if (!match) {
		return null;
	}
	return {
		leadOffset: match.index + match[0].indexOf("@"),
		matchingString: match[1],
		replaceableString: `@${match[1]}`,
	};
};

/**
 * Lists the built-in types, then each source's agents. Like chatd, custom
 * agents skip reserved names and keep the first of a name in a source, and a
 * name that more than one source defines is offered only by its
 * tier-qualified name, as is every name while a list is unknown.
 */
const resolveAgentMentions = (
	lists: readonly {
		source: ChatSkillSource;
		agents: readonly { name: string; description: string }[] | undefined;
	}[],
): AgentMention[] => {
	const hasUnknownList = lists.some((list) => !list.agents);
	const custom = lists.flatMap(({ source, agents = [] }) => {
		const byName = new Map<string, AgentMention>();
		for (const { name, description } of agents) {
			if (!reservedAgentTypeNames.has(name) && !byName.has(name)) {
				byName.set(name, { name, source, description });
			}
		}
		return [...byName.values()];
	});
	const isShared = (name: string) =>
		custom.filter((agent) => agent.name === name).length > 1;
	return [
		...builtinAgentTypes.map((type) => ({
			...type,
			source: "built-in" as const,
		})),
		...custom.map((agent) =>
			hasUnknownList || isShared(agent.name)
				? { ...agent, name: `${agent.source}/${agent.name}` }
				: agent,
		),
	];
};

/**
 * Lists the workspace agents of a chat's pinned context. They are unknown
 * when the chat endpoint omits the resources, which it does both when it
 * fails to load them and when there are none, and when a workspace is
 * selected but not pinned yet, as in a new chat.
 */
const workspaceAgents = (
	context: ChatContext | undefined,
	hasWorkspace: boolean,
) => {
	if (!context?.resources) {
		return context || hasWorkspace ? undefined : [];
	}
	return context.resources
		.filter(({ kind, status }) => kind === "subagent" && status === "ok")
		.map((resource) => ({
			name: resource.subagent_name ?? "",
			description: resource.subagent_description ?? "",
		}));
};

const handleSelect = (
	option: AgentMentionOption,
	textNode: TextNode | null,
	closeMenu: () => void,
) => {
	const { name } = option.agent;
	textNode
		?.setTextContent(
			`Delegate to the ${name} agent (spawn_agent type ${name}): `,
		)
		.selectEnd();
	closeMenu();
};

type ForkAgentMentionPluginProps = {
	organizationId?: string;
	projectId?: string;
	hasWorkspace: boolean;
	anchor: HTMLElement | null;
};

/** Offers agent types after "@" and inserts a delegation to the chosen one. */
export const ForkAgentMentionPlugin: React.FC<ForkAgentMentionPluginProps> = ({
	organizationId = "",
	projectId,
	hasWorkspace,
	anchor,
}) => {
	const isEnabled = useForkExperiment("fork-agent-definitions");
	const isNestingEnabled = useForkExperiment("fork-nested-subagents");
	const [editor] = useLexicalComposerContext();
	const [query, setQuery] = useState<string | null>(null);
	const isMenuOpen = query !== null;
	// Escape closes the menu but leaves the query, so Enter checks this.
	const [isMenuShown, setIsMenuShown] = useState(false);
	// The chat page loads the chat; the menu only reads its cached copy.
	const { agentId } = useParams();
	const currentChat = useQuery({ ...chat(agentId ?? ""), enabled: false }).data;
	const isChildChat = currentChat?.parent_chat_id !== undefined;
	// chatd gives a child spawn_agent only with fork-nested-subagents, and a
	// custom child only when its definition allows it, which the client can't
	// see (nestedSubagentTools in coderd/x/chatd/fork_subagents.go).
	const canSpawn =
		!isChildChat || (isNestingEnabled && currentChat?.agent_type === "general");
	// A child chat has no project of its own; chatd offers it the agents of
	// its root chat's project.
	const rootChatId = isChildChat ? currentChat?.root_chat_id : undefined;
	const rootChat = useQuery({
		...chat(rootChatId ?? ""),
		enabled: isMenuOpen && rootChatId !== undefined,
	});
	const chatProjectId = rootChatId ? rootChat.data?.project_id : projectId;
	const project = { source: "project", tier: "project" } as const;
	const scopes = [
		{ source: "personal", tier: "user", organizationId },
		{ source: "org", tier: "organization", organizationId },
		...(chatProjectId
			? [{ ...project, organizationId, projectId: chatProjectId }]
			: []),
	] as const;
	const storedQueries = useQueries({
		queries: scopes.map(({ source: _, ...scope }) => ({
			...agentDefinitions(scope),
			enabled: isMenuOpen && organizationId !== "",
			staleTime: 60_000,
		})),
	});
	const isRootProjectUnknown = rootChatId !== undefined && !rootChat.data;
	const mentions = resolveAgentMentions([
		...scopes.map(({ source }, index) => ({
			source,
			agents: storedQueries[index].data?.filter((agent) => agent.enabled),
		})),
		...(isRootProjectUnknown
			? [{ source: "project" as const, agents: undefined }]
			: []),
		{
			source: "workspace",
			agents: workspaceAgents(currentChat?.context, hasWorkspace),
		},
	]);
	const options = mentions
		.filter(({ name }) =>
			name
				.toLocaleLowerCase("en-US")
				.includes((query ?? "").toLocaleLowerCase("en-US")),
		)
		.map((mention) => new AgentMentionOption(mention));
	const unresolved = [
		...storedQueries,
		...(isRootProjectUnknown ? [rootChat] : []),
	].filter((result) => !result.data);
	const isLoading = unresolved.some((result) => result.isFetching);
	const hasError = [...storedQueries, ...(rootChatId ? [rootChat] : [])].some(
		(result) => result.isError,
	);

	// Lexical's menu lets Enter through when nothing is selectable, which
	// would send the unfinished mention while agents load or failed to.
	const handleEnter = useEffectEvent((event: KeyboardEvent | null) => {
		if (!isMenuShown || options.length > 0 || !(isLoading || hasError)) {
			return false;
		}
		event?.preventDefault();
		return true;
	});
	useEffect(
		() =>
			editor.registerCommand(
				KEY_ENTER_COMMAND,
				handleEnter,
				COMMAND_PRIORITY_CRITICAL,
			),
		[editor],
	);

	if (!isEnabled || !canSpawn) {
		return null;
	}

	return (
		<LexicalTypeaheadMenuPlugin<AgentMentionOption>
			options={options}
			triggerFn={matchAgentMention}
			onQueryChange={setQuery}
			onOpen={() => setIsMenuShown(true)}
			onClose={() => setIsMenuShown(false)}
			onSelectOption={handleSelect}
			commandPriority={COMMAND_PRIORITY_CRITICAL}
			menuRenderFn={(menuContainer, menu) =>
				menuContainer.current &&
				// Lexical points the editor's aria-controls at this container.
				createPortal(
					<ForkAgentMentionMenu
						anchor={anchor}
						options={options}
						selectedIndex={menu.selectedIndex}
						status={
							hasError
								? "Could not load some agents. Close and type @ again to retry."
								: isLoading
									? "Loading agents..."
									: undefined
						}
						onSelect={menu.selectOptionAndCleanUp}
						onHighlight={menu.setHighlightedIndex}
					/>,
					menuContainer.current,
				)
			}
		/>
	);
};
