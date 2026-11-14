// This file is fork code: the fork-token-budget experiment's organization
// token budget form on the Coder Agents settings page.

import { useFormik } from "formik";
import isEqual from "lodash/isEqual";
import { useId, useState } from "react";
import { getErrorMessage } from "#/api/errors";
import type {
	ForkOrganizationTokenBudget,
	ForkTokenBudgetFallback,
	ForkTokenBudgetStrategy,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Label } from "#/components/Label/Label";
import { Loader } from "#/components/Loader/Loader";
import { RadioGroup, RadioGroupItem } from "#/components/RadioGroup/RadioGroup";
import { useTemporarySavedState } from "#/components/TemporarySavedState/TemporarySavedState";
import { AgentSettingLayout } from "./AgentSettingLayout";
import type { MutationCallbacks } from "./SubagentModelOverrideSettings";

type Option<T extends string> = {
	value: T;
	label: string;
	description: string;
};

const strategyOptions: readonly Option<ForkTokenBudgetStrategy>[] = [
	{
		value: "summary",
		label: "Summarize the conversation",
		description:
			"Chats summarize their history at the compression threshold, as they do without the token budget.",
	},
	{
		value: "token_budget",
		label: "Token budget",
		description:
			"At the handoff point, chats are asked to save notes and call new_context to start a new context window.",
	},
];

const fallbackOptions: readonly Option<ForkTokenBudgetFallback>[] = [
	{
		value: "new_window",
		label: "Start a new context window",
		description:
			"The chat continues in a new window that starts from its notes.",
	},
	{
		value: "summary",
		label: "Summarize the conversation",
		description: "The chat summarizes its history and continues.",
	},
];

type ForkTokenBudgetSettingsProps = {
	setting: ForkOrganizationTokenBudget | undefined;
	isLoading: boolean;
	loadError: unknown;
	canEdit: boolean;
	onSave: (
		req: ForkOrganizationTokenBudget,
		options: MutationCallbacks,
	) => void;
	isSaving: boolean;
	saveError: unknown;
};

/**
 * Shows how an organization's chats compact their context and lets editors
 * pick the summary or the token budget.
 */
export const ForkTokenBudgetSettings: React.FC<
	ForkTokenBudgetSettingsProps
> = ({ setting, isLoading, loadError, ...formProps }) => {
	if (isLoading) {
		return <Loader label="Loading token budget setting" />;
	}
	if (!setting) {
		return <ErrorAlert error={loadError} />;
	}
	return (
		<div className="flex flex-col gap-4">
			{loadError != null && <ErrorAlert error={loadError} />}
			<TokenBudgetForm setting={setting} {...formProps} />
		</div>
	);
};

type TokenBudgetFormProps = Omit<
	ForkTokenBudgetSettingsProps,
	"setting" | "isLoading" | "loadError"
> & {
	setting: ForkOrganizationTokenBudget;
};

const TokenBudgetForm: React.FC<TokenBudgetFormProps> = ({
	setting,
	canEdit,
	onSave,
	isSaving,
	saveError,
}) => {
	const { isSavedVisible, showSavedState } = useTemporarySavedState();
	const [hasDraft, setHasDraft] = useState(false);
	const form = useFormik<ForkOrganizationTokenBudget>({
		// A refetch updates an unedited form but must not discard a draft.
		enableReinitialize: !hasDraft,
		initialValues: setting,
		onSubmit: (values, { resetForm }) => {
			onSave(values, {
				onSuccess: () => {
					showSavedState();
					resetForm({ values });
					setHasDraft(false);
				},
			});
		},
	});
	const changeValue = <K extends keyof ForkOrganizationTokenBudget>(
		field: K,
		value: ForkOrganizationTokenBudget[K],
	) => {
		setHasDraft(
			!isEqual({ ...form.values, [field]: value }, form.initialValues),
		);
		void form.setFieldValue(field, value);
	};
	const isDisabled = !canEdit || isSaving;
	const isFallbackDisabled =
		isDisabled || form.values.strategy !== "token_budget";

	return (
		<AgentSettingLayout
			title="Context compaction"
			description={
				<>
					The token budget hands off at each model's compression threshold,
					which you set on the model config. A threshold of 100% turns the
					budget off for that model. A user's own compression threshold for the
					model replaces it in that user's chats.{" "}
					{canEdit
						? "A change applies from the next step of each chat."
						: "You can view this setting but not change it."}
				</>
			}
			showSave={canEdit && form.dirty}
			isSaving={isSaving}
			isSavedVisible={isSavedVisible}
			saveDisabled={isSaving}
			onSubmit={form.handleSubmit}
			error={
				saveError ? (
					<p className="m-0" role="alert">
						{getErrorMessage(
							saveError,
							"Failed to save the token budget setting.",
						)}
					</p>
				) : undefined
			}
		>
			<div className="grid w-full gap-6 sm:grid-cols-2">
				<OptionGroup
					name="strategy"
					legend="Strategy"
					options={strategyOptions}
					value={form.values.strategy}
					onChange={(value) => changeValue("strategy", value)}
					disabled={isDisabled}
				/>
				<OptionGroup
					name="fallback"
					legend="Fallback at the usable limit"
					hint="Applies only with the token budget, when a chat reaches the usable limit without calling new_context. With summary, a compaction model override that triggers earlier still summarizes first."
					options={fallbackOptions}
					value={form.values.fallback}
					onChange={(value) => changeValue("fallback", value)}
					disabled={isFallbackDisabled}
				/>
			</div>
		</AgentSettingLayout>
	);
};

type OptionGroupProps<T extends string> = {
	name: string;
	legend: string;
	hint?: string;
	options: readonly Option<T>[];
	value: T;
	onChange: (value: T) => void;
	disabled: boolean;
};

const OptionGroup = <T extends string>({
	name,
	legend,
	hint,
	options,
	value,
	onChange,
	disabled,
}: OptionGroupProps<T>) => {
	const legendId = `${useId()}-${name}`;
	const hintId = `${legendId}-hint`;
	return (
		<div className="flex flex-col gap-3">
			<div>
				<p
					id={legendId}
					className="m-0 text-sm font-medium text-content-primary"
				>
					{legend}
				</p>
				{hint && (
					<p id={hintId} className="m-0 text-xs text-content-secondary">
						{hint}
					</p>
				)}
			</div>
			<RadioGroup
				aria-labelledby={legendId}
				aria-describedby={hint ? hintId : undefined}
				value={value}
				onValueChange={(next) => {
					const option = options.find((o) => o.value === next);
					if (option) {
						onChange(option.value);
					}
				}}
				disabled={disabled}
			>
				{options.map((option) => {
					const id = `${legendId}-${option.value}`;
					const descriptionId = `${id}-description`;
					return (
						<div key={option.value} className="flex items-start gap-2">
							<RadioGroupItem
								id={id}
								value={option.value}
								aria-describedby={descriptionId}
								className="mt-0.5"
							/>
							<div className="flex flex-col">
								<Label htmlFor={id} className="cursor-pointer font-normal">
									{option.label}
								</Label>
								<span
									id={descriptionId}
									className="text-xs text-content-secondary"
								>
									{option.description}
								</span>
							</div>
						</div>
					);
				})}
			</RadioGroup>
		</div>
	);
};
