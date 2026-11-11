// This file is fork code: the fork-plugin-packs experiment's organization
// plugin pack table.

import { EllipsisVerticalIcon, PlusIcon } from "lucide-react";
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

const title = "Plugin packs";

/**
 * Which plugin pack actions to offer. The server checks plugin packs against
 * the organization skill permissions, so each field mirrors the matching
 * organization skill permission.
 */
export type ForkPluginPacksAccess = {
	create: boolean;
	update: boolean;
	delete: boolean;
};

type ForkPluginPacksTableViewProps = {
	packs: readonly ForkPluginPack[];
	access: ForkPluginPacksAccess;
	action?: React.ReactNode;
	error: unknown;
	/** True when the first load failed, so no cached packs exist. */
	isLoadingError: boolean;
	isLoading: boolean;
	isRetrying: boolean;
	onRetry: () => void;
	updatingPackIds: ReadonlySet<string>;
	/** Registers the buttons that open dialogs, so closing one restores focus. */
	focusReturn?: Pick<
		ReturnType<typeof useDialogFocusReturn>,
		"rememberTrigger" | "setPrimaryFallback" | "setSecondaryFallback"
	>;
	/**
	 * Focus target of last resort. It is focusable by script only, so closing a
	 * dialog can land on it when no row or install button is left.
	 */
	tableRef?: React.Ref<HTMLTableElement>;
	onInstall: () => void;
	onUpdate: (pack: ForkPluginPack) => void;
	onDelete: (pack: ForkPluginPack) => void;
};

/**
 * Lists an organization's installed plugin packs. It shows a loader while
 * `isLoading`, a retryable empty state when the first load fails, and a
 * retryable alert above the cached rows, even an empty list, when a later
 * refetch fails.
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
	onUpdate,
	onDelete,
}) => {
	const installAction = (ref?: React.Ref<HTMLButtonElement>) =>
		access.create && (
			<Button
				ref={ref}
				variant="outline"
				onClick={(event) => {
					focusReturn?.rememberTrigger(event);
					onInstall();
				}}
			>
				<PlusIcon aria-hidden="true" />
				Install plugin pack
			</Button>
		);
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
				cta={installAction()}
			/>
		);
	} else {
		tableBody = packs.map((pack, index) => (
			<TableRow key={pack.id}>
				<TableCell className="max-w-48 truncate" title={pack.name}>
					{pack.name}
				</TableCell>
				<TableCell
					className="w-full max-w-0 truncate"
					title={`${pack.source_url} ${pack.source_path}`.trim()}
				>
					{pack.source_url}
					{pack.source_path && (
						<span className="text-content-secondary">
							{" "}
							/ {pack.source_path}
						</span>
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
				{hasRowActions && (
					<TableCell className="text-right">
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<Button
									size="icon"
									variant="subtle"
									aria-label={`More actions for ${pack.name}`}
									// A deleted row takes its menu button with it, so focus
									// returns to the first remaining row's button.
									ref={
										index === 0 ? focusReturn?.setPrimaryFallback : undefined
									}
									onPointerDown={focusReturn?.rememberTrigger}
									onKeyDown={focusReturn?.rememberTrigger}
								>
									<EllipsisVerticalIcon aria-hidden="true" />
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end">
								{access.update && (
									<DropdownMenuItem
										disabled={updatingPackIds.has(pack.id)}
										onClick={() => {
											if (!updatingPackIds.has(pack.id)) {
												onUpdate(pack);
											}
										}}
									>
										{updatingPackIds.has(pack.id)
											? "Updating…"
											: `Update to the latest commit of ${pack.ref}`}
									</DropdownMenuItem>
								)}
								{access.delete && (
									<DropdownMenuItem
										className="text-content-destructive focus:text-content-destructive"
										disabled={updatingPackIds.has(pack.id)}
										onClick={() => onDelete(pack)}
									>
										Delete&hellip;
									</DropdownMenuItem>
								)}
							</DropdownMenuContent>
						</DropdownMenu>
					</TableCell>
				)}
			</TableRow>
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
						{installAction(focusReturn?.setSecondaryFallback)}
					</div>
				}
			/>
			{Boolean(error) && !isLoadingError && (
				<ErrorAlert
					error={error}
					actions={<RetryButton isRetrying={isRetrying} onRetry={onRetry} />}
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
