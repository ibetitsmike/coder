// This file is fork code: the fork-plugin-packs experiment's list of plugin
// pack switches.

import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import type { PluginPackTarget } from "#/api/forkPluginPackEnablement";
import {
	availablePluginPacks,
	enabledPluginPacks,
	type PluginPackToggle,
	setPluginPackEnabledMutation,
} from "#/api/queries/forkPluginPackEnablement";
import type { ForkEnabledPluginPack } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import { Switch } from "#/components/Switch/Switch";
import { toast } from "#/components/Toaster/toast";
import { RetryButton } from "./RetryButton";
import { SkillEnabledStatus } from "./SkillEnabledSwitch";

type ForkPluginPackTogglesViewProps = {
	installed: readonly ForkEnabledPluginPack[] | undefined;
	enabled: readonly ForkEnabledPluginPack[] | undefined;
	error: unknown;
	isRetrying: boolean;
	onRetry: () => void;
	canEdit: boolean;
	pending: PluginPackToggle | undefined;
	onToggle: (pack: ForkEnabledPluginPack, enabled: boolean) => void;
};

/**
 * Lists the installed plugin packs merged with the target's direct
 * enablements, one switch per pack, and reports toggles through `onToggle`.
 */
export const ForkPluginPackTogglesView: React.FC<
	ForkPluginPackTogglesViewProps
> = ({
	installed,
	enabled,
	error,
	isRetrying,
	onRetry,
	canEdit,
	pending,
	onToggle,
}) => {
	const errorAlert = error ? (
		<ErrorAlert
			error={error}
			actions={<RetryButton isRetrying={isRetrying} onRetry={onRetry} />}
		/>
	) : undefined;
	if (!installed || !enabled) {
		return errorAlert ?? <Loader />;
	}
	const isEnabled = (packId: string) =>
		pending?.pack.id === packId
			? pending.enabled
			: enabled.some((pack) => pack.id === packId);
	return (
		<div className="flex flex-col gap-3">
			{errorAlert}
			{installed.length === 0 ? (
				<p className="m-0 text-sm text-content-secondary">
					No plugin packs are installed in this organization.
				</p>
			) : (
				<ul className="m-0 flex list-none flex-col gap-2 p-0">
					{installed.map((pack) => (
						<li key={pack.id} className="flex items-center gap-3 text-sm">
							<span className="min-w-0 flex-1 truncate" title={pack.name}>
								{pack.name}
							</span>
							<code className="text-xs text-content-secondary">
								{pack.commit_sha.slice(0, 10)}
							</code>
							{canEdit ? (
								<Switch
									aria-label={`Enable ${pack.name}`}
									checked={isEnabled(pack.id)}
									disabled={pending !== undefined}
									onCheckedChange={(checked) => onToggle(pack, checked)}
								/>
							) : (
								<SkillEnabledStatus enabled={isEnabled(pack.id)} />
							)}
						</li>
					))}
				</ul>
			)}
		</div>
	);
};

type ForkPluginPackTogglesProps = {
	target: PluginPackTarget;
	organizationId: string;
	canEdit: boolean;
};

/**
 * Loads a chat or project target's plugin pack enablements and the
 * organization's installed packs, and saves each toggle for that target.
 */
export const ForkPluginPackToggles: React.FC<ForkPluginPackTogglesProps> = ({
	target,
	organizationId,
	canEdit,
}) => {
	const queryClient = useQueryClient();
	const enabledQuery = useQuery(enabledPluginPacks(target));
	const availableQuery = useQuery(availablePluginPacks(organizationId));
	// The error toast is a mutation-level callback, so it still shows when
	// the dialog closes while the request is in flight.
	const toggleMutation = useMutation({
		...setPluginPackEnabledMutation(queryClient, target, organizationId),
		onError: (error) =>
			toast.error(getErrorMessage(error, "Failed to update the plugin pack."), {
				description: getErrorDetail(error),
			}),
	});
	return (
		<ForkPluginPackTogglesView
			installed={availableQuery.data}
			enabled={enabledQuery.data}
			error={enabledQuery.error ?? availableQuery.error}
			isRetrying={enabledQuery.isFetching || availableQuery.isFetching}
			onRetry={() => {
				void enabledQuery.refetch();
				void availableQuery.refetch();
			}}
			canEdit={canEdit}
			pending={toggleMutation.isPending ? toggleMutation.variables : undefined}
			onToggle={(pack, enabled) => toggleMutation.mutate({ pack, enabled })}
		/>
	);
};
