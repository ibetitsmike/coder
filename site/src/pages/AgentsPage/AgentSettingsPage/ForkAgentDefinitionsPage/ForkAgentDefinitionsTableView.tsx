// This file is fork code: the fork-agent-definitions experiment's table.

import { EllipsisVerticalIcon, PlusIcon } from "lucide-react";
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
import { SkillEnabledStatus } from "../../components/SkillEnabledSwitch";
import { formatUpdatedAt } from "../../components/SkillsTableView";
import { useDialogFocusReturn } from "../../components/useDialogFocusReturn";
import { fullSkillAccess, type SkillAccess } from "../../utils/skills";
import {
	ForkAgentDefinitionEditorDialog,
	type ForkAgentDefinitionEditorState,
} from "./ForkAgentDefinitionEditorDialog";

const title = "Agent definitions";

/**
 * The open delete confirmation. While isDeleting, onClose should keep the
 * dialog open, because a failure is reported only as error in this dialog.
 */
export type ForkAgentDefinitionDeleteState = {
	definition: ForkAgentDefinitionMetadata;
	message: string;
	error?: unknown;
	isDeleting: boolean;
	onConfirm: () => void;
	onClose: () => void;
};

type ForkAgentDefinitionsTableViewProps = {
	definitions: readonly ForkAgentDefinitionMetadata[];
	description: string;
	emptyMessage: string;
	/**
	 * Hides the create, edit, and delete controls the user may not use, and
	 * shows the enabled state as a static status without update access.
	 */
	access?: SkillAccess;
	/** Rendered in the section header, for example an organization picker. */
	action?: React.ReactNode;
	error: unknown;
	isLoading: boolean;
	isRetrying: boolean;
	onRetry: () => void;
	/** The enabled value each in-flight toggle sends, keyed by name. */
	pendingToggles?: Readonly<Record<string, boolean>>;
	onToggleEnabled: (name: string, enabled: boolean) => void;
	onCreate: () => void;
	onEdit: (definition: ForkAgentDefinitionMetadata) => void;
	onDelete: (definition: ForkAgentDefinitionMetadata) => void;
	editorState?: ForkAgentDefinitionEditorState;
	deleteState?: ForkAgentDefinitionDeleteState;
};

type EnabledCellProps = {
	definition: ForkAgentDefinitionMetadata;
	canUpdate: boolean;
	pendingEnabled: boolean | undefined;
	onToggleEnabled: (name: string, enabled: boolean) => void;
};

const EnabledCell: React.FC<EnabledCellProps> = ({
	definition,
	canUpdate,
	pendingEnabled,
	onToggleEnabled,
}) => {
	if (!canUpdate) {
		return <SkillEnabledStatus enabled={definition.enabled} />;
	}
	return (
		<Switch
			aria-label={`Enable ${definition.name}`}
			checked={pendingEnabled ?? definition.enabled}
			disabled={pendingEnabled !== undefined}
			onCheckedChange={(enabled) => onToggleEnabled(definition.name, enabled)}
		/>
	);
};

export const ForkAgentDefinitionsTableView: React.FC<
	ForkAgentDefinitionsTableViewProps
> = ({
	definitions,
	description,
	emptyMessage,
	access = fullSkillAccess,
	action,
	error,
	isLoading,
	isRetrying,
	onRetry,
	pendingToggles = {},
	onToggleEnabled,
	onCreate,
	onEdit,
	onDelete,
	editorState,
	deleteState,
}) => {
	const {
		rememberTrigger,
		restoreFocus,
		setPrimaryFallback,
		setSecondaryFallback,
	} = useDialogFocusReturn();
	const deleteErrorDetail = deleteState?.error
		? getErrorDetail(deleteState.error)
		: undefined;

	const hasRowActions = access.update || access.delete;

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
		tableBody = <TableEmpty message={emptyMessage} />;
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
						<EnabledCell
							definition={definition}
							canUpdate={access.update}
							pendingEnabled={pendingEnabled}
							onToggleEnabled={onToggleEnabled}
						/>
					</TableCell>
					<TableCell className="hidden whitespace-nowrap sm:table-cell">
						{formatUpdatedAt(definition.updated_at)}
					</TableCell>
					{hasRowActions && (
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
									{access.update && (
										<DropdownMenuItem onClick={() => onEdit(definition)}>
											Edit
										</DropdownMenuItem>
									)}
									{access.delete && (
										<DropdownMenuItem
											className="text-content-destructive focus:text-content-destructive"
											onClick={() => onDelete(definition)}
										>
											Delete&hellip;
										</DropdownMenuItem>
									)}
								</DropdownMenuContent>
							</DropdownMenu>
						</TableCell>
					)}
				</TableRow>
			);
		});
	}

	return (
		<div className="flex flex-col gap-8">
			<SectionHeader
				label={title}
				description={description}
				action={
					<div className="flex flex-col gap-2 sm:flex-row sm:items-center">
						{action}
						{access.create && (
							<Button
								ref={setSecondaryFallback}
								variant="outline"
								onClick={(event) => {
									rememberTrigger(event);
									onCreate();
								}}
							>
								<PlusIcon aria-hidden="true" />
								New agent definition
							</Button>
						)}
					</div>
				}
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

			<Table aria-label={title}>
				<TableHeader>
					<TableRow>
						<TableHead className="whitespace-nowrap">Name</TableHead>
						<TableHead className="w-full">Description</TableHead>
						<TableHead className="whitespace-nowrap">Enabled</TableHead>
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

			{editorState && (
				<ForkAgentDefinitionEditorDialog
					{...editorState}
					onCloseAutoFocus={restoreFocus}
				/>
			)}

			{deleteState && (
				<ConfirmDialog
					type="delete"
					open
					onClose={deleteState.onClose}
					onCloseAutoFocus={restoreFocus}
					title={`Delete ${deleteState.definition.name}?`}
					confirmText="Delete"
					description={
						<>
							<p className="m-0">{deleteState.message}</p>
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
