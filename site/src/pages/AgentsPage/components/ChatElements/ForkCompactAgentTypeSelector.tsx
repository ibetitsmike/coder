// This file is fork code: the agent type picker of the
// fork-agent-definitions experiment's new-chat composer.

import { cn } from "cn";
import { BotIcon, CheckIcon, SettingsIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { getErrorMessage } from "#/api/errors";
import type { ForkAgentType } from "#/api/typesGenerated";
import { ChevronDownIcon } from "#/components/AnimatedIcons/ChevronDown";
import { Button } from "#/components/Button/Button";
import {
	Command,
	CommandEmpty,
	CommandGroup,
	CommandInput,
	CommandItem,
	CommandList,
	CommandSeparator,
} from "#/components/Command/Command";
import {
	Popover,
	PopoverContent,
	PopoverTrigger,
} from "#/components/Popover/Popover";
import { Spinner } from "#/components/Spinner/Spinner";

const defaultAgentLabel = "Default agent";

/** The label of each agent type source, in the order the picker groups them. */
export const agentTypeSourceLabels: Record<string, string> = {
	builtin: "Built-in",
	personal: "Personal",
	org: "Organization",
	project: "Project",
	workspace: "Workspace",
};
const groupBySource = (agentTypes: readonly ForkAgentType[]) =>
	Object.entries(agentTypeSourceLabels)
		.map(([source, heading]) => ({
			heading,
			types: agentTypes.filter((type) => type.source === source),
		}))
		.filter(({ types }) => types.length > 0);

type ForkCompactAgentTypeSelectorProps = {
	/** The selected type's name, or null for the default agent. */
	readonly value: string | null;
	readonly agentTypes: readonly ForkAgentType[];
	readonly onChange: (name: string | null) => void;
	readonly isLoading?: boolean;
	/** Shown in place of the type list. */
	readonly error?: unknown;
	readonly onRetry?: () => void;
	readonly disabled?: boolean;
};

/** Picks the agent type a new chat starts as, if any. */
export const ForkCompactAgentTypeSelector: React.FC<
	ForkCompactAgentTypeSelectorProps
> = ({
	value,
	agentTypes,
	onChange,
	isLoading = false,
	error,
	onRetry,
	disabled = false,
}) => {
	const [open, setOpen] = useState(false);
	const label = value ?? defaultAgentLabel;
	const select = (name: string | null) => {
		onChange(name);
		setOpen(false);
	};
	return (
		<Popover open={open} onOpenChange={disabled ? undefined : setOpen}>
			<PopoverTrigger asChild>
				<button
					type="button"
					disabled={disabled}
					aria-label={`Agent type: ${label}`}
					className={cn(
						"group flex h-6 w-auto min-w-0 cursor-pointer items-center gap-1.5 border-none bg-transparent px-1 text-xs text-content-secondary shadow-none whitespace-nowrap transition-colors",
						"hover:text-content-primary focus:ring-0",
						"disabled:cursor-not-allowed disabled:opacity-50",
					)}
				>
					<BotIcon aria-hidden="true" className="size-3.5 shrink-0" />
					<span className="truncate">{label}</span>
					<ChevronDownIcon
						open={open}
						className="size-icon-sm shrink-0 text-content-secondary transition-colors group-hover:text-content-primary"
					/>
				</button>
			</PopoverTrigger>
			<PopoverContent
				align="start"
				className="mobile-full-width-dropdown mobile-full-width-dropdown-bottom w-80 overflow-hidden p-0"
				onOpenAutoFocus={(event) => {
					// Focusing the search input on touch devices opens the software
					// keyboard, which hides the list.
					if (matchMedia("(pointer: coarse)").matches) {
						event.preventDefault();
					}
				}}
			>
				<Command loop label="Search agent types">
					<CommandInput placeholder="Search…" className="text-xs" />
					<CommandList className="max-h-80">
						<CommandEmpty className="text-xs">
							No agent types found
						</CommandEmpty>
						<CommandGroup>
							<CommandItem
								className="text-xs font-normal"
								value={defaultAgentLabel}
								onSelect={() => select(null)}
							>
								<BotIcon />
								<span className="truncate">{defaultAgentLabel}</span>
								{value === null && (
									<CheckIcon className="ml-auto size-icon-sm shrink-0" />
								)}
							</CommandItem>
						</CommandGroup>
						<CommandSeparator />
						{isLoading ? (
							<div className="flex items-center gap-2 px-4 py-3 text-xs text-content-secondary">
								<Spinner loading size="sm" />
								Loading agent types…
							</div>
						) : error ? (
							<div className="flex items-center gap-2 px-4 py-2 text-xs text-content-secondary">
								<span className="min-w-0 flex-1">
									{getErrorMessage(error, "Failed to load agent types.")}
								</span>
								{onRetry && (
									<Button
										variant="subtle"
										size="sm"
										onClick={onRetry}
										// cmdk handles Enter on its root to select the highlighted
										// item, which would swallow the button's own activation.
										onKeyDown={(event) => event.stopPropagation()}
									>
										Retry
									</Button>
								)}
							</div>
						) : (
							groupBySource(agentTypes).map(({ heading, types }) => (
								<CommandGroup key={heading} heading={heading}>
									{types.map((agentType) => (
										<CommandItem
											key={agentType.name}
											className="text-xs font-normal"
											value={agentType.name}
											keywords={[agentType.description]}
											disabled={!agentType.root_available}
											onSelect={() => select(agentType.name)}
										>
											<span className="flex min-w-0 flex-col">
												<span className="truncate text-content-primary">
													{agentType.name}
												</span>
												<span className="truncate">
													{agentType.root_available
														? agentType.description
														: agentType.root_unavailable_reason}
												</span>
											</span>
											{value === agentType.name && (
												<CheckIcon className="ml-auto size-icon-sm shrink-0" />
											)}
										</CommandItem>
									))}
								</CommandGroup>
							))
						)}
					</CommandList>
				</Command>
				<div className="border-0 border-t border-solid border-border p-1">
					<Button
						asChild
						variant="subtle"
						size="sm"
						className="w-full justify-start"
					>
						<Link to="/agents/settings/agent-definitions">
							<SettingsIcon />
							Manage agent definitions…
						</Link>
					</Button>
				</div>
			</PopoverContent>
		</Popover>
	);
};
