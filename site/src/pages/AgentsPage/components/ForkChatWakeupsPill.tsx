import { AlarmClockIcon } from "lucide-react";
import type React from "react";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorMessage } from "#/api/errors";
import { chatWakeups, updateChatWakeup } from "#/api/queries/forkChatWakeups";
import type {
	ChatWakeup,
	ChatWakeupEvent,
	ChatWakeupStatus,
} from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { useTime } from "#/hooks/useTime";
import { humanDurationShort, timeFrom } from "#/utils/time";
import { ComposerPillError, ComposerPillPopover } from "./ComposerPillPopover";

// This file is fork code: the composer pill for the fork-chat-wakeups
// experiment lives here so upstream chat files stay unchanged.

type ForkChatWakeupsPillProps = {
	chatId: string;
	isDisabled: boolean;
};

/** Shows the chat's open wake-ups as a pill with a popover. */
export const ForkChatWakeupsPill: React.FC<ForkChatWakeupsPillProps> = ({
	chatId,
	isDisabled,
}) => {
	const queryClient = useQueryClient();
	const wakeupsQuery = useQuery(chatWakeups(chatId));
	const { isPending, variables, error, mutate } = useMutation(
		updateChatWakeup(queryClient, chatId),
	);

	return (
		<ForkChatWakeupsPillView
			wakeups={wakeupsQuery.data}
			error={wakeupsQuery.error}
			onUpdate={(wakeup, status) => mutate({ wakeupId: wakeup.id, status })}
			isDisabled={isDisabled || isPending}
			updateError={
				error && variables
					? {
							id: variables.wakeupId,
							message: getErrorMessage(error, "Failed to update the wake-up."),
						}
					: undefined
			}
		/>
	);
};

const isOpenWakeup = (wakeup: ChatWakeup) =>
	wakeup.status === "active" || wakeup.status === "paused";

type WakeupUpdateError = { id: string; message: string };

type ForkChatWakeupsPillViewProps = {
	wakeups: readonly ChatWakeup[] | undefined;
	error: unknown;
	onUpdate: (wakeup: ChatWakeup, status: ChatWakeupStatus) => void;
	/** Disables every row's actions, so updates run one at a time. */
	isDisabled?: boolean;
	updateError?: WakeupUpdateError;
};

/**
 * Renders the wake-ups pill. Renders nothing while the wake-ups load or no
 * open wake-up remains, and a warning pill when a fetch failed with no open
 * wake-up to show.
 */
export const ForkChatWakeupsPillView: React.FC<
	ForkChatWakeupsPillViewProps
> = ({ wakeups, error, onUpdate, isDisabled = false, updateError }) => {
	const [isOpen, setIsOpen] = useState(false);
	const openWakeups = wakeups?.filter(isOpenWakeup);
	// Stay mounted while the popover is open, so canceling the last wake-up
	// does not unmount the popover that holds keyboard focus.
	if (!openWakeups || (openWakeups.length === 0 && !isOpen)) {
		return error ? (
			<ComposerPillError
				label="Wake-ups failed to load"
				heading="Wake-ups"
				message={getErrorMessage(error, "Failed to load the wake-ups.")}
			/>
		) : null;
	}

	return (
		<ComposerPillPopover
			label={`Wake-ups, ${openWakeups.length} open`}
			heading="Wake-ups"
			open={isOpen}
			onOpenChange={setIsOpen}
			trigger={
				<>
					<AlarmClockIcon
						aria-hidden="true"
						className="size-3.5 shrink-0 text-content-secondary"
					/>
					<span className="text-content-secondary tabular-nums">
						{openWakeups.length}
					</span>
				</>
			}
		>
			{Boolean(error) && (
				<p role="status" className="mb-0 mt-2 text-content-warning">
					{getErrorMessage(error, "Failed to refresh the wake-ups.")}
				</p>
			)}
			{openWakeups.length === 0 ? (
				<p className="mb-0 mt-2 text-content-secondary">No open wake-ups.</p>
			) : (
				<ul className="m-0 mt-2 flex list-none flex-col gap-3 p-0">
					{openWakeups.map((wakeup) => (
						<ForkChatWakeupRow
							key={wakeup.id}
							wakeup={wakeup}
							onUpdate={onUpdate}
							isDisabled={isDisabled}
							errorMessage={
								wakeup.id === updateError?.id ? updateError.message : undefined
							}
						/>
					))}
				</ul>
			)}
		</ComposerPillPopover>
	);
};

/** Describes what the wake-up waits for, like "Every 5m" or "Once". */
const formatWakeupSchedule = (wakeup: ChatWakeup): string => {
	switch (wakeup.kind) {
		case "interval":
			return wakeup.interval_seconds === undefined
				? "Every unknown interval"
				: `Every ${humanDurationShort(wakeup.interval_seconds * 1000)}`;
		case "cron":
			return `Cron ${wakeup.cron ?? "Unknown"} (${wakeup.timezone})`;
		case "delay":
			return "Once";
		// The events list keeps entries that were already delivered, so it
		// is not a count of what remains.
		case "events":
			return "Waiting for events";
	}
};

const formatWakeupEvent = (event: ChatWakeupEvent): string => {
	if (event.chat_id) {
		return `child agent ${event.chat_id.slice(0, 8)}`;
	}
	if (event.url) {
		return event.on?.length
			? `${event.url} (${event.on.join(", ")})`
			: event.url;
	}
	return event.type;
};

/** Explains the status_reason codes the server pauses a wake-up with. */
const pauseReasonText: Record<string, string> = {
	daily_run_cap: "daily run limit reached",
	daily_spend_cap: "daily spend limit reached",
	org_daily_run_cap: "organization daily run limit reached",
	org_daily_spend_cap: "organization daily spend limit reached",
	hook_denied: "a prompt hook denied the prompt",
	invalid_cron: "invalid cron schedule",
	invalid_events: "invalid events",
};

type ForkChatWakeupRowProps = {
	wakeup: ChatWakeup;
	onUpdate: (wakeup: ChatWakeup, status: ChatWakeupStatus) => void;
	isDisabled: boolean;
	errorMessage?: string;
};

const ForkChatWakeupRow: React.FC<ForkChatWakeupRowProps> = ({
	wakeup,
	onUpdate,
	isDisabled,
	errorMessage,
}) => {
	const schedule = formatWakeupSchedule(wakeup);
	const now = useTime(() => Date.now(), {
		disabled: !wakeup.next_run_at,
		interval: 30_000,
	});
	const toggleLabel = wakeup.status === "active" ? "Pause" : "Resume";
	const statusParts: string[] = [];
	if (wakeup.status === "active" && wakeup.next_run_at) {
		// An events wake-up polls at next_run_at; the agent runs only once
		// an event arrives.
		const label = wakeup.kind === "events" ? "Next check" : "Next run";
		statusParts.push(`${label} ${timeFrom(wakeup.next_run_at, now)}`);
	}
	if (wakeup.status === "paused") {
		const reason = wakeup.status_reason
			? pauseReasonText[wakeup.status_reason]
			: undefined;
		statusParts.push(reason ? `Paused: ${reason}` : "Paused");
	}
	const runs = wakeup.run_count === 1 ? "1 run" : `${wakeup.run_count} runs`;
	statusParts.push(
		wakeup.max_runs === undefined ? runs : `${runs} of ${wakeup.max_runs}`,
	);

	return (
		<li className="m-0 flex list-none flex-col gap-1.5 p-0">
			<div>{schedule}</div>
			{wakeup.kind === "events" && wakeup.events.length > 0 && (
				<ul className="m-0 flex list-none flex-col gap-0.5 p-0">
					{wakeup.events.map((event, index) => (
						<li
							key={`${event.type}-${index}`}
							className="wrap-break-word text-content-secondary"
						>
							{formatWakeupEvent(event)}
						</li>
					))}
				</ul>
			)}
			<span
				title={wakeup.prompt}
				className="wrap-break-word leading-5 line-clamp-2"
			>
				{wakeup.prompt}
			</span>
			<div className="text-content-secondary">{statusParts.join(", ")}</div>
			<div className="flex items-center gap-1.5">
				<Button
					variant="outline"
					size="sm"
					disabled={isDisabled}
					aria-label={`${toggleLabel} ${schedule}: ${wakeup.prompt}`}
					onClick={() =>
						onUpdate(wakeup, wakeup.status === "active" ? "paused" : "active")
					}
				>
					{toggleLabel}
				</Button>
				<Button
					variant="subtle"
					size="sm"
					disabled={isDisabled}
					aria-label={`Cancel ${schedule}: ${wakeup.prompt}`}
					onClick={() => onUpdate(wakeup, "canceled")}
				>
					Cancel
				</Button>
			</div>
			{errorMessage && (
				<p role="alert" className="mb-0 mt-0 text-content-destructive">
					{errorMessage}
				</p>
			)}
		</li>
	);
};
