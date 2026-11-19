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
	// hide what they were reading.
	const [expanded, setExpanded] = useState(() => textPreview(text) === null);
	const preview = textPreview(text);
	const textClassName = cn(
		"m-0 whitespace-pre-wrap wrap-break-word bg-transparent font-mono text-xs leading-5",
		isError ? "text-content-destructive" : "text-content-primary",
	);
	// A block that grew past the threshold while live keeps the same
	// expanded pre when the run finishes: swapping it for a scroll area
	// then would drop keyboard focus and reset the scroll position right
	// as the user reads it, and role="log" would also re-announce the
	// added subtree. The live flag only moves the toggle out of the
	// region while the block announces additions.
	const isLiveExpanded = expanded && preview !== null;

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
			{preview === null ? (
				<pre className={textClassName}>{text}</pre>
			) : (
				<>
					{isLiveExpanded ? (
						// The same element the preview rendered, grown to the
						// scrollable height instead of swapped for a scroll
						// area, so the live region keeps announcing additions.
						<pre
							role="region"
							aria-label={`Full ${label.toLowerCase()}`}
							// oxlint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- a scrolling region must be keyboard reachable.
							tabIndex={0}
							className={cn(textClassName, "max-h-96 overflow-y-auto")}
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
					{!isLive && (
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
				</>
			)}
		</section>
	);
};
