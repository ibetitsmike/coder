// This file is fork code: the fork-plugin-packs experiment's list of plugin
// pack switches.

import { useId } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import type { PluginPackTarget } from "#/api/forkPluginPackEnablement";
import {
	availablePluginPacks,
	enabledPluginPacks,
	excludedPluginPacks,
	type PluginPackState,
	type PluginPackToggle,
	pluginPackToggle,
	projectPluginPacks,
	setPluginPackEnabledMutation,
} from "#/api/queries/forkPluginPackEnablement";
import type { ForkEnabledPluginPack } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import { Switch } from "#/components/Switch/Switch";
import { toast } from "#/components/Toaster/toast";
import { RetryButton } from "./RetryButton";
import { SkillEnabledStatus } from "./SkillEnabledSwitch";

const has = (packs: readonly ForkEnabledPluginPack[], packId: string) =>
	packs.some((pack) => pack.id === packId);

type ForkPluginPackTogglesViewProps = {
	installed: readonly ForkEnabledPluginPack[] | undefined;
	enabled: readonly ForkEnabledPluginPack[] | undefined;
	/** The packs a chat's project enables; empty for a project. */
	inherited: readonly ForkEnabledPluginPack[] | undefined;
	/** The packs a chat turns off; empty for a project. */
	excluded: readonly ForkEnabledPluginPack[] | undefined;
	error: unknown;
	isRetrying: boolean;
	onRetry: () => void;
	canEdit: boolean;
	pending: PluginPackToggle | undefined;
	onToggle: (toggle: PluginPackToggle) => void;
};

/**
 * Lists the plugin packs in `installed`, one switch per pack, and reports
 * toggles through `onToggle`. A switch is on when the target or the chat's
 * project enables the pack and the chat does not turn it off. The other lists
 * only set each switch's state, so a pack missing from `installed` is not
 * shown. A subagent's switches ignore the packs its ancestor chats enable or
 * turn off.
 */
export const ForkPluginPackTogglesView: React.FC<
	ForkPluginPackTogglesViewProps
> = ({
	installed,
	enabled,
	inherited,
	excluded,
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
	const projectLabelId = useId();
	if (!installed || !enabled || !inherited || !excluded) {
		return errorAlert ?? <Loader />;
	}
	const stateOf = (packId: string): PluginPackState => ({
		direct: has(enabled, packId),
		inherited: has(inherited, packId),
		excluded: has(excluded, packId),
	});
	const isEnabled = (packId: string) => {
		if (pending?.pack.id === packId) {
			return pending.enabled;
		}
		const state = stateOf(packId);
		return (state.direct || state.inherited) && !state.excluded;
	};
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
							{has(inherited, pack.id) && (
								<span
									id={`${projectLabelId}-${pack.id}`}
									className="text-xs text-content-secondary"
								>
									Project
								</span>
							)}
							<code className="text-xs text-content-secondary">
								{pack.commit_sha.slice(0, 10)}
							</code>
							{canEdit ? (
								<Switch
									aria-label={`Enable ${pack.name}`}
									aria-describedby={
										has(inherited, pack.id)
											? `${projectLabelId}-${pack.id}`
											: undefined
									}
									checked={isEnabled(pack.id)}
									disabled={pending !== undefined}
									onCheckedChange={(checked) =>
										onToggle(pluginPackToggle(pack, checked, stateOf(pack.id)))
									}
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
	/** The project of a chat target, whose packs the chat inherits. */
	projectId?: string;
};

/**
 * Loads a chat or project target's plugin pack enablements and the packs
 * available to the caller in the organization, and saves each toggle for
 * that target. A chat target also loads the packs it turns off and those its
 * project enables.
 */
export const ForkPluginPackToggles: React.FC<ForkPluginPackTogglesProps> = ({
	target,
	organizationId,
	canEdit,
	projectId,
}) => {
	const queryClient = useQueryClient();
	const isChat = target.kind === "chat";
	const enabledQuery = useQuery(enabledPluginPacks(target));
	const availableQuery = useQuery(availablePluginPacks(organizationId));
	const excludedQuery = useQuery(excludedPluginPacks(target));
	const inheritedQuery = useQuery(projectPluginPacks(organizationId, projectId));
	// Refetching a skipped query throws, so Retry covers only the queries
	// that run.
	const queries = [
		enabledQuery,
		availableQuery,
		...(isChat ? [excludedQuery] : []),
		...(projectId !== undefined ? [inheritedQuery] : []),
	];
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
			inherited={projectId !== undefined ? inheritedQuery.data : []}
			excluded={isChat ? excludedQuery.data : []}
			error={queries.find((query) => query.error)?.error}
			isRetrying={queries.some((query) => query.isFetching)}
			onRetry={() => {
				for (const query of queries) {
					void query.refetch();
				}
			}}
			canEdit={canEdit}
			pending={toggleMutation.isPending ? toggleMutation.variables : undefined}
			onToggle={toggleMutation.mutate}
		/>
	);
};
