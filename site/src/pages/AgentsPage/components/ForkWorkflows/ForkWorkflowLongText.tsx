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
};

export const ForkWorkflowLongText: React.FC<ForkWorkflowLongTextProps> = ({
	label,
	text,
	isError = false,
}) => {
	const [expanded, setExpanded] = useState(false);
	const preview = textPreview(text);
	const textClassName = cn(
		"m-0 whitespace-pre-wrap wrap-break-word bg-transparent p-0 font-mono text-xs leading-5",
		isError ? "text-content-destructive" : "text-content-primary",
	);

	const header = (
		<div className="text-xs font-medium text-content-secondary">{label}</div>
	);
	if (preview === null) {
		return (
			<section aria-label={label} className="mt-1.5 flex min-w-0 flex-col gap-0.5">
				{header}
				<pre className={textClassName}>{text}</pre>
			</section>
		);
	}
	return (
		<section aria-label={label} className="mt-1.5 flex min-w-0 flex-col gap-0.5">
			{header}
			{expanded ? (
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
			<Button
				variant="subtle"
				size="xs"
				className="w-fit px-0"
				aria-expanded={expanded}
				onClick={() => setExpanded((value) => !value)}
			>
				{expanded ? "Show less" : "Show more"}
			</Button>
		</section>
	);
};
