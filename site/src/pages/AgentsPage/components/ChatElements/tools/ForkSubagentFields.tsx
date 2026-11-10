// This file is fork code: it adds the fields of the fork experiments to the
// spawn_agent and wait_agent cards.

import { WrenchIcon } from "lucide-react";
import type React from "react";
import { forkToolErrorMessage, useForkExperiment } from "./forkToolUtils";
import type { SubagentDescriptor } from "./subagentDescriptor";
import { ToolCall } from "./ToolCall";
import { asRecord, asString, parseArgs, parseArray } from "./utils";

/** The unparsed spawn or wait tool call; ForkSubagentFields does the parsing. */
export type ForkSubagentCall = {
	args: unknown;
	result: unknown;
};

type RecentToolCall = {
	toolName: string;
	args: string;
	isError: boolean;
};

const parseRecentToolCall = (item: unknown): RecentToolCall | null => {
	const record = asRecord(item);
	if (!record || typeof record.tool_name !== "string") {
		return null;
	}
	return {
		toolName: record.tool_name,
		args: record.args === undefined ? "" : JSON.stringify(record.args),
		isError: record.is_error === true,
	};
};

/** Lists the child's last tool calls that wait_agent returned, collapsed. */
const RecentToolCalls: React.FC<{ calls: readonly RecentToolCall[] }> = ({
	calls,
}) => (
	<ToolCall.Root className="mt-1.5" status="completed" hasContent>
		<ToolCall.HeaderButton>
			<ToolCall.LeadingIcon>
				<WrenchIcon className="size-4 shrink-0 stroke-[1.5] text-current" />
			</ToolCall.LeadingIcon>
			<ToolCall.Label>
				{calls.length === 1
					? "1 recent tool call"
					: `${calls.length} recent tool calls`}
			</ToolCall.Label>
			<ToolCall.Chevron />
		</ToolCall.HeaderButton>
		<ToolCall.Content>
			<ol className="m-0 mt-1.5 list-none p-0">
				{calls.map((call, index) => (
					<li
						key={index}
						className="flex items-baseline gap-1.5 text-[13px] text-content-secondary"
					>
						<span className="shrink-0 font-mono text-xs">{call.toolName}</span>
						{call.isError && (
							<span className="shrink-0 text-content-destructive">failed</span>
						)}
						<span className="min-w-0 flex-1 truncate font-mono text-xs opacity-60">
							{call.args}
						</span>
					</li>
				))}
			</ol>
		</ToolCall.Content>
	</ToolCall.Root>
);

/**
 * Shows the fork experiments' spawn_agent and wait_agent fields. It renders
 * outside the card's collapsed content, so a timed-out wait without a report
 * still shows what the child was doing.
 */
export const ForkSubagentFields: React.FC<{
	descriptor: SubagentDescriptor;
	isError: boolean;
	call: ForkSubagentCall;
}> = ({ descriptor, isError, call }) => {
	const agentDefinitions = useForkExperiment("fork-agent-definitions");
	const nestedSubagents = useForkExperiment("fork-nested-subagents");
	const chatTodos = useForkExperiment("fork-chat-todos");
	const result = parseArgs(call.result);

	if (descriptor.action === "spawn") {
		const type = asString(result?.type || parseArgs(call.args)?.type).trim();
		// The descriptor maps a type it does not know to a built-in variant,
		// so a type that differs from the variant is a custom one.
		const customType =
			agentDefinitions && type.toLowerCase() !== descriptor.variant ? type : "";
		const errorMessage =
			nestedSubagents && isError
				? forkToolErrorMessage(call.result).trim()
				: "";
		return (
			<>
				{customType && (
					<div className="mt-1.5 text-[13px] text-content-secondary">
						Agent type: {customType}
					</div>
				)}
				{errorMessage && (
					<ToolCall.ErrorDetail>{errorMessage}</ToolCall.ErrorDetail>
				)}
			</>
		);
	}

	if (descriptor.action === "wait" && chatTodos && result) {
		const callsError = asString(result.tool_calls_error).trim();
		const calls = parseArray(result.tool_calls, parseRecentToolCall) ?? [];
		return (
			<>
				{callsError && (
					<ToolCall.ErrorDetail>{callsError}</ToolCall.ErrorDetail>
				)}
				{calls.length > 0 && <RecentToolCalls calls={calls} />}
			</>
		);
	}

	return null;
};
