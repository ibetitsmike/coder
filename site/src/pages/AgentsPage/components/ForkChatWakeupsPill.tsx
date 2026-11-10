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
import { humanDurationShort, relativeTime } from "#/utils/time";
import { ComposerPillError, ComposerPillPopover } from "./ComposerPillPopover";

// This file is fork code: the composer pill for the fork-chat-wakeups
// experiment lives here so upstream chat files stay unchanged.

type ForkChatWakeupsPillProps = {
	chatId: string;
};

/** Shows the chat's open wake-ups as a pill with a popover. */
export const ForkChatWakeupsPill: React.FC<ForkChatWakeupsPillProps> = ({
	chatId,
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
			pendingId={isPending ? variables?.wakeupId : undefined}
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
	pendingId?: string;
	updateError?: WakeupUpdateError;
};

/**
 * Renders the wake-ups pill. Renders nothing while the wake-ups load or no
 * open wake-up remains, and a warning pill when the first fetch failed.
 */
export const ForkChatWakeupsPillView: React.FC<
	ForkChatWakeupsPillViewProps
> = ({ wakeups, error, onUpdate, pendingId, updateError }) => {
	const [isOpen, setIsOpen] = useState(false);
	const openWakeups = wakeups?.filter(isOpenWakeup);
	if (!openWakeups) {
		return error ? (
			<ComposerPillError
				label="Wake-ups failed to load"
				heading="Wake-ups"
				message={getErrorMessage(error, "Failed to load the wake-ups.")}
			/>
		) : null;
	}
	// Stay mounted while the popover is open, so canceling the last wake-up
	// does not unmount the popover that holds keyboard focus.
	if (openWakeups.length === 0 && !isOpen) {
		return null;
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
				<p className="mb-0 mt-2 text-content-warning">
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
							isPending={wakeup.id === pendingId}
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
		case "events": {
			const count = wakeup.events.length;
			return `Waiting for ${count} ${count === 1 ? "event" : "events"}`;
		}
	}
};

const formatWakeupEvent = (event: ChatWakeupEvent): string => {
	if (event.chat_id) {
		return `child agent ${event.chat_id.slice(0, 8)}`;
	}
	if (event.url) {
		return event.url;
	}
	return event.type;
};

type ForkChatWakeupRowProps = {
	wakeup: ChatWakeup;
	onUpdate: (wakeup: ChatWakeup, status: ChatWakeupStatus) => void;
	isPending: boolean;
	errorMessage?: string;
};

const ForkChatWakeupRow: React.FC<ForkChatWakeupRowProps> = ({
	wakeup,
	onUpdate,
	isPending,
	errorMessage,
}) => {
	const schedule = formatWakeupSchedule(wakeup);
	const toggleLabel = wakeup.status === "active" ? "Pause" : "Resume";
	const statusParts: string[] = [];
	if (wakeup.status === "active" && wakeup.next_run_at) {
		statusParts.push(`Next run ${relativeTime(wakeup.next_run_at)}`);
	}
	if (wakeup.status === "paused") {
		statusParts.push(
			wakeup.status_reason ? `Paused: ${wakeup.status_reason}` : "Paused",
		);
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
							className="text-content-secondary"
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
					disabled={isPending}
					aria-label={`${toggleLabel} ${schedule}`}
					onClick={() =>
						onUpdate(wakeup, wakeup.status === "active" ? "paused" : "active")
					}
				>
					{toggleLabel}
				</Button>
				<Button
					variant="subtle"
					size="sm"
					disabled={isPending}
					aria-label={`Cancel ${schedule}`}
					onClick={() => onUpdate(wakeup, "canceled")}
				>
					Cancel
				</Button>
			</div>
			{errorMessage && (
				<p className="mb-0 mt-0 text-content-destructive">{errorMessage}</p>
			)}
		</li>
	);
};
