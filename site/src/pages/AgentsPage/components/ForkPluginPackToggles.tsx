// This file is fork code: the fork-plugin-packs experiment's list of plugin
// pack switches.

import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage, getErrorStatus } from "#/api/errors";
import type { PluginPackTarget } from "#/api/forkPluginPackEnablement";
import {
	enabledPluginPacks,
	type PluginPackToggle,
	setPluginPackEnabledMutation,
} from "#/api/queries/forkPluginPackEnablement";
import { pluginPacks } from "#/api/queries/forkPluginPacks";
import type { ForkEnabledPluginPack } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import { Switch } from "#/components/Switch/Switch";
import { toast } from "#/components/Toaster/toast";
import { RetryButton } from "./RetryButton";
import { SkillEnabledStatus } from "./SkillEnabledSwitch";

type ForkPluginPackTogglesViewProps = {
	/** The organization's installed packs, or undefined when the user cannot list them. */
	installed: readonly ForkEnabledPluginPack[] | undefined;
	enabled: readonly ForkEnabledPluginPack[] | undefined;
	error: unknown;
	isRetrying: boolean;
	onRetry: () => void;
	canEdit: boolean;
	pending: PluginPackToggle | undefined;
	onToggle: (packId: string, enabled: boolean) => void;
};

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
	if (!enabled) {
		return error ? (
			<ErrorAlert
				error={error}
				actions={<RetryButton isRetrying={isRetrying} onRetry={onRetry} />}
			/>
		) : (
			<Loader />
		);
	}
	const isEnabled = (packId: string) =>
		pending?.packId === packId
			? pending.enabled
			: enabled.some((pack) => pack.id === packId);
	// Packs enabled here stay listed even when the user cannot list the
	// organization's packs, so they can still be disabled.
	const packs = [
		...(installed ?? []),
		...enabled.filter(
			(pack) =>
				!installed?.some((installedPack) => installedPack.id === pack.id),
		),
	];
	return (
		<div className="flex flex-col gap-3">
			{packs.length === 0 ? (
				<p className="m-0 text-sm text-content-secondary">
					{installed
						? "No plugin packs are installed in this organization."
						: "No plugin packs are enabled."}
				</p>
			) : (
				<ul className="m-0 flex list-none flex-col gap-2 p-0">
					{packs.map((pack) => (
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
									onCheckedChange={(checked) => onToggle(pack.id, checked)}
								/>
							) : (
								<SkillEnabledStatus enabled={isEnabled(pack.id)} />
							)}
						</li>
					))}
				</ul>
			)}
			{!installed && (
				<p className="m-0 text-xs text-content-secondary">
					Only enabled packs are shown. Organization admins install plugin packs
					in AI settings.
				</p>
			)}
		</div>
	);
};

type ForkPluginPackTogglesProps = {
	target: PluginPackTarget;
	organizationId: string;
	canEdit: boolean;
};

export const ForkPluginPackToggles: React.FC<ForkPluginPackTogglesProps> = ({
	target,
	organizationId,
	canEdit,
}) => {
	const queryClient = useQueryClient();
	const enabledQuery = useQuery(enabledPluginPacks(target));
	// Listing the organization's packs needs the organization skill read
	// permission, which members usually lack.
	const installedQuery = useQuery({
		...pluginPacks(organizationId),
		retry: false,
	});
	const toggleMutation = useMutation(
		setPluginPackEnabledMutation(queryClient, target),
	);
	const installedError =
		getErrorStatus(installedQuery.error) === 403
			? undefined
			: installedQuery.error;
	return (
		<ForkPluginPackTogglesView
			installed={installedQuery.data}
			enabled={
				installedQuery.isPending || installedError
					? undefined
					: enabledQuery.data
			}
			error={enabledQuery.error ?? installedError}
			isRetrying={enabledQuery.isFetching || installedQuery.isFetching}
			onRetry={() => {
				void enabledQuery.refetch();
				void installedQuery.refetch();
			}}
			canEdit={canEdit}
			pending={toggleMutation.isPending ? toggleMutation.variables : undefined}
			onToggle={(packId, enabled) =>
				toggleMutation.mutate(
					{ packId, enabled },
					{
						onError: (error) =>
							toast.error(
								getErrorMessage(error, "Failed to update the plugin pack."),
								{ description: getErrorDetail(error) },
							),
					},
				)
			}
		/>
	);
};
