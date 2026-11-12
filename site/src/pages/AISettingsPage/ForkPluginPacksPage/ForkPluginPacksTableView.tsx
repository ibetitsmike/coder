// This file is fork code: the fork-plugin-packs experiment's organization
// plugin pack table.

import { EllipsisVerticalIcon, GlobeIcon, PlusIcon } from "lucide-react";
import type { ForkPluginPack } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "#/components/DropdownMenu/DropdownMenu";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "#/components/Table/Table";
import { TableEmpty } from "#/components/TableEmpty/TableEmpty";
import { TableLoader } from "#/components/TableLoader/TableLoader";
import { RetryButton } from "#/pages/AgentsPage/components/RetryButton";
import { SectionHeader } from "#/pages/AgentsPage/components/SectionHeader";
import { formatUpdatedAt } from "#/pages/AgentsPage/components/SkillsTableView";
import type { useDialogFocusReturn } from "#/pages/AgentsPage/components/useDialogFocusReturn";
import type { SkillAccess } from "#/pages/AgentsPage/utils/skills";

const title = "Plugin packs";

type FocusReturn = Pick<
	ReturnType<typeof useDialogFocusReturn>,
	"rememberTrigger" | "setPrimaryFallback" | "setSecondaryFallback"
>;

type ForkPluginPacksTableViewProps = {
	packs: readonly ForkPluginPack[];
	access: SkillAccess;
	action?: React.ReactNode;
	error: unknown;
	isLoadingError: boolean;
	isLoading: boolean;
	isRetrying: boolean;
	onRetry: () => void;
	updatingPackIds: ReadonlySet<string>;
	/** Registers the buttons that open dialogs, so closing one restores focus. */
	focusReturn?: FocusReturn;
	/** Focus target of last resort when no row or install button remains. */
	tableRef?: React.Ref<HTMLTableElement>;
	onInstall: () => void;
	onEditSources: () => void;
	onUpdate: (pack: ForkPluginPack) => void;
	onDelete: (pack: ForkPluginPack) => void;
};

type ForkPluginPackEntryProps = {
	pack: ForkPluginPack;
	access: SkillAccess;
	isUpdating: boolean;
	isFirst: boolean;
	focusReturn?: FocusReturn;
	onUpdate: (pack: ForkPluginPack) => void;
	onDelete: (pack: ForkPluginPack) => void;
};

const ForkPluginPackActions: React.FC<ForkPluginPackEntryProps> = ({
	pack,
	access,
	isUpdating,
	isFirst,
	focusReturn,
	onUpdate,
	onDelete,
}) => (
	<DropdownMenu>
		<DropdownMenuTrigger asChild>
			<Button
				size="icon"
				variant="subtle"
				aria-label={`More actions for ${pack.name}`}
				ref={isFirst ? focusReturn?.setPrimaryFallback : undefined}
				onPointerDown={focusReturn?.rememberTrigger}
				onKeyDown={focusReturn?.rememberTrigger}
			>
				<EllipsisVerticalIcon aria-hidden="true" />
			</Button>
		</DropdownMenuTrigger>
		<DropdownMenuContent align="end">
			{access.update && (
				<DropdownMenuItem
					disabled={isUpdating}
					onClick={() => {
						if (!isUpdating) {
							onUpdate(pack);
						}
					}}
				>
					{isUpdating
						? "Updating…"
						: `Update to the latest commit of ${pack.ref}`}
				</DropdownMenuItem>
			)}
			{access.delete && (
				<DropdownMenuItem
					className="text-content-destructive focus:text-content-destructive"
					disabled={isUpdating}
					onClick={() => onDelete(pack)}
				>
					Delete&hellip;
				</DropdownMenuItem>
			)}
		</DropdownMenuContent>
	</DropdownMenu>
);

const ForkPluginPackRow: React.FC<ForkPluginPackEntryProps> = ({
	pack,
	access,
	isUpdating,
	isFirst,
	focusReturn,
	onUpdate,
	onDelete,
}) => (
	<TableRow>
		<TableCell className="max-w-48 truncate" title={pack.name}>
			{pack.name}
		</TableCell>
		<TableCell
			className="w-full max-w-0 truncate"
			title={`${pack.source_url} ${pack.source_path}`.trim()}
		>
			{pack.source_url}
			{pack.source_path && (
				<span className="text-content-secondary"> / {pack.source_path}</span>
			)}
		</TableCell>
		<TableCell className="whitespace-nowrap">
			{pack.ref}{" "}
			<code className="text-content-secondary" title={pack.commit_sha}>
				{pack.commit_sha.slice(0, 10)}
			</code>
		</TableCell>
		<TableCell className="hidden whitespace-nowrap sm:table-cell">
			{formatUpdatedAt(pack.updated_at)}
		</TableCell>
		{(access.update || access.delete) && (
			<TableCell className="text-right">
				<ForkPluginPackActions
					pack={pack}
					access={access}
					isUpdating={isUpdating}
					isFirst={isFirst}
					focusReturn={focusReturn}
					onUpdate={onUpdate}
					onDelete={onDelete}
				/>
			</TableCell>
		)}
	</TableRow>
);

/**
 * A failed refetch keeps the cached rows, even an empty list, and shows the
 * alert above them.
 */
export const ForkPluginPacksTableView: React.FC<
	ForkPluginPacksTableViewProps
> = ({
	packs,
	access,
	action,
	error,
	isLoadingError,
	isLoading,
	isRetrying,
	onRetry,
	updatingPackIds,
	focusReturn,
	tableRef,
	onInstall,
	onEditSources,
	onUpdate,
	onDelete,
}) => {
	const hasRowActions = access.update || access.delete;

	let tableBody: React.ReactNode;
	if (isLoading) {
		tableBody = <TableLoader />;
	} else if (isLoadingError) {
		tableBody = (
			<TableEmpty
				message="Failed to load plugin packs"
				cta={
					<RetryButton
						variant="outline"
						isRetrying={isRetrying}
						onRetry={onRetry}
					/>
				}
			/>
		);
	} else if (packs.length === 0) {
		tableBody = (
			<TableEmpty
				message="No plugin packs installed"
				description="Install a Claude Code or Cursor plugin from Git, then enable it on a project or chat to use its skills and agents."
			/>
		);
	} else {
		tableBody = packs.map((pack, index) => (
			<ForkPluginPackRow
				key={pack.id}
				pack={pack}
				access={access}
				isUpdating={updatingPackIds.has(pack.id)}
				isFirst={index === 0}
				focusReturn={focusReturn}
				onUpdate={onUpdate}
				onDelete={onDelete}
			/>
		));
	}

	return (
		<div className="flex flex-col gap-8">
			<SectionHeader
				label={title}
				description="Plugin packs installed in this organization. Projects and chats choose which packs they use."
				action={
					<div className="flex flex-col gap-2 sm:flex-row sm:items-center">
						{action}
						{access.update && (
							<Button
								variant="outline"
								onClick={(event) => {
									focusReturn?.rememberTrigger(event);
									onEditSources();
								}}
							>
								<GlobeIcon aria-hidden="true" />
								Sources
							</Button>
						)}
						{access.create && (
							<Button
								ref={focusReturn?.setSecondaryFallback}
								variant="outline"
								onClick={(event) => {
									focusReturn?.rememberTrigger(event);
									onInstall();
								}}
							>
								<PlusIcon aria-hidden="true" />
								Install plugin pack
							</Button>
						)}
					</div>
				}
			/>
			{Boolean(error) && (
				<ErrorAlert
					error={error}
					actions={
						isLoadingError ? undefined : (
							<RetryButton isRetrying={isRetrying} onRetry={onRetry} />
						)
					}
				/>
			)}
			<Table ref={tableRef} tabIndex={-1} aria-label={title}>
				<TableHeader>
					<TableRow>
						<TableHead className="whitespace-nowrap">Name</TableHead>
						<TableHead className="w-full">Source</TableHead>
						<TableHead className="whitespace-nowrap">Ref</TableHead>
						<TableHead className="hidden whitespace-nowrap sm:table-cell">
							Updated
						</TableHead>
						{hasRowActions && (
							<TableHead className="w-14">
								<span className="sr-only">Actions</span>
							</TableHead>
						)}
					</TableRow>
				</TableHeader>
				<TableBody size="lg">{tableBody}</TableBody>
			</Table>
		</div>
	);
};
