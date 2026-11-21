// This file is fork code: creates and edits saved workflows with the skill
// editor's parts (fork-workflows experiment).

import { cn } from "cn";
import { useId, useRef, useState } from "react";
import TextareaAutosize from "react-textarea-autosize";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "#/components/Dialog/Dialog";
import { Input } from "#/components/Input/Input";
import { Label } from "#/components/Label/Label";
import { Loader } from "#/components/Loader/Loader";
import { Spinner } from "#/components/Spinner/Spinner";
import { RetryButton } from "#/pages/AgentsPage/components/RetryButton";
import {
	SkillEditorFields,
	SkillEditorSubmitError,
} from "#/pages/AgentsPage/components/SkillEditor";
import { getSkillContentSizeBytes } from "#/pages/AgentsPage/utils/skills";
import {
	SAVED_WORKFLOW_MAX_ARGS_SCHEMA_BYTES,
	SAVED_WORKFLOW_MAX_SCRIPT_BYTES,
	type SavedWorkflowFormValues,
	savedWorkflowArgsSchemaSizeBytes,
	savedWorkflowFormErrors,
} from "./savedWorkflowForm";
import type { ForkSavedWorkflowEditorState } from "./useForkSavedWorkflowEditor";

type ForkSavedWorkflowEditorDialogProps = ForkSavedWorkflowEditorState & {
	onCloseAutoFocus?: (event: Event) => void;
};

/** Creates or edits one saved workflow: description, script, args, phases. */
export const ForkSavedWorkflowEditorDialog: React.FC<
	ForkSavedWorkflowEditorDialogProps
> = ({
	mode,
	initialValues,
	isLoading = false,
	loadError,
	onRetry,
	isRetrying = false,
	submitError,
	isSubmitting,
	onSubmit,
	onClose,
	onCloseAutoFocus,
}) => {
	const cancelButtonRef = useRef<HTMLButtonElement>(null);
	const handleOpenChange = (open: boolean) => {
		if (!open && !isSubmitting) {
			onClose();
		}
	};
	// Escape and outside clicks act as Cancel, which the form disables from
	// the moment Save is clicked, so a save in flight keeps the dialog open
	// to show its result.
	const dismissWithCancel = (event: Event) => {
		event.preventDefault();
		cancelButtonRef.current?.click();
	};

	if (isLoading || !initialValues) {
		return (
			<Dialog open onOpenChange={handleOpenChange}>
				<DialogContent onCloseAutoFocus={onCloseAutoFocus}>
					<DialogHeader>
						<DialogTitle>
							{isLoading
								? "Loading saved workflow"
								: "Unable to load saved workflow"}
						</DialogTitle>
					</DialogHeader>
					{isLoading ? (
						<Loader />
					) : (
						<ErrorAlert error={loadError} showDebugDetail={false} />
					)}
					<DialogFooter>
						<Button variant="outline" onClick={onClose}>
							Close
						</Button>
						{!isLoading && onRetry && (
							<RetryButton isRetrying={isRetrying} onRetry={onRetry} />
						)}
					</DialogFooter>
				</DialogContent>
			</Dialog>
		);
	}

	return (
		<Dialog open onOpenChange={handleOpenChange}>
			<DialogContent
				className="flex max-w-2xl flex-col gap-0 overflow-hidden p-0"
				onCloseAutoFocus={onCloseAutoFocus}
				onEscapeKeyDown={dismissWithCancel}
				onInteractOutside={dismissWithCancel}
			>
				<SavedWorkflowEditorForm
					mode={mode}
					initialValues={initialValues}
					submitError={submitError}
					isSubmitting={isSubmitting}
					onSubmit={onSubmit}
					onClose={onClose}
					cancelButtonRef={cancelButtonRef}
				/>
			</DialogContent>
		</Dialog>
	);
};

type SavedWorkflowEditorFormProps = Pick<
	ForkSavedWorkflowEditorState,
	"mode" | "submitError" | "isSubmitting" | "onSubmit" | "onClose"
> & {
	initialValues: SavedWorkflowFormValues;
	cancelButtonRef: React.Ref<HTMLButtonElement>;
};

/**
 * Every field keeps the values the form opened with as its baseline, so
 * refreshed initialValues cannot reset some fields and leave others stale.
 */
const SavedWorkflowEditorForm: React.FC<SavedWorkflowEditorFormProps> = ({
	mode,
	initialValues: refreshedInitialValues,
	submitError,
	isSubmitting,
	onSubmit,
	onClose,
	cancelButtonRef,
}) => {
	const [initialValues] = useState(refreshedInitialValues);
	const [values, setValues] = useState(initialValues);
	const errors = savedWorkflowFormErrors(values);
	const canSubmit =
		!isSubmitting &&
		Object.keys(errors).length === 0 &&
		(mode === "create" ||
			values.script.trim() !== initialValues.script.trim() ||
			values.description.trim() !== initialValues.description.trim() ||
			values.argsSchema.trim() !== initialValues.argsSchema.trim() ||
			values.phases !== initialValues.phases);

	return (
		<form
			className="flex min-h-0 flex-1 flex-col"
			onSubmit={(event) => {
				event.preventDefault();
				if (canSubmit) {
					onSubmit(values);
				}
			}}
		>
			<DialogHeader className="px-6 pt-6">
				<DialogTitle>
					{mode === "create" ? "Create" : "Edit"} saved workflow
				</DialogTitle>
				<DialogDescription>
					Starlark that a chat runs with run_workflow, with the args it takes
					and the phases it reports.
				</DialogDescription>
			</DialogHeader>
			<SkillEditorFields>
				<SkillEditorSubmitError error={submitError} />
				{mode === "create" && (
					<SavedWorkflowNameField
						value={values.name}
						error={errors.name}
						disabled={isSubmitting}
						onChange={(name) => setValues((current) => ({ ...current, name }))}
					/>
				)}
				<SavedWorkflowTextArea
					label="Description"
					placeholder="What the workflow does and when a chat should run it"
					value={values.description}
					error={errors.description}
					disabled={isSubmitting}
					minRows={2}
					onChange={(description) =>
						setValues((current) => ({ ...current, description }))
					}
				/>
				<SavedWorkflowTextArea
					label="Script"
					placeholder="def main(args):"
					value={values.script}
					error={errors.script}
					disabled={isSubmitting}
					size={getSkillContentSizeBytes(values.script)}
					sizeLimit={SAVED_WORKFLOW_MAX_SCRIPT_BYTES}
					minRows={10}
					mono
					onChange={(script) =>
						setValues((current) => ({ ...current, script }))
					}
				/>
				<SavedWorkflowTextArea
					label="Args schema"
					// sentence-case-expect: JSON Schema is a proper noun.
					hint="Optional JSON Schema object for the args of a run. Clearing it removes the schema."
					placeholder='{"type": "object"}'
					value={values.argsSchema}
					error={errors.argsSchema}
					disabled={isSubmitting}
					size={savedWorkflowArgsSchemaSizeBytes(values.argsSchema)}
					sizeLimit={SAVED_WORKFLOW_MAX_ARGS_SCHEMA_BYTES}
					minRows={4}
					mono
					onChange={(argsSchema) =>
						setValues((current) => ({ ...current, argsSchema }))
					}
				/>
				<SavedWorkflowTextArea
					label="Phases"
					hint="Optional phase names, one per line, in the order the script reports them."
					placeholder={"review\nsummarize"}
					value={values.phases}
					error={errors.phases}
					disabled={isSubmitting}
					minRows={3}
					onChange={(phases) =>
						setValues((current) => ({ ...current, phases }))
					}
				/>
			</SkillEditorFields>
			<DialogFooter className="border-t px-6 py-4">
				<Button
					ref={cancelButtonRef}
					variant="outline"
					disabled={isSubmitting}
					onClick={onClose}
				>
					Cancel
				</Button>
				<Button type="submit" disabled={!canSubmit}>
					<Spinner loading={isSubmitting} />
					{mode === "create" ? "Create workflow" : "Save workflow"}
				</Button>
			</DialogFooter>
		</form>
	);
};

type SavedWorkflowNameFieldProps = {
	value: string;
	error: string | undefined;
	disabled: boolean;
	onChange: (name: string) => void;
};

export const SavedWorkflowNameField: React.FC<SavedWorkflowNameFieldProps> = ({
	value,
	error,
	disabled,
	onChange,
}) => {
	const nameId = useId();
	const nameErrorId = useId();
	const nameHelpId = useId();
	return (
		<div className="flex flex-col gap-2">
			<Label htmlFor={nameId}>Name</Label>
			<Input
				id={nameId}
				value={value}
				onChange={(event) => onChange(event.target.value)}
				placeholder="review-files"
				disabled={disabled}
				aria-invalid={Boolean(error)}
				aria-describedby={error ? nameErrorId : nameHelpId}
			/>
			{error ? (
				<p id={nameErrorId} className="m-0 text-xs text-content-destructive">
					{error}
				</p>
			) : (
				<p id={nameHelpId} className="m-0 text-xs text-content-secondary">
					Use lowercase letters, numbers, and hyphens. Names cannot be changed
					after creation.
				</p>
			)}
		</div>
	);
};

type SavedWorkflowTextAreaProps = {
	label: string;
	hint?: string;
	placeholder?: string;
	value: string;
	error?: string | undefined;
	disabled?: boolean;
	size?: number;
	sizeLimit?: number;
	minRows: number;
	mono?: boolean;
	readOnly?: boolean;
	onChange?: (value: string) => void;
};

/**
 * The text field of the saved workflow editor and its read-only detail view.
 */
export const SavedWorkflowTextArea: React.FC<SavedWorkflowTextAreaProps> = ({
	label,
	hint,
	placeholder,
	value,
	error,
	disabled = false,
	size,
	sizeLimit,
	minRows,
	mono = false,
	readOnly = false,
	onChange,
}) => {
	const fieldId = useId();
	const errorId = useId();
	const hintId = useId();
	return (
		<div className="flex flex-col gap-2">
			<Label htmlFor={fieldId}>{label}</Label>
			<TextareaAutosize
				id={fieldId}
				value={value}
				onChange={
					onChange ? (event) => onChange(event.target.value) : undefined
				}
				placeholder={placeholder}
				disabled={disabled}
				readOnly={readOnly}
				minRows={minRows}
				aria-invalid={Boolean(error)}
				aria-describedby={error ? errorId : hint ? hintId : undefined}
				className={cn(
					"w-full resize-y rounded-md border bg-transparent px-3 py-2 text-sm leading-relaxed text-content-primary",
					"placeholder:text-content-secondary",
					"focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link",
					"disabled:cursor-not-allowed disabled:opacity-50",
					mono && "font-mono",
					readOnly && "bg-surface-secondary",
					error && "border-border-destructive",
				)}
			/>
			{error && (
				<p id={errorId} className="m-0 text-xs text-content-destructive">
					{error}
				</p>
			)}
			{!error && hint && (
				<p id={hintId} className="m-0 text-xs text-content-secondary">
					{hint}
				</p>
			)}
			{size !== undefined && sizeLimit !== undefined && !error && (
				<p className="m-0 text-xs text-content-secondary">
					{size} of {sizeLimit} bytes used.
				</p>
			)}
		</div>
	);
};
