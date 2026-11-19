// This file is fork code: the fork-workflows experiment's saved workflows
// table.

import { EllipsisVerticalIcon, PlusIcon } from "lucide-react";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import type {
	ForkSavedWorkflow,
	ForkSavedWorkflowMetadata,
} from "#/api/typesGenerated";
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
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "#/components/Tooltip/Tooltip";
import { RetryButton } from "#/pages/AgentsPage/components/RetryButton";
import { SectionHeader } from "#/pages/AgentsPage/components/SectionHeader";
import { formatUpdatedAt } from "#/pages/AgentsPage/components/SkillsTableView";
import { useDialogFocusReturn } from "#/pages/AgentsPage/components/useDialogFocusReturn";
import {
	fullSkillAccess,
	type SkillAccess,
} from "#/pages/AgentsPage/utils/skills";
import { ForkSavedWorkflowDetailDialog } from "./ForkSavedWorkflowDetailDialog";
import { ForkSavedWorkflowEditorDialog } from "./ForkSavedWorkflowEditorDialog";
import { SAVED_WORKFLOW_MAX_PER_SCOPE } from "./savedWorkflowForm";
import type { ForkSavedWorkflowEditorState } from "./useForkSavedWorkflowEditor";

/**
 * The open delete confirmation. While isDeleting, onClose should keep the
 * dialog open, because a failure is reported only as error in this dialog.
 */
export type ForkSavedWorkflowDeleteState = {
	workflow: ForkSavedWorkflowMetadata;
	message: string;
	error: unknown;
	isDeleting: boolean;
	onConfirm: () => void;
	onClose: () => void;
};

/** Everything the saved workflows table view renders one section with. */
type ForkSavedWorkflowsTableViewProps = {
	/** The section title, for example "Personal workflows". */
	title: string;
	workflows: readonly ForkSavedWorkflowMetadata[];
	description: string;
	emptyMessage: string;
	/**
	 * Hides the create, edit, and delete controls the user may not use.
	 */
	access?: SkillAccess;
	/** Rendered in the section header, for example an organization picker. */
	action?: React.ReactNode;
	error: unknown;
	isLoading: boolean;
	isRetrying: boolean;
	onRetry: () => void;
	onCreate: () => void;
	/** Opens the read-only detail dialog from the workflow's name button. */
	onView: (workflow: ForkSavedWorkflowMetadata) => void;
	onEdit: (workflow: ForkSavedWorkflowMetadata) => void;
	onDelete: (workflow: ForkSavedWorkflowMetadata) => void;
	/**
	 * The caller opens its own dialog and passes onCloseAutoFocus to it so
	 * focus returns to the row's menu.
	 */
	onManagePermissions?: (
		workflow: ForkSavedWorkflowMetadata,
		onCloseAutoFocus: (event: Event) => void,
	) => void;
	/** The open editor dialog, when the table's hook holds one. */
	editorState: ForkSavedWorkflowEditorState | undefined;
	/** The open delete confirmation, when a row's menu opened one. */
	deleteState: ForkSavedWorkflowDeleteState | undefined;
	/** The open detail dialog, when a row's name button opened one. */
	detailState:
		| {
				workflow?: ForkSavedWorkflow;
				isLoading: boolean;
				error: unknown;
				onRetry?: () => void;
				isRetrying: boolean;
				onClose: () => void;
		  }
		| undefined;
};

type NewWorkflowButtonProps = {
	ref?: React.Ref<HTMLButtonElement>;
	/** The list holds the most workflows the server allows. */
	atLimit?: boolean;
	onClick: (event: React.MouseEvent<HTMLButtonElement>) => void;
};

/**
 * The button that opens the create dialog, with a fallback ref for focus.
 * At the limit it stays focusable but inert, so its tooltip can say why.
 */
const NewWorkflowButton: React.FC<NewWorkflowButtonProps> = ({
	ref,
	atLimit = false,
	onClick,
}) => {
	const button = (
		<Button
			ref={ref}
			variant="outline"
			aria-disabled={atLimit || undefined}
			onClick={(event) => {
				if (!atLimit) {
					onClick(event);
				}
			}}
		>
			<PlusIcon aria-hidden="true" />
			New workflow
		</Button>
	);
	if (!atLimit) {
		return button;
	}
	return (
		<Tooltip>
			<TooltipTrigger asChild>{button}</TooltipTrigger>
			<TooltipContent>
				This list holds {SAVED_WORKFLOW_MAX_PER_SCOPE} workflows, the limit.
				Delete one before you create another.
			</TooltipContent>
		</Tooltip>
	);
};

/** Lists saved workflows of one tier with its dialogs. */
export const ForkSavedWorkflowsTableView: React.FC<
	ForkSavedWorkflowsTableViewProps
> = ({
	title,
	workflows,
	description,
	emptyMessage,
	access = fullSkillAccess,
	action,
	error,
	isLoading,
	isRetrying,
	onRetry,
	onCreate,
	onView,
	onEdit,
	onDelete,
	onManagePermissions,
	editorState,
	deleteState,
	detailState,
}) => {
	const {
		rememberTrigger,
		restoreFocus,
		setPrimaryFallback,
		setSecondaryFallback,
	} = useDialogFocusReturn();

	const hasRowActions =
		access.update || access.delete || Boolean(onManagePermissions);

	let tableBody: React.ReactNode;
	if (isLoading) {
		tableBody = <TableLoader />;
	} else if (workflows.length === 0 && error) {
		tableBody = (
			<TableEmpty
				message="Failed to load saved workflows"
				cta={
					<RetryButton
						variant="outline"
						isRetrying={isRetrying}
						onRetry={onRetry}
					/>
				}
			/>
		);
	} else if (workflows.length === 0) {
		tableBody = (
			<TableEmpty
				message={emptyMessage}
				description="Chats run a saved workflow by name with run_workflow."
				cta={
					access.create && (
						<NewWorkflowButton
							onClick={(event) => {
								rememberTrigger(event);
								onCreate();
							}}
						/>
					)
				}
			/>
		);
	} else {
		tableBody = workflows.map((workflow) => (
			<SavedWorkflowRow
				key={workflow.id}
				workflow={workflow}
				access={access}
				hasRowActions={hasRowActions}
				// A deleted row takes its menu button with it, so focus falls
				// back to the first remaining row's menu button, or its name
				// button when the table has no row menus.
				isFirst={workflow.id === workflows[0]?.id}
				onView={onView}
				onEdit={onEdit}
				onDelete={onDelete}
				onManagePermissions={onManagePermissions}
				rememberTrigger={rememberTrigger}
				restoreFocus={restoreFocus}
				setPrimaryFallback={setPrimaryFallback}
			/>
		));
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
							<NewWorkflowButton
								ref={setSecondaryFallback}
								atLimit={workflows.length >= SAVED_WORKFLOW_MAX_PER_SCOPE}
								onClick={(event) => {
									rememberTrigger(event);
									onCreate();
								}}
							/>
						)}
					</div>
				}
			/>

			{Boolean(error) && (
				<ErrorAlert
					error={error}
					actions={
						workflows.length > 0 && (
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
				<ForkSavedWorkflowEditorDialog
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
					title={`Delete ${deleteState.workflow.name}?`}
					confirmText="Delete"
					description={
						<>
							<p className="m-0">{deleteState.message}</p>
							{Boolean(deleteState.error) && (
								<Alert severity="error" className="mt-3">
									<AlertDescription>
										{getErrorMessage(
											deleteState.error,
											"Failed to delete saved workflow.",
										)}
										{getErrorDetail(deleteState.error)
											? ` ${getErrorDetail(deleteState.error)}`
											: ""}
									</AlertDescription>
								</Alert>
							)}
						</>
					}
					onConfirm={deleteState.onConfirm}
					confirmLoading={deleteState.isDeleting}
				/>
			)}

			{detailState && (
				<ForkSavedWorkflowDetailDialog
					onCloseAutoFocus={restoreFocus}
					workflow={detailState.workflow}
					isLoading={detailState.isLoading}
					error={detailState.error}
					onRetry={detailState.onRetry}
					isRetrying={detailState.isRetrying}
					onClose={detailState.onClose}
				/>
			)}
		</div>
	);
};

type SavedWorkflowRowProps = {
	workflow: ForkSavedWorkflowMetadata;
	access: SkillAccess;
	hasRowActions: boolean;
	isFirst: boolean;
	onView: (workflow: ForkSavedWorkflowMetadata) => void;
	onEdit: (workflow: ForkSavedWorkflowMetadata) => void;
	onDelete: (workflow: ForkSavedWorkflowMetadata) => void;
	onManagePermissions?: (
		workflow: ForkSavedWorkflowMetadata,
		onCloseAutoFocus: (event: Event) => void,
	) => void;
	rememberTrigger: (event: React.SyntheticEvent<HTMLButtonElement>) => void;
	restoreFocus: (event: Event) => void;
	setPrimaryFallback: (button: HTMLButtonElement | null) => void;
};

const SavedWorkflowRow: React.FC<SavedWorkflowRowProps> = ({
	workflow,
	access,
	hasRowActions,
	isFirst,
	onView,
	onEdit,
	onDelete,
	onManagePermissions,
	rememberTrigger,
	restoreFocus,
	setPrimaryFallback,
}) => (
	<TableRow>
		<TableCell className="max-w-48 truncate" title={workflow.name}>
			<Button
				variant="subtle"
				className="h-auto min-w-0 max-w-full justify-start p-0"
				ref={isFirst && !hasRowActions ? setPrimaryFallback : undefined}
				onClick={(event) => {
					rememberTrigger(event);
					onView(workflow);
				}}
			>
				<span className="truncate">{workflow.name}</span>
			</Button>
		</TableCell>
		<TableCell
			className="w-full max-w-0 truncate"
			title={workflow.description || undefined}
		>
			{workflow.description || (
				<span className="text-content-disabled">No description</span>
			)}
		</TableCell>
		<TableCell className="hidden whitespace-nowrap sm:table-cell">
			{formatUpdatedAt(workflow.updated_at)}
		</TableCell>
		{hasRowActions && (
			<TableCell className="text-right">
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							size="icon"
							variant="subtle"
							aria-label={`More actions for ${workflow.name}`}
							ref={isFirst ? setPrimaryFallback : undefined}
							onPointerDown={rememberTrigger}
							onKeyDown={rememberTrigger}
						>
							<EllipsisVerticalIcon aria-hidden="true" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end">
						{access.update && (
							<DropdownMenuItem onClick={() => onEdit(workflow)}>
								Edit
							</DropdownMenuItem>
						)}
						{onManagePermissions && (
							<DropdownMenuItem
								onClick={() => onManagePermissions(workflow, restoreFocus)}
							>
								Manage permissions
							</DropdownMenuItem>
						)}
						{access.delete && (
							<DropdownMenuItem
								className="text-content-destructive focus:text-content-destructive"
								onClick={() => onDelete(workflow)}
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
