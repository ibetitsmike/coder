// This file is fork code: the "@" agent menu of the fork-agent-definitions
// experiment.

import { MenuOption } from "@lexical/react/LexicalTypeaheadMenuPlugin";
import type { ChatSkillSource } from "#/api/typesGenerated";
import {
	Command,
	CommandEmpty,
	CommandItem,
	CommandList,
} from "#/components/Command/Command";
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

export class AgentMentionOption extends MenuOption {
	constructor(readonly agent: AgentMention) {
		super(`${agent.source}:${agent.name}`);
	}
}

type ForkAgentMentionMenuProps = {
	/** The composer box the menu is pinned above. */
	anchor: HTMLElement | null;
	options: readonly AgentMentionOption[];
	selectedIndex: number | null;
	/** Why the list may be incomplete, such as a source still loading. */
	status?: string;
	onSelect: (option: AgentMentionOption) => void;
	onHighlight: (index: number) => void;
};

export const ForkAgentMentionMenu: React.FC<ForkAgentMentionMenuProps> = ({
	anchor,
	options,
	selectedIndex,
	status,
	onSelect,
	onHighlight,
}) => (
	<Popover open={anchor !== null}>
		{anchor && <PopoverAnchor virtualRef={{ current: anchor }} />}
		<PopoverContent
			align="start"
			side="top"
			sideOffset={8}
			className="w-(--radix-popper-anchor-width) overflow-hidden p-1 mobile-full-width-dropdown mobile-full-width-dropdown-above-composer"
			onMouseDown={(event) => event.preventDefault()}
			onOpenAutoFocus={(event) => event.preventDefault()}
			onCloseAutoFocus={(event) => event.preventDefault()}
		>
			<Command
				shouldFilter={false}
				value={selectedIndex === null ? "" : String(selectedIndex)}
				onValueChange={(value) => {
					if (/^\d+$/.test(value)) {
						onHighlight(Number(value));
					}
				}}
			>
				<CommandList className="max-h-72 border-t-0 mobile-full-width-dropdown-scroll-area">
					{options.map((option, index) => (
						<CommandItem
							key={option.key}
							ref={option.setRefElement}
							value={String(index)}
							className="flex-col items-stretch gap-1"
							onSelect={() => onSelect(option)}
						>
							<div className="flex items-center gap-2 text-xs">
								<span className="truncate font-mono">@{option.agent.name}</span>
								<span className="ml-auto text-content-secondary">
									{option.agent.source}
								</span>
							</div>
							<div className="line-clamp-2 text-content-secondary text-xs">
								{option.agent.description}
							</div>
						</CommandItem>
					))}
					{status && <CommandItem disabled>{status}</CommandItem>}
					<CommandEmpty>No agents match that query.</CommandEmpty>
				</CommandList>
			</Command>
		</PopoverContent>
	</Popover>
);
