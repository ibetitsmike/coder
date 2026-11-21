// This file is fork code: the save dialog of a workflow run
// (fork-workflows experiment). It turns one run's script into a saved
// workflow the chat can run again with run_workflow.

import type React from "react";
import { useMemo, useState } from "react";
import { Button } from "#/components/Button/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "#/components/Dialog/Dialog";
import { Label } from "#/components/Label/Label";
import { RadioGroup, RadioGroupItem } from "#/components/RadioGroup/RadioGroup";
import { Spinner } from "#/components/Spinner/Spinner";
import {
	SkillEditorSubmitError,
	type SkillErrorDisplay,
} from "#/pages/AgentsPage/components/SkillEditor";
import {
	SavedWorkflowNameField,
	SavedWorkflowTextArea,
} from "#/pages/AISettingsPage/ForkWorkflowsPage/ForkSavedWorkflowEditorDialog";
import {
	type SavedWorkflowFormValues,
	savedWorkflowFormErrors,
} from "#/pages/AISettingsPage/ForkWorkflowsPage/savedWorkflowForm";

type ForkWorkflowSaveDialogProps = {
	/** The run title, which prefills the name in the panel. */
	title: string;
	/** The run's script, saved as the workflow body. */
	script: string;
	/** Whether the viewer may create organization workflows. */
	canCreateOrganization: boolean;
	isSubmitting: boolean;
	submitError?: SkillErrorDisplay;
	onSubmit: (tier: "user" | "organization", values: SaveValues) => void;
	onClose: () => void;
};

/** The fields the save dialog collects beyond the tier. */
export type SaveValues = {
	name: string;
	description: string;
};

/** Slugs a run title into a saved workflow name candidate. */
export const nameFromTitle = (title: string): string =>
	title
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 128)
		.replace(/-+$/g, "");

/**
 * The dialog starts from these values. The card passes an empty title so the
 * name starts empty and the submit stays disabled until the user types a
 * valid one; the panel prefills it from the run's title.
 */
export const ForkWorkflowSaveDialog: React.FC<ForkWorkflowSaveDialogProps> = ({
	title,
	script,
	canCreateOrganization,
	isSubmitting,
	submitError,
	onSubmit,
	onClose,
}) => {
	const [tier, setTier] = useState<"user" | "organization">("user");
	const [values, setValues] = useState<SaveValues>({
		name: nameFromTitle(title),
		description: "",
	});
	// The form values mirror the editor's shape so the shared validation
	// applies; script and schema are fixed by the run and stay out.
	const formValues = useMemo<SavedWorkflowFormValues>(
		() => ({
			name: values.name,
			description: values.description,
			script,
			argsSchema: "",
			phases: "",
		}),
		[values.name, values.description, script],
	);
	const errors = savedWorkflowFormErrors(formValues);
	const canSubmit = !isSubmitting && Object.keys(errors).length === 0;

	const saveAsLabelId = "save-workflow-tier-label";

	return (
		<Dialog
			open
			onOpenChange={(open) => {
				if (!open && !isSubmitting) {
					onClose();
				}
			}}
		>
			<DialogContent className="max-w-2xl">
				<form
					className="flex flex-col gap-4"
					onSubmit={(event) => {
						event.preventDefault();
						if (canSubmit) {
							onSubmit(tier, values);
						}
					}}
				>
					<DialogHeader>
						<DialogTitle>Save workflow</DialogTitle>
						<DialogDescription>
							Saves this run's script as a saved workflow a chat can run again
							with run_workflow.
						</DialogDescription>
					</DialogHeader>
					<SkillEditorSubmitError error={submitError} />
					<SavedWorkflowNameField
						value={values.name}
						error={errors.name}
						disabled={isSubmitting}
						onChange={(name) => setValues((current) => ({ ...current, name }))}
					/>
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
						value={script}
						readOnly
						disabled={isSubmitting}
						size={script.length}
						sizeLimit={undefined}
						minRows={6}
						mono
					/>
					{canCreateOrganization && (
						<div className="flex flex-col gap-2">
							<Label id={saveAsLabelId}>Save as</Label>
							<RadioGroup
								value={tier}
								aria-labelledby={saveAsLabelId}
								onValueChange={(value) =>
									setTier(value as "user" | "organization")
								}
								disabled={isSubmitting}
							>
								<div className="flex items-center gap-2">
									<RadioGroupItem
										id="save-workflow-tier-personal"
										value="user"
									/>
									<Label
										htmlFor="save-workflow-tier-personal"
										className="font-normal cursor-pointer"
									>
										Personal
									</Label>
								</div>
								<div className="flex items-center gap-2">
									<RadioGroupItem
										id="save-workflow-tier-organization"
										value="organization"
									/>
									<Label
										htmlFor="save-workflow-tier-organization"
										className="font-normal cursor-pointer"
									>
										Organization
									</Label>
								</div>
							</RadioGroup>
						</div>
					)}
					<DialogFooter>
						<Button
							type="button"
							variant="outline"
							disabled={isSubmitting}
							onClick={onClose}
						>
							Cancel
						</Button>
						<Button type="submit" disabled={!canSubmit}>
							<Spinner loading={isSubmitting} />
							Save workflow
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
};
