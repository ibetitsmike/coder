import { cn } from "cn";
import { FolderIcon, type LucideIcon } from "lucide-react";
import {
	Tooltip,
	TooltipContent,
	TooltipProvider,
	TooltipTrigger,
} from "#/components/Tooltip/Tooltip";

type ContextResourceTreeProps = { children: React.ReactNode };

/** Shares spacing and tooltip timing across context resource branches. */
export const ContextResourceTree: React.FC<ContextResourceTreeProps> = ({
	children,
}) => (
	<TooltipProvider delayDuration={300}>
		<div className="flex flex-col gap-1.5">{children}</div>
	</TooltipProvider>
);

type ContextResourceTreeItemProps = {
	className?: string;
	icon?: LucideIcon;
	name: string;
	title?: string;
	description?: string;
	children?: React.ReactNode;
};

/**
 * Keeps nested resources aligned and optionally describes a row on hover or
 * keyboard focus.
 */
export const ContextResourceTreeItem: React.FC<
	ContextResourceTreeItemProps
> = ({ icon: Icon, name, title, description, children, className }) => {
	const content = (
		<>
			{Icon && (
				<span className="flex size-4 shrink-0 items-center justify-center">
					<Icon className="size-3" />
				</span>
			)}
			<span className="truncate">{name}</span>
		</>
	);
	const rowClassName = cn("flex min-w-0 items-center gap-1 py-px", className);
	// A described row is a button so keyboard users can focus it to read the
	// description.
	const row = description ? (
		<button
			type="button"
			className={cn(
				rowClassName,
				"cursor-default rounded-sm border-0 bg-transparent px-0 text-left font-[inherit] text-[length:inherit]",
				"focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link",
			)}
			title={title}
		>
			{content}
		</button>
	) : (
		<div className={rowClassName} title={title}>
			{content}
		</div>
	);
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			{description ? (
				<Tooltip>
					<TooltipTrigger asChild>{row}</TooltipTrigger>
					<TooltipContent
						side="right"
						sideOffset={4}
						className="max-w-48 text-xs"
					>
						{description}
					</TooltipContent>
				</Tooltip>
			) : (
				row
			)}
			{children && <div className="ml-5 flex flex-col gap-1.5">{children}</div>}
		</div>
	);
};

type ContextResourceTreeDirectoryProps = {
	dir: string;
	children: React.ReactNode;
};

/** Omits the directory level for resources without a parent path. */
export const ContextResourceTreeDirectory: React.FC<
	ContextResourceTreeDirectoryProps
> = ({ dir, children }) =>
	dir ? (
		<ContextResourceTreeItem icon={FolderIcon} name={dir} title={dir}>
			{children}
		</ContextResourceTreeItem>
	) : (
		children
	);
