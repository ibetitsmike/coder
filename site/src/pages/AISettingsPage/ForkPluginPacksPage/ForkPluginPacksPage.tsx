// This file is fork code: the fork-plugin-packs experiment's organization
// plugin packs page in AI settings.

import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { Navigate } from "react-router";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import { isNewer } from "#/api/queries/forkListCache";
import {
	cachedPluginPacks,
	createPluginPackMutation,
	deletePluginPackMutation,
	isPluginPackAbsentFromCache,
	pluginPacks,
	reconcileInstalledPluginPack,
	reconcileUpdatedPluginPack,
	updatePluginPackMutation,
} from "#/api/queries/forkPluginPacks";
import { organizationsPermissions } from "#/api/queries/organizations";
import type {
	CreateForkPluginPackRequest,
	ForkPluginPack,
	Organization,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import { toast } from "#/components/Toaster/toast";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { RequirePermission } from "#/modules/permissions/RequirePermission";
import { useDialogFocusReturn } from "#/pages/AgentsPage/components/useDialogFocusReturn";
import type { SkillAccess } from "#/pages/AgentsPage/utils/skills";
import { pageTitle } from "#/utils/page";
import { OrganizationPicker } from "../MCPServersPage/components/OrganizationPicker";
import { useRequestedSkillsOrganization } from "../SkillsPage/forkRequestedSkillsOrganization";
import { readableSkillsOrganizations } from "../SkillsPage/readableSkillsOrganizations";
import { ForkDeletePluginPackDialog } from "./ForkDeletePluginPackDialog";
import { ForkInstallPluginPackDialog } from "./ForkInstallPluginPackDialog";
import { ForkPluginPacksTableView } from "./ForkPluginPacksTableView";

type OrganizationPluginPacksProps = {
	organization: Organization;
	access: SkillAccess;
	picker: React.ReactNode;
};

const toastUpdateResult = (
	pack: ForkPluginPack,
	updated: ForkPluginPack | undefined,
	error: unknown,
) => {
	if (!updated) {
		toast.error(getErrorMessage(error, `Failed to update ${pack.name}.`), {
			description: getErrorDetail(error),
		});
		return;
	}

	toast.success(
		`${pack.name} updated to commit ${updated.commit_sha.slice(0, 10)}.`,
	);
};

const OrganizationPluginPacks: React.FC<OrganizationPluginPacksProps> = ({
	organization,
	access,
	picker,
}) => {
	const queryClient = useQueryClient();
	const packsQuery = useQuery(pluginPacks(organization.id));
	const installMutation = useMutation(
		createPluginPackMutation(queryClient, organization.id),
	);
	// Mutation-level callbacks track each pack, because per-call callbacks only
	// fire for the latest call.
	const [updatingPackIds, setUpdatingPackIds] = useState<ReadonlySet<string>>(
		new Set(),
	);
	const updateOptions = updatePluginPackMutation(queryClient, organization.id);
	const updateMutation = useMutation({
		...updateOptions,
		onMutate: (pack) => {
			setUpdatingPackIds((current) => new Set(current).add(pack.id));
		},
		onSettled: async (...args) => {
			await updateOptions.onSettled?.(...args);
			const [data, error, pack] = args;
			// A later update that another administrator made may already be
			// cached, and the toast names the commit the table shows.
			const cached = cachedPluginPacks(queryClient, organization.id)?.find(
				(candidate) => candidate.id === pack.id,
			);
			const updated = data
				? cached && isNewer(cached, data)
					? cached
					: data
				: reconcileUpdatedPluginPack(queryClient, organization.id, pack, error);
			toastUpdateResult(pack, updated, error);
			setUpdatingPackIds((current) => {
				const next = new Set(current);
				next.delete(pack.id);
				return next;
			});
		},
	});
	const deleteMutation = useMutation(
		deletePluginPackMutation(queryClient, organization.id),
	);
	const [isInstallOpen, setIsInstallOpen] = useState(false);
	const [packToDelete, setPackToDelete] = useState<ForkPluginPack>();
	const focusReturn = useDialogFocusReturn();
	const tableRef = useRef<HTMLTableElement>(null);
	// Without a create permission, deleting the last pack leaves neither a row
	// button nor an install button, so focus falls back to the table.
	const restoreFocus = (event: Event) => {
		focusReturn.restoreFocus(event);
		if (!event.defaultPrevented && tableRef.current) {
			event.preventDefault();
			tableRef.current.focus();
		}
	};

	const installPack = (req: CreateForkPluginPackRequest) => {
		const cachedPacks = cachedPluginPacks(queryClient, organization.id);
		const knownPackIds = cachedPacks?.map((pack) => pack.id);
		installMutation.mutate(req, {
			onSettled: (data, error) => {
				const pack =
					data ??
					reconcileInstalledPluginPack(
						queryClient,
						organization.id,
						req,
						knownPackIds,
						error,
					);
				if (!pack) {
					return;
				}

				toast.success(`${pack.name} installed.`);
				setIsInstallOpen(false);
			},
		});
	};

	const deletePack = (pack: ForkPluginPack) => {
		deleteMutation.mutate(pack.id, {
			onSettled: (_data, error) => {
				if (
					error &&
					!isPluginPackAbsentFromCache(queryClient, organization.id, pack.id)
				) {
					return;
				}

				toast.success(`${pack.name} deleted.`);
				setPackToDelete(undefined);
			},
		});
	};

	return (
		<>
			<ForkPluginPacksTableView
				packs={packsQuery.data ?? []}
				access={access}
				action={picker}
				error={packsQuery.error}
				isLoadingError={packsQuery.isLoadingError}
				isLoading={packsQuery.isLoading}
				isRetrying={packsQuery.isFetching}
				onRetry={() => void packsQuery.refetch()}
				updatingPackIds={updatingPackIds}
				focusReturn={focusReturn}
				tableRef={tableRef}
				onInstall={() => {
					installMutation.reset();
					setIsInstallOpen(true);
				}}
				onUpdate={(pack) => updateMutation.mutate(pack)}
				onDelete={(pack) => {
					deleteMutation.reset();
					setPackToDelete(pack);
				}}
			/>
			{isInstallOpen && (
				<ForkInstallPluginPackDialog
					error={installMutation.error}
					isInstalling={installMutation.isPending}
					onClose={() => setIsInstallOpen(false)}
					onCloseAutoFocus={restoreFocus}
					onInstall={installPack}
				/>
			)}
			{packToDelete && (
				<ForkDeletePluginPackDialog
					pack={packToDelete}
					error={deleteMutation.error}
					isDeleting={deleteMutation.isPending}
					onClose={() => setPackToDelete(undefined)}
					onCloseAutoFocus={restoreFocus}
					onConfirm={() => deletePack(packToDelete)}
				/>
			)}
		</>
	);
};

const ForkPluginPacksPage: React.FC = () => {
	const { permissions } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const isEnabled = experiments.includes("fork-plugin-packs");
	const organizationPermissionsQuery = useQuery({
		...organizationsPermissions(
			organizations.map((organization) => organization.id),
		),
		enabled: isEnabled && permissions.viewAnyOrganizationSkills,
	});

	const readableOrganizations = readableSkillsOrganizations(
		organizations,
		organizationPermissionsQuery.data,
	);
	const { organization, setOrganization } = useRequestedSkillsOrganization(
		readableOrganizations,
	);

	if (!isEnabled) {
		return <Navigate to="/ai/settings" replace />;
	}

	let content: React.ReactNode;
	if (organizationPermissionsQuery.isLoadingError) {
		content = <ErrorAlert error={organizationPermissionsQuery.error} />;
	} else if (!organizationPermissionsQuery.data) {
		content = <Loader />;
	} else {
		const organizationPermissions = organization
			? organizationPermissionsQuery.data[organization.id]
			: undefined;
		content = (
			<RequirePermission isFeatureVisible={Boolean(organization)}>
				{organizationPermissionsQuery.isRefetchError && (
					<ErrorAlert
						className="mb-4"
						error={organizationPermissionsQuery.error}
					/>
				)}
				{organization && (
					<OrganizationPluginPacks
						key={organization.id}
						organization={organization}
						access={{
							create: Boolean(organizationPermissions?.createOrganizationSkill),
							update: Boolean(organizationPermissions?.updateOrganizationSkill),
							delete: Boolean(organizationPermissions?.deleteOrganizationSkill),
						}}
						picker={
							<OrganizationPicker
								id="plugin-packs-organization"
								className="w-full sm:w-60"
								organizations={readableOrganizations}
								organization={organization}
								onChange={setOrganization}
								showLabel={false}
							/>
						}
					/>
				)}
			</RequirePermission>
		);
	}

	return (
		<RequirePermission isFeatureVisible={permissions.viewAnyOrganizationSkills}>
			<title>{pageTitle("Plugin packs", "AI Settings")}</title>
			{content}
		</RequirePermission>
	);
};

export default ForkPluginPacksPage;
