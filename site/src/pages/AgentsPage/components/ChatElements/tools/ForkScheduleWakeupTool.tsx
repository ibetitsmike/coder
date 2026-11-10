// This file is fork code: it renders the schedule_wakeup tool of the
// fork-chat-wakeups experiment.

import type React from "react";
import { DATE_FORMAT, formatDateTime, humanDurationShort } from "#/utils/time";
import { asNumber } from "../runtimeTypeUtils";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { asString, parseArgs, type ToolStatus } from "./utils";

const failedLabel = "Failed to schedule a wake-up";

/** Describes the schedule in the tool args, like "every 5m". */
const formatSchedule = (args: Record<string, unknown> | null): string => {
	const interval = asNumber(args?.interval_seconds);
	if (interval !== undefined) {
		return `every ${humanDurationShort(interval * 1000)}`;
	}
	const cron = asString(args?.cron);
	if (cron !== "") {
		const timezone = asString(args?.timezone);
		return timezone === ""
			? `on cron ${cron}`
			: `on cron ${cron} (${timezone})`;
	}
	const delay = asNumber(args?.delay_seconds);
	if (delay !== undefined) {
		return `in ${humanDurationShort(delay * 1000)}`;
	}
	return "";
};

type ScheduledWakeup = {
	nextRunAt: string;
	expiresAt: string;
	maxRuns?: number;
};

type ForkScheduleWakeupToolProps = {
	schedule: string;
	prompt: string;
	wakeup?: ScheduledWakeup;
	status: ToolStatus;
	isError: boolean;
	errorMessage?: string;
};

const ForkScheduleWakeupTool: React.FC<ForkScheduleWakeupToolProps> = ({
	schedule,
	prompt,
	wakeup,
	status,
	isError,
	errorMessage,
}) => {
	let label: string;
	if (status === "running") {
		label = "Scheduling a wake-up…";
	} else if (isError) {
		label = failedLabel;
	} else {
		label =
			schedule === ""
				? "Scheduled a wake-up"
				: `Scheduled a wake-up ${schedule}`;
	}
	const errorDetail = errorMessage?.trim();
	const details: string[] = [];
	if (wakeup) {
		details.push(
			`Next run ${formatDateTime(wakeup.nextRunAt, DATE_FORMAT.FULL_DATETIME)}`,
		);
		if (wakeup.maxRuns !== undefined) {
			details.push(
				`Stops after ${wakeup.maxRuns} ${wakeup.maxRuns === 1 ? "run" : "runs"}`,
			);
		}
		if (wakeup.expiresAt !== "") {
			details.push(
				`Expires ${formatDateTime(wakeup.expiresAt, DATE_FORMAT.FULL_DATETIME)}`,
			);
		}
	}

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={prompt !== "" || details.length > 0 || Boolean(errorDetail)}
		>
			<ToolCall.Header iconName="fork_wakeup" label={label} />
			<ToolCall.Content>
				{errorDetail && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{prompt !== "" && (
					<div className="mt-1.5 whitespace-pre-wrap break-words text-[13px] text-content-primary">
						{prompt}
					</div>
				)}
				{details.length > 0 && (
					<div className="mt-1.5 text-[13px] text-content-secondary">
						{details.join(", ")}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};

/**
 * The schedule and prompt come from the args, so the card can show them
 * while the call runs; the run times come from the result.
 */
export const ForkScheduleWakeupToolRenderer: React.FC<
	ForkToolRendererProps
> = ({ fallback: Fallback, ...props }) => {
	const args = parseArgs(props.args);
	const schedule = formatSchedule(args);
	const prompt = asString(args?.prompt).trim();
	if (props.isError) {
		return (
			<ForkScheduleWakeupTool
				schedule={schedule}
				prompt={prompt}
				status={props.status}
				isError
				errorMessage={forkToolErrorMessage(props.result)}
			/>
		);
	}
	const result = parseArgs(props.result);
	if (!result || typeof result.next_run_at !== "string") {
		if (props.status === "running") {
			return (
				<ForkScheduleWakeupTool
					schedule={schedule}
					prompt={prompt}
					status={props.status}
					isError={false}
				/>
			);
		}
		return <Fallback {...props} />;
	}
	return (
		<ForkScheduleWakeupTool
			schedule={schedule}
			prompt={prompt}
			wakeup={{
				nextRunAt: result.next_run_at,
				expiresAt: asString(result.expires_at),
				maxRuns: asNumber(result.max_runs),
			}}
			status={props.status}
			isError={false}
		/>
	);
};
