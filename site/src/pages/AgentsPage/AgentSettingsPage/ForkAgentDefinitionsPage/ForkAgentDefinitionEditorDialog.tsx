// This file is fork code: creates and edits agent definitions with the
// skill editor's parts.

import { UploadIcon } from "lucide-react";
import { useId, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { Alert, AlertDescription, AlertTitle } from "#/components/Alert/Alert";
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
import { formatKiB } from "#/utils/fileSize";
import { RetryButton } from "../../components/RetryButton";
import {
	SkillEditorBodyField,
	SkillEditorDescriptionField,
	SkillEditorFields,
	SkillEditorForm,
	SkillEditorNameField,
	SkillEditorSubmitError,
	type SkillErrorDisplay,
	useSkillEditorContext,
} from "../../components/SkillEditor";
import { readAgentAttachmentText } from "../../utils/fileAttachmentLimits";
import { SKILL_MAX_SIZE_BYTES } from "../../utils/skills";
import {
	type AgentDefinitionFormValues,
	buildAgentDefinitionMarkdown,
	parseAgentDefinitionMarkdown,
} from "./agentDefinitionMarkdown";

type ForkAgentDefinitionEditorState = {
	mode: "create" | "edit";
	/** Unset while an edited definition loads or after it fails to load. */
	initialValues?: AgentDefinitionFormValues;
	isLoading?: boolean;
	loadError?: unknown;
	/** Shown with a load error that a refetch can fix. */
	onRetry?: () => void;
	isRetrying?: boolean;
	submitError?: SkillErrorDisplay;
	isSubmitting: boolean;
	/** Call onSettled when the request finishes so the form can submit again. */
	onSubmit: (
		submission: { name: string; content: string },
		onSettled: () => void,
	) => void;
	onClose: () => void;
};

type ForkAgentDefinitionEditorDialogProps = ForkAgentDefinitionEditorState & {
	onCloseAutoFocus?: (event: Event) => void;
};

/** Creates or edits one agent definition as markdown with front-matter. */
export const ForkAgentDefinitionEditorDialog: React.FC<
	ForkAgentDefinitionEditorDialogProps
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
								? "Loading agent definition"
								: "Unable to load agent definition"}
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
				className="flex max-h-[90vh] max-w-2xl flex-col gap-0 overflow-hidden p-0"
				onCloseAutoFocus={onCloseAutoFocus}
				onEscapeKeyDown={dismissWithCancel}
				onInteractOutside={dismissWithCancel}
			>
				<AgentDefinitionEditorForm
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

type AgentDefinitionEditorFormProps = Pick<
	ForkAgentDefinitionEditorState,
	"mode" | "submitError" | "isSubmitting" | "onSubmit" | "onClose"
> & {
	initialValues: AgentDefinitionFormValues;
	cancelButtonRef: React.Ref<HTMLButtonElement>;
};

/**
 * The skill form owns the name, description, and body. The model and the
 * other front-matter live here and join them in the saved markdown. Every
 * field keeps the values the form opened with as its baseline, so refreshed
 * initialValues cannot reset some fields and leave others stale.
 */
const AgentDefinitionEditorForm: React.FC<AgentDefinitionEditorFormProps> = ({
	mode,
	initialValues: refreshedInitialValues,
	submitError,
	isSubmitting,
	onSubmit,
	onClose,
	cancelButtonRef,
}) => {
	const [initialValues] = useState(refreshedInitialValues);
	const [model, setModel] = useState(initialValues.model);
	const [extraFrontMatter, setExtraFrontMatter] = useState(
		initialValues.extraFrontMatter,
	);
	const isAgentDirty =
		model !== initialValues.model ||
		extraFrontMatter !== initialValues.extraFrontMatter;

	return (
		<SkillEditorForm
			mode={mode}
			initialValues={{
				name: initialValues.name,
				description: initialValues.description,
				body: initialValues.body,
			}}
			// The server rejects a duplicate name with a 409, which is
			// shown with the other submit errors.
			existingNames={[]}
			isSubmitting={isSubmitting}
			buildContent={(values) =>
				buildAgentDefinitionMarkdown({ ...values, model, extraFrontMatter })
			}
			onSubmit={(values, content, onSettled) =>
				onSubmit({ name: values.name, content }, onSettled)
			}
		>
			<DialogHeader className="px-6 pt-6">
				<DialogTitle>
					{mode === "create" ? "Create" : "Edit"} agent definition
				</DialogTitle>
				<DialogDescription>
					Saved as a Claude Code agent markdown file that chats can spawn with
					spawn_agent.
				</DialogDescription>
			</DialogHeader>
			<SkillEditorFields>
				<SkillEditorSubmitError error={submitError} />
				<AgentDefinitionImport
					onImport={(values) => {
						setModel(values.model);
						setExtraFrontMatter(values.extraFrontMatter);
					}}
				/>
				<SkillEditorNameField />
				<SkillEditorDescriptionField placeholder="When chats should spawn this agent" />
				<AgentDefinitionModelField model={model} onModelChange={setModel} />
				<SkillEditorBodyField placeholder="Instructions the agent follows when a chat spawns it." />
			</SkillEditorFields>
			<EditorFooter
				isAgentDirty={isAgentDirty}
				onClose={onClose}
				cancelButtonRef={cancelButtonRef}
			/>
		</SkillEditorForm>
	);
};

type AgentDefinitionModelFieldProps = {
	model: string;
	onModelChange: (model: string) => void;
};

const AgentDefinitionModelField: React.FC<AgentDefinitionModelFieldProps> = ({
	model,
	onModelChange,
}) => {
	const { form, isBusy } = useSkillEditorContext();
	const modelId = useId();
	const hintId = useId();
	return (
		<div className="flex flex-col gap-2">
			<Label htmlFor={modelId}>Model</Label>
			<Input
				id={modelId}
				value={model}
				onChange={(event) => {
					// The size limit counts the model, so validate again once
					// the form renders with the new value.
					flushSync(() => onModelChange(event.target.value));
					void form.validateForm();
				}}
				placeholder="The chat's model"
				disabled={isBusy}
				aria-describedby={hintId}
			/>
			<p id={hintId} className="m-0 text-xs text-content-secondary">
				Optional. Matches a model config ID, model name, or display name. Leave
				empty to use the chat's model.
			</p>
		</div>
	);
};

// Outside the component: the React Compiler cannot compile this try/catch.
const readAgentDefinitionFile = async (
	file: File,
): Promise<{ values?: AgentDefinitionFormValues; error?: string }> => {
	try {
		const values = parseAgentDefinitionMarkdown(
			await readAgentAttachmentText(file),
		);
		// Like the workspace agent's loader, fall back to the file name.
		return {
			values: {
				...values,
				name: values.name || file.name.replace(/\.[^.]*$/, ""),
			},
		};
	} catch (error) {
		return {
			error:
				error instanceof Error ? error.message : "Could not read the file.",
		};
	}
};

type AgentDefinitionImportProps = {
	/** Receives the imported model and other front-matter. */
	onImport: (values: AgentDefinitionFormValues) => void;
};

/** Fills the form from an agent markdown file, front-matter included. */
const AgentDefinitionImport: React.FC<AgentDefinitionImportProps> = ({
	onImport,
}) => {
	const { form, mode, isBusy, isReadingFile, setIsReadingFile } =
		useSkillEditorContext();
	const fileInputRef = useRef<HTMLInputElement>(null);
	const [importError, setImportError] = useState<string>();

	const handleFileChange = async (
		event: React.ChangeEvent<HTMLInputElement>,
	) => {
		const file = event.currentTarget.files?.[0];
		event.currentTarget.value = "";
		if (!file) {
			return;
		}
		setImportError(undefined);
		if (file.size > SKILL_MAX_SIZE_BYTES) {
			setImportError(
				`Choose a file that is ${formatKiB(SKILL_MAX_SIZE_BYTES)} or smaller.`,
			);
			return;
		}
		setIsReadingFile(true);
		const { values, error } = await readAgentDefinitionFile(file);
		if (values) {
			// Render the imported model first, so setValues validates the
			// size with it.
			flushSync(() => onImport(values));
			// An edited definition keeps its name, which the API cannot change.
			await form.setValues({
				...form.values,
				name: mode === "edit" ? form.values.name : values.name,
				description: values.description,
				body: values.body,
			});
			await form.setTouched(
				{ name: mode === "create", description: true, body: true },
				false,
			);
		}
		setImportError(error);
		setIsReadingFile(false);
	};

	return (
		<div className="flex flex-col gap-3 rounded-md border border-border p-4">
			<div className="flex items-center justify-between gap-4">
				<p className="m-0 text-xs text-content-secondary">
					Import a Claude Code or Cursor agent markdown file to fill the fields
					below. Its other front-matter, such as tools, is kept.
				</p>
				<input
					ref={fileInputRef}
					type="file"
					accept=".md,.markdown,text/markdown"
					aria-label="Upload agent definition file"
					className="hidden"
					disabled={isBusy}
					onChange={handleFileChange}
				/>
				<Button
					variant="outline"
					size="sm"
					disabled={isBusy}
					onClick={() => fileInputRef.current?.click()}
				>
					<Spinner loading={isReadingFile}>
						<UploadIcon />
					</Spinner>
					Import file
				</Button>
			</div>
			{importError && (
				<Alert severity="error">
					<AlertTitle>Could not import the file</AlertTitle>
					<AlertDescription>{importError}</AlertDescription>
				</Alert>
			)}
		</div>
	);
};

type EditorFooterProps = {
	/** Whether the model or the other front-matter changed. */
	isAgentDirty: boolean;
	onClose: () => void;
	cancelButtonRef: React.Ref<HTMLButtonElement>;
};

const EditorFooter: React.FC<EditorFooterProps> = ({
	isAgentDirty,
	onClose,
	cancelButtonRef,
}) => {
	const { form, mode, isBusy, isSubmitting } = useSkillEditorContext();
	const canSubmit = !isBusy && form.isValid && (form.dirty || isAgentDirty);
	return (
		<DialogFooter className="border-t border-border px-6 py-4">
			<Button
				ref={cancelButtonRef}
				variant="outline"
				disabled={isBusy}
				onClick={onClose}
			>
				Cancel
			</Button>
			<Button type="submit" disabled={!canSubmit}>
				<Spinner loading={isSubmitting} />
				{mode === "create"
					? "Create agent definition"
					: "Save agent definition"}
			</Button>
		</DialogFooter>
	);
};
