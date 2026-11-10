// This file is fork code: the fork-agent-definitions experiment's table.

import { EllipsisVerticalIcon } from "lucide-react";
import { useRef } from "react";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import type { ForkAgentDefinitionMetadata } from "#/api/typesGenerated";
import { Alert, AlertDescription } from "#/components/Alert/Alert";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import { ConfirmDialog } from "#/components/Dialog/ConfirmDialog/ConfirmDialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "#/components/DropdownMenu/DropdownMenu";
import { Switch } from "#/components/Switch/Switch";
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
import { RetryButton } from "../../components/RetryButton";
import { SectionHeader } from "../../components/SectionHeader";
import { formatUpdatedAt } from "../../components/SkillsTableView";
import { useDialogFocusReturn } from "../../components/useDialogFocusReturn";

const title = "Agent definitions";

/**
 * The open delete confirmation. While isDeleting, onClose should keep the
 * dialog open, because a failure is reported only as error in this dialog.
 */
export type ForkAgentDefinitionDeleteState = {
	definition: ForkAgentDefinitionMetadata;
	error?: unknown;
	isDeleting: boolean;
	onConfirm: () => void;
	onClose: () => void;
};

type ForkAgentDefinitionsTableViewProps = {
	definitions: readonly ForkAgentDefinitionMetadata[];
	/** Rendered in the section header, for example an organization picker. */
	action?: React.ReactNode;
	error: unknown;
	isLoading: boolean;
	isRetrying: boolean;
	onRetry: () => void;
	/** The enabled value each in-flight toggle sends, keyed by name. */
	pendingToggles?: Readonly<Record<string, boolean>>;
	onToggleEnabled: (name: string, enabled: boolean) => void;
	onDelete: (definition: ForkAgentDefinitionMetadata) => void;
	deleteState?: ForkAgentDefinitionDeleteState;
};

export const ForkAgentDefinitionsTableView: React.FC<
	ForkAgentDefinitionsTableViewProps
> = ({
	definitions,
	action,
	error,
	isLoading,
	isRetrying,
	onRetry,
	pendingToggles = {},
	onToggleEnabled,
	onDelete,
	deleteState,
}) => {
	const { rememberTrigger, restoreFocus, setPrimaryFallback } =
		useDialogFocusReturn();
	// Deleting the only row removes every button on the page in a
	// single-organization deployment, so focus falls back to the table.
	const tableRef = useRef<HTMLTableElement>(null);
	const restoreDeleteFocus = (event: Event) => {
		restoreFocus(event);
		if (!event.defaultPrevented && tableRef.current?.isConnected) {
			event.preventDefault();
			tableRef.current.focus();
		}
	};
	const deleteErrorDetail = deleteState?.error
		? getErrorDetail(deleteState.error)
		: undefined;

	let tableBody: React.ReactNode;
	if (isLoading) {
		tableBody = <TableLoader />;
	} else if (definitions.length === 0 && error) {
		tableBody = (
			<TableEmpty
				message="Failed to load agent definitions"
				cta={
					<RetryButton
						variant="outline"
						isRetrying={isRetrying}
						onRetry={onRetry}
					/>
				}
			/>
		);
	} else if (definitions.length === 0) {
		tableBody = (
			<TableEmpty message="No personal agent definitions in this organization yet." />
		);
	} else {
		tableBody = definitions.map((definition, index) => {
			const pendingEnabled = pendingToggles[definition.name];
			return (
				<TableRow key={definition.id}>
					<TableCell className="max-w-48 truncate" title={definition.name}>
						{definition.name}
					</TableCell>
					<TableCell
						className="w-full max-w-0 truncate"
						title={definition.description || undefined}
					>
						{definition.description || (
							<span className="text-content-disabled">No description</span>
						)}
					</TableCell>
					<TableCell>
						<Switch
							aria-label={`Enable ${definition.name}`}
							checked={pendingEnabled ?? definition.enabled}
							disabled={pendingEnabled !== undefined}
							onCheckedChange={(enabled) =>
								onToggleEnabled(definition.name, enabled)
							}
						/>
					</TableCell>
					<TableCell className="hidden whitespace-nowrap sm:table-cell">
						{formatUpdatedAt(definition.updated_at)}
					</TableCell>
					<TableCell className="text-right">
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<Button
									size="icon"
									variant="subtle"
									aria-label={`More actions for ${definition.name}`}
									// A deleted row takes its menu button with it, so focus
									// returns to the first remaining row's button.
									ref={index === 0 ? setPrimaryFallback : undefined}
									onPointerDown={rememberTrigger}
									onKeyDown={rememberTrigger}
								>
									<EllipsisVerticalIcon aria-hidden="true" />
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end">
								<DropdownMenuItem
									className="text-content-destructive focus:text-content-destructive"
									onClick={() => onDelete(definition)}
								>
									Delete&hellip;
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					</TableCell>
				</TableRow>
			);
		});
	}

	return (
		<div className="flex flex-col gap-8">
			<SectionHeader
				label={title}
				description="Custom agent types your chats can spawn with spawn_agent in this organization. Each definition is a Claude Code or Cursor agent markdown file."
				action={action}
			/>

			{Boolean(error) && (
				<ErrorAlert
					error={error}
					actions={
						definitions.length > 0 && (
							<RetryButton isRetrying={isRetrying} onRetry={onRetry} />
						)
					}
				/>
			)}

			<Table ref={tableRef} aria-label={title} tabIndex={-1}>
				<TableHeader>
					<TableRow>
						<TableHead className="whitespace-nowrap">Name</TableHead>
						<TableHead className="w-full">Description</TableHead>
						<TableHead className="whitespace-nowrap">Enabled</TableHead>
						<TableHead className="hidden whitespace-nowrap sm:table-cell">
							Updated
						</TableHead>
						<TableHead className="w-14">
							<span className="sr-only">Actions</span>
						</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody size="lg">{tableBody}</TableBody>
			</Table>

			{deleteState && (
				<ConfirmDialog
					type="delete"
					open
					onClose={deleteState.onClose}
					onCloseAutoFocus={restoreDeleteFocus}
					title={`Delete ${deleteState.definition.name}?`}
					confirmText="Delete"
					description={
						<>
							<p className="m-0">
								Your personal definition {deleteState.definition.name} will be
								deleted. Organization, project, and workspace definitions with
								the same name are not affected. This action cannot be undone.
							</p>
							{Boolean(deleteState.error) && (
								<Alert severity="error" className="mt-3">
									<AlertDescription>
										{getErrorMessage(
											deleteState.error,
											"Failed to delete agent definition.",
										)}
										{deleteErrorDetail ? ` ${deleteErrorDetail}` : ""}
									</AlertDescription>
								</Alert>
							)}
						</>
					}
					onConfirm={deleteState.onConfirm}
					confirmLoading={deleteState.isDeleting}
				/>
			)}
		</div>
	);
};
