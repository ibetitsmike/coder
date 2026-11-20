// This file is fork code: the "@" agent and workflow menu of the
// fork-agent-definitions and fork-workflows experiments, so
// ChatMessageInput.tsx, an upstream file, needs one line.

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
import type { ForkSavedWorkflowSchema } from "#/api/forkSavedWorkflowSchema";
import { chat } from "#/api/queries/chats";
import { agentDefinitions } from "#/api/queries/forkAgentDefinitions";
import { savedWorkflows } from "#/api/queries/forkSavedWorkflows";
import type {
	ChatContext,
	ChatSkillSource,
	ForkSavedWorkflowMetadata,
} from "#/api/typesGenerated";
import { asRecord } from "../ChatElements/runtimeTypeUtils";
import { useForkExperiment } from "../ChatElements/tools/forkToolUtils";
import {
	type AgentMention,
	AgentMentionOption,
	ForkAgentMentionMenu,
	type ForkMentionOption,
	type WorkflowMention,
	WorkflowMentionOption,
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

type ResolvedAgentMention = AgentMention & { qualifiedName: string };

/**
 * Lists the built-in types, then each source's agents. Like chatd, custom
 * agents skip reserved names and keep the first of a name in a source, and a
 * name that more than one source defines is offered only by its
 * tier-qualified name, as is every name while a list is unknown. chatd
 * accepts the qualified name of a custom agent either way.
 */
const resolveAgentMentions = (
	lists: readonly {
		source: ChatSkillSource;
		agents: readonly { name: string; description: string }[] | undefined;
	}[],
): ResolvedAgentMention[] => {
	const hasUnknownList = lists.some((list) => !list.agents);
	const custom = lists.flatMap(({ source, agents = [] }) => {
		const byName = new Map<string, ResolvedAgentMention>();
		for (const { name, description } of agents) {
			if (!reservedAgentTypeNames.has(name) && !byName.has(name)) {
				byName.set(name, {
					name,
					source,
					description,
					qualifiedName: `${source}/${name}`,
				});
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
			qualifiedName: type.name,
		})),
		...custom.map((agent) =>
			hasUnknownList || isShared(agent.name)
				? { ...agent, name: agent.qualifiedName }
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

/** The empty string means the workflow takes no args. */
const workflowArgsSummary = (schema: ForkSavedWorkflowSchema): string => {
	const properties = asRecord(schema.properties);
	const required =
		schema.required?.filter((name) => typeof name === "string") ?? [];
	// A required name can sit outside properties, and run_workflow
	// validates against both, so the summary unions them.
	const names = [
		...new Set([...(properties ? Object.keys(properties) : []), ...required]),
	].sort();
	// A schema can constrain args through required alone, so an empty
	// properties object still means args exist; run_workflow validates
	// against the whole schema and would reject an empty args object.
	if (names.length === 0) {
		return "";
	}
	const requiredNames = new Set(required);
	return names
		.map((name) => (requiredNames.has(name) ? `${name} (required)` : name))
		.join(", ");
};

/**
 * Dedupes as run_workflow resolves a name: personal first, then organization
 * ones no personal workflow hides. Both list endpoints already return only
 * what the signed-in user may read. A list that has not loaded yet hides the
 * tiers after it, as run_workflow would resolve nothing for them either.
 */
const resolveWorkflowMentions = (
	lists: readonly {
		tier: WorkflowMention["tier"];
		workflows: readonly ForkSavedWorkflowMetadata[] | undefined;
	}[],
): WorkflowMention[] => {
	const mentions: WorkflowMention[] = [];
	const seen = new Set<string>();
	for (const { tier, workflows } of lists) {
		if (!workflows) {
			break;
		}
		for (const workflow of workflows) {
			if (!seen.has(workflow.name)) {
				seen.add(workflow.name);
				mentions.push({
					name: workflow.name,
					tier,
					description: workflow.description,
					argsSummary: workflow.args_schema
						? workflowArgsSummary(
								workflow.args_schema as ForkSavedWorkflowSchema,
							)
						: "",
				});
			}
		}
	}
	return mentions;
};

/** The one-line state the menu shows above its option list. */
const menuStatus = (
	hasAgentError: boolean,
	hasWorkflowError: boolean,
	isAgentsLoading: boolean,
	isWorkflowsLoading: boolean,
): string | undefined => {
	if (hasAgentError && hasWorkflowError) {
		return "Could not load some agents and workflows. Close and type @ again to retry.";
	}
	if (hasAgentError) {
		return "Could not load some agents. Close and type @ again to retry.";
	}
	if (hasWorkflowError) {
		return "Could not load some workflows. Close and type @ again to retry.";
	}
	if (isAgentsLoading && isWorkflowsLoading) {
		return "Loading agents and workflows...";
	}
	if (isAgentsLoading) {
		return "Loading agents...";
	}
	return isWorkflowsLoading ? "Loading workflows..." : undefined;
};

const handleSelect = (
	option: ForkMentionOption,
	textNode: TextNode | null,
	closeMenu: () => void,
) => {
	if ("agent" in option) {
		const { name } = option.agent;
		textNode
			?.setTextContent(
				`Delegate to the ${name} agent (spawn_agent type ${name}): `,
			)
			.selectEnd();
	} else {
		const { name, argsSummary } = option.workflow;
		textNode
			?.setTextContent(
				`Run the ${name} saved workflow (run_workflow name ${name}) with${
					argsSummary ? ` args: ${argsSummary}. ` : " no args: "
				}`,
			)
			.selectEnd();
	}
	closeMenu();
};

type ForkAgentMentionPluginProps = {
	organizationId?: string;
	projectId?: string;
	hasWorkspace: boolean;
	anchor: HTMLElement | null;
	/**
	 * Pending mode of a chat that does not exist yet: the new-chat
	 * composer has no chat to read, so the form reports its plan mode
	 * and agent type directly. Undefined in an existing chat.
	 */
	planMode?: boolean;
	agentType?: string;
};

/** Offers agent types and saved workflows after "@" and inserts the choice. */
export const ForkAgentMentionPlugin: React.FC<ForkAgentMentionPluginProps> = ({
	organizationId = "",
	projectId,
	hasWorkspace,
	anchor,
	planMode = false,
	agentType,
}) => {
	const isEnabled = useForkExperiment("fork-agent-definitions");
	const isNestingEnabled = useForkExperiment("fork-nested-subagents");
	const isWorkflowsEnabled = useForkExperiment("fork-workflows");
	const [editor] = useLexicalComposerContext();
	const [query, setQuery] = useState<string | null>(null);
	// Escape closes the menu but leaves the query, so Enter and the agent
	// queries check this, and reopening the menu retries a failed load.
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
	// Workflows run only in root chats that are not in plan mode and not
	// explore mode: chatd omits run_workflow for all three states
	// (forkWorkflowTools in coderd/x/chatd/fork_workflow.go), so the menu
	// must not offer what the model has no tool to execute.
	const isWorkflowGatedChat =
		isChildChat ||
		currentChat?.plan_mode === "plan" ||
		// A new chat carries its mode in form state instead of a chat.
		planMode ||
		agentType === "explore" ||
		currentChat?.agent_type === "explore";
	const canRunWorkflows = isWorkflowsEnabled && !isWorkflowGatedChat;
	// Agents are offered only when the experiment is on and this chat may
	// spawn them; workflows stay available on their own experiment.
	const canOfferAgents = isEnabled && canSpawn;
	// A child chat has no project of its own; chatd offers it the agents of
	// its root chat's project.
	const rootChatId = isChildChat ? currentChat?.root_chat_id : undefined;
	const rootChat = useQuery({
		...chat(rootChatId ?? ""),
		enabled: isMenuShown && rootChatId !== undefined,
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
			enabled: canOfferAgents && isMenuShown && organizationId !== "",
			staleTime: 60_000,
		})),
	});
	const isRootProjectUnknown = rootChatId !== undefined && !rootChat.data;
	const mentions = canOfferAgents
		? resolveAgentMentions([
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
			])
		: [];
	// One ordered tier list drives the queries and the resolution, so the
	// personal-over-organization precedence is stated once.
	const workflowTiers = [
		{ tier: "personal", queryTier: "user" },
		{ tier: "org", queryTier: "organization" },
	] as const;
	const workflowQueries = useQueries({
		queries: workflowTiers.map(({ queryTier }) => ({
			...savedWorkflows({ tier: queryTier, organizationId }),
			enabled: canRunWorkflows && isMenuShown && organizationId !== "",
			staleTime: 60_000,
		})),
	});
	// The query cache is shared across pages, so a disabled query still
	// returns the workflows another page fetched; gate the resolved list
	// itself, not only the fetch.
	const workflows = canRunWorkflows
		? resolveWorkflowMentions(
				workflowTiers.map(({ tier }, index) => ({
					tier,
					workflows: workflowQueries[index].data,
				})),
			)
		: [];
	const search = (query ?? "").toLocaleLowerCase("en-US");
	// Only a query with a slash searches tier prefixes, so a short query
	// doesn't match every agent of a tier.
	const agentOptions = mentions
		.filter(({ name, qualifiedName }) =>
			(search.includes("/") ? qualifiedName : name)
				.toLocaleLowerCase("en-US")
				.includes(search),
		)
		.map((mention) => new AgentMentionOption(mention));
	// A workflow name that an agent mention already takes must not appear
	// twice: both @name options would look the same while one delegates and
	// the other runs a workflow.
	const agentNames = new Set(mentions.map(({ name }) => name));
	const workflowOptions = workflows
		.filter(
			({ name }) =>
				!agentNames.has(name) &&
				name.toLocaleLowerCase("en-US").includes(search),
		)
		.map((workflow) => new WorkflowMentionOption(workflow));
	const options: ForkMentionOption[] = [...agentOptions, ...workflowOptions];
	// A disabled query still reports the cached status of a key another
	// page fetched, so mask each status with its capability gate.
	const unresolved = [
		...(canOfferAgents ? storedQueries : []),
		...(isRootProjectUnknown ? [rootChat] : []),
	].filter((result) => !result.data);
	const isLoading = unresolved.some((result) => result.isFetching);
	const hasError = [
		...(canOfferAgents ? storedQueries : []),
		...(rootChatId ? [rootChat] : []),
	].some((result) => result.isError);
	const hasWorkflowError =
		canRunWorkflows && workflowQueries.some((result) => result.isError);
	const isWorkflowsLoading =
		canRunWorkflows &&
		workflowQueries.some((result) => !result.data && result.isFetching);
	const hasAgents = canOfferAgents || !canRunWorkflows;

	// Lexical's menu lets Enter through when nothing is selectable, which
	// would send the unfinished mention while agents or workflows load or
	// failed to.
	const handleEnter = useEffectEvent((event: KeyboardEvent | null) => {
		if (
			!isMenuShown ||
			options.length > 0 ||
			!(isLoading || hasError || hasWorkflowError || isWorkflowsLoading)
		) {
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

	if ((!isEnabled || !canSpawn) && !canRunWorkflows) {
		return null;
	}

	return (
		<LexicalTypeaheadMenuPlugin<ForkMentionOption>
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
						workflowStartIndex={agentOptions.length}
						status={menuStatus(
							hasError,
							hasWorkflowError,
							isLoading,
							isWorkflowsLoading,
						)}
						emptyMessage={
							hasAgents && canRunWorkflows
								? "No agents or workflows match that query."
								: hasAgents
									? "No agents match that query."
									: "No workflows match that query."
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
