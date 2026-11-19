// This file is fork code: the "@" agent and workflow menu of the
// fork-agent-definitions and fork-workflows experiments.

import { MenuOption } from "@lexical/react/LexicalTypeaheadMenuPlugin";
import { cn } from "cn";
import type { ChatSkillSource } from "#/api/typesGenerated";
import {
	Popover,
	PopoverAnchor,
	PopoverContent,
} from "#/components/Popover/Popover";

/** An agent type the "@" menu offers, by the name spawn_agent accepts. */
export type AgentMention = {
	name: string;
	source: "built-in" | ChatSkillSource;
	description: string;
};

/** A saved workflow the "@" menu offers to run by name. */
export type WorkflowMention = {
	name: string;
	/** The tier label shown on the row's right edge, spelled as agent sources. */
	tier: "personal" | "org";
	description: string;
	/**
	 * The run_workflow args to fill in, required ones marked. Empty when the
	 * workflow takes no args.
	 */
	argsSummary: string;
};

/** Adapts an agent mention to Lexical's typeahead menu options. */
export class AgentMentionOption extends MenuOption {
	constructor(readonly agent: AgentMention) {
		super(`${agent.source}:${agent.name}`);
	}
}

/** Adapts a workflow mention to Lexical's typeahead menu options. */
export class WorkflowMentionOption extends MenuOption {
	constructor(readonly workflow: WorkflowMention) {
		super(`workflow:${workflow.tier}:${workflow.name}`);
	}
}

/** Either kind of option the "@" menu offers. */
export type ForkMentionOption = AgentMentionOption | WorkflowMentionOption;

/** The visible rows of one option, agent or workflow. */
type OptionRow = {
	/** The mention text, shown as @name. */
	name: string;
	/** The tier or source label on the row's right edge. */
	label: string;
	description: string;
	/** The run_workflow args to fill in; absent for an agent. */
	args?: string;
};

/** The rows an option button shows, whichever kind of option it is. */
const optionRow = (option: ForkMentionOption): OptionRow => {
	if ("agent" in option) {
		return {
			name: option.agent.name,
			label: option.agent.source,
			description: option.agent.description,
		};
	}
	return {
		name: option.workflow.name,
		label: option.workflow.tier,
		description: option.workflow.description,
		args: option.workflow.argsSummary,
	};
};

type MentionOptionProps = {
	row: OptionRow;
	setRefElement: (element: HTMLButtonElement | null) => void;
	index: number;
	isSelected: boolean;
	onSelect: () => void;
	onHighlight: (index: number) => void;
};

/** One option button of the typeahead list. */
const MentionOption: React.FC<MentionOptionProps> = ({
	row,
	setRefElement,
	index,
	isSelected,
	onSelect,
	onHighlight,
}) => {
	return (
		<button
			type="button"
			ref={setRefElement}
			id={`typeahead-item-${index}`}
			role="option"
			tabIndex={-1}
			aria-selected={isSelected}
			className={cn(
				"flex w-full cursor-default select-none flex-col gap-1 rounded-sm border-0 bg-transparent px-2 py-2 text-left font-medium text-content-secondary text-sm",
				isSelected &&
					"bg-surface-secondary text-content-primary ring-2 ring-content-link ring-inset",
			)}
			onMouseEnter={() => onHighlight(index)}
			onClick={onSelect}
		>
			<div className="flex items-center gap-2 text-xs">
				<span className="truncate font-mono">@{row.name}</span>
				<span className="ml-auto text-content-secondary">{row.label}</span>
			</div>
			<div className="line-clamp-2 text-content-secondary text-xs">
				{row.description}
			</div>
			{row.args && (
				<div className="text-content-secondary text-xs">Args: {row.args}</div>
			)}
		</button>
	);
};

type ForkAgentMentionMenuProps = {
	/** The composer box the menu is pinned above. */
	anchor: HTMLElement | null;
	options: readonly ForkMentionOption[];
	selectedIndex: number | null;
	/**
	 * The index of the first workflow option, which the Workflows heading
	 * precedes. Defaults past every option, so agents-only callers keep the
	 * flat list.
	 */
	workflowStartIndex?: number;
	/** Why the list may be incomplete, such as a source still loading. */
	status?: string;
	/** Shown when no option matches and there is no status to explain why. */
	emptyMessage?: string;
	onSelect: (option: ForkMentionOption) => void;
	onHighlight: (index: number) => void;
};

/** Renders the "@" typeahead listbox above the composer. */
export const ForkAgentMentionMenu: React.FC<ForkAgentMentionMenuProps> = ({
	anchor,
	options,
	selectedIndex,
	workflowStartIndex = options.length,
	status,
	emptyMessage = "No agents or workflows match that query.",
	onSelect,
	onHighlight,
}) => {
	const optionElements = options.map((option, index) => (
		<MentionOption
			key={option.key}
			row={optionRow(option)}
			setRefElement={option.setRefElement}
			index={index}
			isSelected={selectedIndex === index}
			onSelect={() => onSelect(option)}
			onHighlight={onHighlight}
		/>
	));
	return (
		<Popover open={anchor !== null}>
			{anchor && <PopoverAnchor virtualRef={{ current: anchor }} />}
			{/* The editor's aria-activedescendant names these option ids. */}
			<PopoverContent
				disablePortal
				role="presentation"
				align="start"
				side="top"
				sideOffset={8}
				className="w-(--radix-popper-anchor-width) overflow-hidden p-1 mobile-full-width-dropdown mobile-full-width-dropdown-above-composer"
				onMouseDown={(event) => event.preventDefault()}
				onOpenAutoFocus={(event) => event.preventDefault()}
				onCloseAutoFocus={(event) => event.preventDefault()}
			>
				<div className="max-h-72 min-h-0 overflow-y-auto mobile-full-width-dropdown-scroll-area">
					{optionElements.slice(0, workflowStartIndex)}
					{workflowStartIndex < optionElements.length && (
						<div role="group" aria-label="Workflows">
							<div className="px-2 py-1.5 font-medium text-content-secondary text-xs">
								Workflows
							</div>
							{optionElements.slice(workflowStartIndex)}
						</div>
					)}
					{/* Stays mounted so screen readers announce status changes. */}
					<div
						role="status"
						className={cn(status && "px-2 py-2 text-content-secondary text-sm")}
					>
						{status}
					</div>
					{optionElements.length === 0 && !status && (
						<div className="py-6 text-center text-sm">{emptyMessage}</div>
					)}
				</div>
			</PopoverContent>
		</Popover>
	);
};
