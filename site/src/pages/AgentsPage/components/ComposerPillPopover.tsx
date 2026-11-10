import { TriangleAlertIcon } from "lucide-react";
import type React from "react";
import {
	Popover,
	PopoverContent,
	PopoverTrigger,
} from "#/components/Popover/Popover";

type ComposerPillPopoverProps = {
	label: string;
	heading: string;
	trigger: React.ReactNode;
	children: React.ReactNode;
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
};

/** A compact composer pill that opens a popover with a heading. */
export const ComposerPillPopover: React.FC<ComposerPillPopoverProps> = ({
	label,
	heading,
	trigger,
	children,
	open,
	onOpenChange,
}) => {
	return (
		<Popover open={open} onOpenChange={onOpenChange}>
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
				<h3 className="m-0 text-sm font-semibold">{heading}</h3>
				{children}
			</PopoverContent>
		</Popover>
	);
};

type ComposerPillErrorProps = {
	label: string;
	heading: string;
	message: string;
};

/** A warning pill whose popover shows why the pill's data failed to load. */
export const ComposerPillError: React.FC<ComposerPillErrorProps> = ({
	label,
	heading,
	message,
}) => {
	return (
		<ComposerPillPopover
			label={label}
			heading={heading}
			trigger={
				<TriangleAlertIcon
					aria-hidden="true"
					className="size-3.5 shrink-0 text-content-warning"
				/>
			}
		>
			<p className="mb-0 mt-2 text-content-secondary">{message}</p>
		</ComposerPillPopover>
	);
};
