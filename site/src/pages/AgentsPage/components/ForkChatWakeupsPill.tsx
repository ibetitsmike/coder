import { AlarmClockIcon, TriangleAlertIcon } from "lucide-react";
import type React from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorMessage } from "#/api/errors";
import { chatWakeups, updateChatWakeup } from "#/api/queries/forkChatWakeups";
import type { ChatWakeup, ChatWakeupStatus } from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import {
	Popover,
	PopoverContent,
	PopoverTrigger,
} from "#/components/Popover/Popover";
import { relativeTime } from "#/utils/time";

// This file is fork code: the composer pill for the fork-chat-wakeups
// experiment lives here so upstream chat files stay unchanged.

type ForkChatWakeupsPillProps = {
	chatId: string;
};

/**
 * Shows the chat's open wake-ups as a pill with a popover. It renders nothing
 * while the wake-ups load or none is open.
 */
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

type ForkChatWakeupsPillViewProps = {
	wakeups: readonly ChatWakeup[] | undefined;
	error: unknown;
	onUpdate: (wakeup: ChatWakeup, status: ChatWakeupStatus) => void;
	pendingId?: string;
	updateError?: { id: string; message: string };
};

/**
 * Renders the wake-ups pill. Renders nothing while the wake-ups load or no
 * open wake-up remains, and a warning pill when the fetch failed.
 */
export const ForkChatWakeupsPillView = ({
	wakeups,
	error,
	onUpdate,
	pendingId,
	updateError,
}: ForkChatWakeupsPillViewProps) => {
	const open = wakeups?.filter(isOpenWakeup);
	if (open) {
		return open.length > 0 ? (
			<ForkChatWakeupsListPill
				open={open}
				onUpdate={onUpdate}
				pendingId={pendingId}
				updateError={updateError}
			/>
		) : null;
	}
	return error ? <ForkChatWakeupsErrorPill error={error} /> : null;
};

type ForkChatWakeupsPopoverProps = {
	label: string;
	trigger: React.ReactNode;
	children: React.ReactNode;
};

const ForkChatWakeupsPopover: React.FC<ForkChatWakeupsPopoverProps> = ({
	label,
	trigger,
	children,
}) => {
	return (
		<Popover>
			<PopoverTrigger asChild>
				<button
					type="button"
					aria-label={label}
					className="inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border-none bg-transparent px-1 text-xs outline-hidden transition-colors hover:bg-surface-secondary/60 focus-visible:ring-2 focus-visible:ring-content-link"
				>
					{trigger}
				</button>
			</PopoverTrigger>
			<PopoverContent
				side="top"
				align="end"
				className="mobile-full-width-dropdown w-76 p-3 text-xs font-normal text-content-primary"
			>
				<h3 className="m-0 text-sm font-semibold">Wake-ups</h3>
				{children}
			</PopoverContent>
		</Popover>
	);
};

type ForkChatWakeupsListPillProps = {
	open: readonly ChatWakeup[];
	onUpdate: (wakeup: ChatWakeup, status: ChatWakeupStatus) => void;
	pendingId?: string;
	updateError?: { id: string; message: string };
};

const ForkChatWakeupsListPill: React.FC<ForkChatWakeupsListPillProps> = ({
	open,
	onUpdate,
	pendingId,
	updateError,
}) => {
	return (
		<ForkChatWakeupsPopover
			label={`Wake-ups, ${open.length} open`}
			trigger={
				<>
					<AlarmClockIcon
						aria-hidden="true"
						className="size-3.5 shrink-0 text-content-secondary"
					/>
					<span className="text-content-secondary tabular-nums">
						{open.length}
					</span>
				</>
			}
		>
			<ul className="m-0 mt-2 flex list-none flex-col gap-3 p-0">
				{open.map((wakeup) => (
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
		</ForkChatWakeupsPopover>
	);
};

/**
 * Formats an interval in seconds as a compact duration, like 45s, 5m,
 * 1h 30m or 2d.
 */
const formatWakeupInterval = (seconds: number): string => {
	const days = Math.floor(seconds / 86_400);
	const hours = Math.floor((seconds % 86_400) / 3_600);
	const minutes = Math.floor((seconds % 3_600) / 60);
	const remainingSeconds = seconds % 60;
	const parts: string[] = [];
	if (days > 0) {
		parts.push(`${days}d`);
	}
	if (hours > 0) {
		parts.push(`${hours}h`);
	}
	if (minutes > 0) {
		parts.push(`${minutes}m`);
	}
	if (remainingSeconds > 0) {
		parts.push(`${remainingSeconds}s`);
	}
	return parts.length > 0 ? parts.join(" ") : "0s";
};

/** Describes what the wake-up waits for, like "Every 5m" or "Once". */
const formatWakeupSchedule = (wakeup: ChatWakeup): string => {
	switch (wakeup.kind) {
		case "interval":
			return `Every ${formatWakeupInterval(wakeup.interval_seconds ?? 0)}`;
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

const formatWakeupEvent = (event: ChatWakeup["events"][number]): string => {
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
			<div>{formatWakeupSchedule(wakeup)}</div>
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
				className="min-w-0 wrap-break-word leading-5 line-clamp-2"
			>
				{wakeup.prompt}
			</span>
			<div className="text-content-secondary">{statusParts.join(", ")}</div>
			<div className="flex items-center gap-1.5">
				<Button
					variant="outline"
					size="sm"
					disabled={isPending}
					onClick={() =>
						onUpdate(wakeup, wakeup.status === "active" ? "paused" : "active")
					}
				>
					{wakeup.status === "active" ? "Pause" : "Resume"}
				</Button>
				<Button
					variant="subtle"
					size="sm"
					disabled={isPending}
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

type ForkChatWakeupsErrorPillProps = {
	error: unknown;
};

const ForkChatWakeupsErrorPill: React.FC<ForkChatWakeupsErrorPillProps> = ({
	error,
}) => {
	return (
		<ForkChatWakeupsPopover
			label="Wake-ups failed to load"
			trigger={
				<TriangleAlertIcon
					aria-hidden="true"
					className="size-3.5 shrink-0 text-content-warning"
				/>
			}
		>
			<p className="mb-0 mt-2 text-content-secondary">
				{getErrorMessage(error, "Failed to load the wake-ups.")}
			</p>
		</ForkChatWakeupsPopover>
	);
};
