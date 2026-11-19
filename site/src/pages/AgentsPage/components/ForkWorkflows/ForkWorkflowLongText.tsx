// This file is fork code: a labeled block of workflow text (script, args,
// result, logs) that previews long text (fork-workflows experiment).

import { cn } from "cn";
import type React from "react";
import { useState } from "react";
import { Button } from "#/components/Button/Button";
import { ScrollArea } from "#/components/ScrollArea/ScrollArea";

const previewChars = 280;
const previewLines = 8;
// Text a little over the limits shows in full: a Show more that reveals
// only a few more words costs a click for nothing.
const toleranceChars = 120;
const toleranceLines = 4;

/** Returns the preview of text, or null when the text shows in full. */
const textPreview = (text: string): string | null => {
	const lines = text.split("\n");
	// Code points, so a cut never splits a surrogate pair.
	const chars = Array.from(text);
	if (
		chars.length <= previewChars + toleranceChars &&
		lines.length <= previewLines + toleranceLines
	) {
		return null;
	}
	const firstLines = lines.slice(0, previewLines).join("\n");
	return Array.from(firstLines).slice(0, previewChars).join("").trimEnd();
};

type ForkWorkflowLongTextProps = {
	label: string;
	text: string;
	isError?: boolean;
	/** Marks a block whose text grows while it is on screen, such as logs. */
	isLive?: boolean;
};

export const ForkWorkflowLongText: React.FC<ForkWorkflowLongTextProps> = ({
	label,
	text,
	isError = false,
	isLive = false,
}) => {
	// Once the text shows in full, it stays in full: polling can grow
	// it past the threshold, and collapsing it under the user would
	// hide what they were reading. A live block has no Show more while
	// it grows, so it starts in full.
	const [expanded, setExpanded] = useState(
		() => isLive || textPreview(text) === null,
	);
	// Whether the block was live from its mount or while it was still
	// short. Such a block grows its own pre into the scroller, so focus,
	// scroll position and the live region's node survive; a block that was
	// long before it turned live, such as a resumed run's log, keeps the
	// scroll area it already shows.
	const [announcedLive, setAnnouncedLive] = useState(isLive);
	const preview = textPreview(text);
	// Set during render while the text is short, so the render that first
	// sees the long text already picks the grown pre.
	if (isLive && preview === null && !announcedLive) {
		setAnnouncedLive(true);
	}
	const textClassName = cn(
		"m-0 whitespace-pre-wrap wrap-break-word bg-transparent font-mono text-xs leading-5",
		isError ? "text-content-destructive" : "text-content-primary",
	);
	const isLiveExpanded = expanded && preview !== null && announcedLive;

	return (
		<section
			aria-label={label}
			className={cn(
				"mt-1.5 flex flex-col gap-0.5",
				// A full log scrolls on the pre itself; min-width 0 would let
				// that long line shrink the block to nothing.
				isLive ? "min-w-px" : "min-w-0",
			)}
			role={isLive ? "log" : undefined}
			aria-live={isLive ? "polite" : undefined}
		>
			<div className="text-xs font-medium text-content-secondary">{label}</div>
			{/* The short text and the grown live log share this one pre, so
			    crossing the threshold updates the node instead of replacing it. */}
			{preview === null || isLiveExpanded ? (
				// biome-ignore lint/a11y/useAriaPropsSupportedByRole: aria-label is set only together with role="region".
				<pre
					role={isLiveExpanded ? "region" : undefined}
					aria-label={
						isLiveExpanded ? `Full ${label.toLowerCase()}` : undefined
					}
					// oxlint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- a scrolling region must be keyboard reachable.
					tabIndex={isLiveExpanded ? 0 : undefined}
					className={cn(
						textClassName,
						isLiveExpanded && "max-h-96 overflow-y-auto",
					)}
				>
					{text}
				</pre>
			) : expanded ? (
				<ScrollArea
					className="rounded-md border"
					viewportClassName="max-h-96 p-2"
					viewportTabIndex={0}
					viewportAriaLabel={`Full ${label.toLowerCase()}`}
				>
					<pre className={textClassName}>{text}</pre>
				</ScrollArea>
			) : (
				<pre className={textClassName}>{`${preview}…`}</pre>
			)}
			{preview !== null && !(isLive && isLiveExpanded) && (
				<Button
					variant="subtle"
					size="xs"
					className="w-fit px-0"
					aria-expanded={expanded}
					aria-label={`${expanded ? "Show less" : "Show more"} of ${label}`}
					onClick={() => setExpanded((value) => !value)}
				>
					{expanded ? "Show less" : "Show more"}
				</Button>
			)}
		</section>
	);
};
