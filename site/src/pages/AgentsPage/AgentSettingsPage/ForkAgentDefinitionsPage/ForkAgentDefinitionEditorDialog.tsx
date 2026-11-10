// This file is fork code: creates and edits agent definitions with the
// skill editor's parts.

import { UploadIcon } from "lucide-react";
import { useId, useRef, useState } from "react";
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
	SkillEditorSubmitButton,
	SkillEditorSubmitError,
	type SkillErrorDisplay,
	useSkillEditorContext,
	useSkillEditorFormState,
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
	onSubmit: (content: string, onSettled: () => void) => void;
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
	const handleOpenChange = (open: boolean) => {
		if (!open) {
			onClose();
		}
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
			>
				<SkillEditorForm
					mode={mode}
					initialValues={initialValues}
					// The server rejects a duplicate name with a 409, which is
					// shown with the other submit errors.
					existingNames={[]}
					isSubmitting={isSubmitting}
					// The form keeps every value it is given, so the submitted
					// values still carry the model and the extra front-matter.
					onSubmit={(values, _skillContent, onSettled) =>
						onSubmit(
							buildAgentDefinitionMarkdown(values as AgentDefinitionFormValues),
							onSettled,
						)
					}
				>
					<DialogHeader className="px-6 pt-6">
						<DialogTitle>
							{mode === "create" ? "Create" : "Edit"} agent definition
						</DialogTitle>
						<DialogDescription>
							Saved as a Claude Code agent markdown file that chats can spawn
							with spawn_agent.
						</DialogDescription>
					</DialogHeader>
					<SkillEditorFields>
						<SkillEditorSubmitError error={submitError} />
						<AgentDefinitionImport />
						<SkillEditorNameField />
						<SkillEditorDescriptionField />
						<AgentDefinitionModelField />
						<SkillEditorBodyField placeholder="Instructions the agent follows when a chat spawns it." />
					</SkillEditorFields>
					<EditorFooter onClose={onClose} />
				</SkillEditorForm>
			</DialogContent>
		</Dialog>
	);
};

const AgentDefinitionModelField: React.FC = () => {
	const { form, isBusy } = useSkillEditorContext();
	const modelId = useId();
	const hintId = useId();
	return (
		<div className="flex flex-col gap-2">
			<Label htmlFor={modelId}>Model</Label>
			<Input
				id={modelId}
				{...form.getFieldProps("model")}
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

/** Fills the form from an agent markdown file, front-matter included. */
const AgentDefinitionImport: React.FC = () => {
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
		const imported = await readAgentDefinitionFile(file);
		if (imported.values) {
			// An edited definition keeps its name, which the API cannot change.
			const values: AgentDefinitionFormValues = {
				...imported.values,
				name: mode === "edit" ? form.values.name : imported.values.name,
			};
			await form.setValues(values);
			await form.setTouched(
				{ name: mode === "create", description: true, body: true },
				false,
			);
		}
		setImportError(imported.error);
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

const EditorFooter: React.FC<{ onClose: () => void }> = ({ onClose }) => {
	const { mode, isBusy } = useSkillEditorFormState();
	return (
		<DialogFooter className="border-t border-border px-6 py-4">
			<Button variant="outline" disabled={isBusy} onClick={onClose}>
				Cancel
			</Button>
			<SkillEditorSubmitButton>
				{mode === "create"
					? "Create agent definition"
					: "Save agent definition"}
			</SkillEditorSubmitButton>
		</DialogFooter>
	);
};
