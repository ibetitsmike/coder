// This file is fork code: it renders the schedule_wakeup tool of the
// fork-chat-wakeups experiment.

import type React from "react";
import { DATE_FORMAT, formatDateTime, humanDurationShort } from "#/utils/time";
import { asNumber } from "../runtimeTypeUtils";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { asString, parseArgs } from "./utils";

const failedLabel = "Failed to schedule a wake-up";

/** Describes the schedule in the tool args, like "every 5m". */
const formatSchedule = (args: Record<string, unknown> | null): string => {
	const interval = asNumber(args?.interval_seconds);
	if (interval !== undefined) {
		return `every ${humanDurationShort(interval * 1000)}`;
	}
	const cron = asString(args?.cron);
	if (cron !== "") {
		// The server runs a cron schedule without a time zone in UTC.
		return `on cron ${cron} (${asString(args?.timezone) || "UTC"})`;
	}
	const delay = asNumber(args?.delay_seconds);
	return delay === undefined ? "" : `in ${humanDurationShort(delay * 1000)}`;
};

const formatRunDetails = (result: Record<string, unknown>): string[] => {
	const details = [
		`Next run ${formatDateTime(asString(result.next_run_at), DATE_FORMAT.FULL_DATETIME)}`,
	];
	const maxRuns = asNumber(result.max_runs);
	if (maxRuns !== undefined) {
		details.push(`Stops after ${maxRuns} ${maxRuns === 1 ? "run" : "runs"}`);
	}
	const expiresAt = asString(result.expires_at);
	if (expiresAt !== "") {
		details.push(
			`Expires ${formatDateTime(expiresAt, DATE_FORMAT.FULL_DATETIME)}`,
		);
	}
	return details;
};

/**
 * The schedule and prompt come from the args, so the card can show them
 * while the call runs; the run times come from the result.
 */
export const ForkScheduleWakeupToolRenderer: React.FC<
	ForkToolRendererProps
> = ({ fallback: Fallback, ...props }) => {
	const { status, isError } = props;
	const result = isError ? null : parseArgs(props.result);
	const hasNextRun = typeof result?.next_run_at === "string";
	if (!isError && !hasNextRun && status !== "running") {
		return <Fallback {...props} />;
	}
	const args = parseArgs(props.args);
	const schedule = formatSchedule(args);
	const prompt = asString(args?.prompt).trim();
	const details = result && hasNextRun ? formatRunDetails(result) : [];
	const errorMessage = isError ? forkToolErrorMessage(props.result) : "";
	const errorDetail = errorMessage.trim();
	const scheduleSuffix = schedule === "" ? "" : ` ${schedule}`;
	let label = `Scheduled a wake-up${scheduleSuffix}`;
	if (status === "running") {
		label = `Scheduling a wake-up${scheduleSuffix}…`;
	} else if (isError) {
		label = failedLabel;
	}

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={prompt !== "" || details.length > 0 || errorDetail !== ""}
		>
			<ToolCall.Header iconName="fork_wakeup" label={label} />
			<ToolCall.Content>
				{errorDetail !== "" && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{prompt !== "" && (
					<div className="mt-1.5 whitespace-pre-wrap break-words text-[13px] text-content-primary">
						{prompt}
					</div>
				)}
				{details.length > 0 && (
					<div className="mt-1.5 text-[13px] text-content-secondary">
						{details.map((detail) => (
							<div key={detail}>{detail}</div>
						))}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};
