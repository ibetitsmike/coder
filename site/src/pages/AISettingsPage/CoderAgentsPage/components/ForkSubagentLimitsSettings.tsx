// This file is fork code: the fork-nested-subagents experiment's organization
// subagent limits form on the Coder Agents settings page.

import { useFormik } from "formik";
import { getErrorMessage } from "#/api/errors";
import {
	type ForkOrganizationSubagentLimits,
	type ForkOrganizationSubagentLimitsResponse,
	type ForkSubagentLimitValues,
	MaxChatMaxSubagentDepth,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { FormField } from "#/components/FormField/FormField";
import { Loader } from "#/components/Loader/Loader";
import { useTemporarySavedState } from "#/components/TemporarySavedState/TemporarySavedState";
import { getFormHelpers } from "#/utils/formUtils";
import { AgentSettingLayout } from "./AgentSettingLayout";

type LimitField = keyof ForkSubagentLimitValues;

type FormValues = Record<LimitField, string>;

type LimitConfig = {
	field: LimitField;
	label: string;
	description: string;
	max?: number;
};

const limitConfigs: readonly LimitConfig[] = [
	{
		field: "max_depth",
		label: "Max depth",
		description: "How deep subagents can nest. A root chat is at depth 0.",
		max: MaxChatMaxSubagentDepth,
	},
	{
		field: "max_concurrent",
		label: "Running subagents in this organization",
		description:
			"Counts only this organization's subagents. The deployment value counts every organization's.",
	},
	{
		field: "max_concurrent_per_family",
		label: "Running subagents per chat family",
		description: "A family is a root chat and all of its subagents.",
	},
	{
		field: "max_live_per_family",
		label: "Live subagents per chat family",
		description:
			"Live subagents have not finished and are not archived. Spawning or resuming a subagent fails at the limit.",
	},
];

const toFormValues = (limits: ForkOrganizationSubagentLimits): FormValues => ({
	max_depth: limits.max_depth?.toString() ?? "",
	max_concurrent: limits.max_concurrent?.toString() ?? "",
	max_concurrent_per_family: limits.max_concurrent_per_family?.toString() ?? "",
	max_live_per_family: limits.max_live_per_family?.toString() ?? "",
});

const toRequest = (values: FormValues): ForkOrganizationSubagentLimits => {
	const request: { -readonly [K in LimitField]?: number } = {};
	for (const { field } of limitConfigs) {
		const value = values[field].trim();
		if (value !== "") {
			request[field] = Number(value);
		}
	}
	return request;
};

const validate = (values: FormValues) => {
	const errors: Partial<Record<LimitField, string>> = {};
	for (const { field, max } of limitConfigs) {
		const value = values[field].trim();
		if (value === "") {
			continue;
		}
		const parsed = Number(value);
		if (
			!/^\d+$/.test(value) ||
			parsed < 1 ||
			(max !== undefined && parsed > max)
		) {
			errors[field] = max
				? `Enter a whole number from 1 to ${max}.`
				: "Enter a whole number of 1 or more.";
		}
	}
	return errors;
};

const formatLimit = (field: LimitField, value: number) =>
	field !== "max_depth" && value === 0 ? "No limit" : value.toString();

type ForkSubagentLimitsSettingsProps = {
	limits: ForkOrganizationSubagentLimitsResponse | undefined;
	isLoading: boolean;
	loadError: unknown;
	canEdit: boolean;
	onSave: (
		req: ForkOrganizationSubagentLimits,
		options: { onSuccess: () => void },
	) => void;
	isSaving: boolean;
	saveError: unknown;
};

export const ForkSubagentLimitsSettings: React.FC<
	ForkSubagentLimitsSettingsProps
> = ({ limits, isLoading, loadError, ...formProps }) => {
	if (isLoading) {
		return <Loader label="Loading subagent limits" />;
	}
	if (!limits) {
		return <ErrorAlert error={loadError} />;
	}
	return <SubagentLimitsForm limits={limits} {...formProps} />;
};

type SubagentLimitsFormProps = Omit<
	ForkSubagentLimitsSettingsProps,
	"limits" | "isLoading" | "loadError"
> & {
	limits: ForkOrganizationSubagentLimitsResponse;
};

const SubagentLimitsForm: React.FC<SubagentLimitsFormProps> = ({
	limits,
	canEdit,
	onSave,
	isSaving,
	saveError,
}) => {
	const { isSavedVisible, showSavedState } = useTemporarySavedState();
	const form = useFormik<FormValues>({
		// Reinitializes only when the server values change, so a refetch with
		// the same values keeps in-progress edits.
		enableReinitialize: true,
		validateOnMount: true,
		initialValues: toFormValues(limits.organization),
		validate,
		onSubmit: (values, { resetForm }) => {
			if (!canEdit) {
				return;
			}
			const request = toRequest(values);
			onSave(request, {
				onSuccess: () => {
					showSavedState();
					resetForm({ values: toFormValues(request) });
				},
			});
		},
	});
	const getFieldHelpers = getFormHelpers(form, saveError);
	const showSave = canEdit && form.dirty;

	return (
		<AgentSettingLayout
			title="Subagent limits"
			description={
				<>
					An organization value can only lower the deployment value. Subagents
					also share the deployment's subagent pool.{" "}
					{canEdit
						? "Leave a field empty to use the deployment value."
						: "You can view these limits but not change them."}
				</>
			}
			showSave={showSave}
			isSaving={isSaving}
			isSavedVisible={isSavedVisible}
			saveDisabled={isSaving || !form.isValid}
			onSubmit={form.handleSubmit}
			error={
				saveError ? (
					<p className="m-0">
						{getErrorMessage(saveError, "Failed to save subagent limits.")}
					</p>
				) : undefined
			}
		>
			<div className="grid w-full gap-6 sm:grid-cols-2">
				{limitConfigs.map(({ field, label, description, max }) => (
					<FormField
						key={field}
						field={getFieldHelpers(field)}
						label={label}
						description={
							<>
								<p className="m-0">{description}</p>
								<p className="m-0">
									Deployment: {formatLimit(field, limits.deployment[field])}.
									Effective: {formatLimit(field, limits.effective[field])}.
								</p>
							</>
						}
						// Formik parses number inputs to numbers; the form keeps the
						// raw text so empty and invalid values validate as typed.
						onChange={(event) =>
							void form.setFieldValue(field, event.currentTarget.value)
						}
						type="number"
						inputMode="numeric"
						min={1}
						max={max}
						step={1}
						placeholder="Deployment value"
						disabled={!canEdit || isSaving}
					/>
				))}
			</div>
		</AgentSettingLayout>
	);
};
