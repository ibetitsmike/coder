// This file is fork code: the fork-plugin-packs experiment's organization
// plugin packs page in AI settings.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { Navigate } from "react-router";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	createPluginPackMutation,
	deletePluginPackMutation,
	isPluginPackGone,
	pluginPacks,
	updatePluginPackMutation,
} from "#/api/queries/forkPluginPacks";
import { organizationsPermissions } from "#/api/queries/organizations";
import type { ForkPluginPack, Organization } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { ConfirmDialog } from "#/components/Dialog/ConfirmDialog/ConfirmDialog";
import { Loader } from "#/components/Loader/Loader";
import { toast } from "#/components/Toaster/toast";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { RequirePermission } from "#/modules/permissions/RequirePermission";
import { useDialogFocusReturn } from "#/pages/AgentsPage/components/useDialogFocusReturn";
import { pageTitle } from "#/utils/page";
import { OrganizationPicker } from "../MCPServersPage/components/OrganizationPicker";
import { useRequestedSkillsOrganization } from "../SkillsPage/forkRequestedSkillsOrganization";
import { readableSkillsOrganizations } from "../SkillsPage/readableSkillsOrganizations";
import { ForkInstallPluginPackDialog } from "./ForkInstallPluginPackDialog";
import {
	type ForkPluginPacksAccess,
	ForkPluginPacksTableView,
} from "./ForkPluginPacksTableView";

type OrganizationPluginPacksProps = {
	organization: Organization;
	access: ForkPluginPacksAccess;
	picker: React.ReactNode;
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
	// Every in-flight update keeps its row disabled. TanStack Query tracks
	// only the latest call's variables, and per-call callbacks run only for
	// the latest call, so the mutation-level callbacks track each pack.
	const [updatingPackIds, setUpdatingPackIds] = useState<ReadonlySet<string>>(
		new Set(),
	);
	const updateOptions = updatePluginPackMutation(queryClient, organization.id);
	const updateMutation = useMutation({
		...updateOptions,
		onMutate: (pack) => {
			setUpdatingPackIds((current) => new Set(current).add(pack.id));
		},
		onSuccess: (...args) => {
			const [updated, pack] = args;
			toast.success(`${pack.name} is at ${updated.commit_sha.slice(0, 10)}.`);
			return updateOptions.onSuccess?.(...args);
		},
		onError: (error, pack) => {
			toast.error(getErrorMessage(error, `Failed to update ${pack.name}.`), {
				description: getErrorDetail(error),
			});
		},
		onSettled: (_data, _error, pack) => {
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
	const [deleting, setDeleting] = useState<ForkPluginPack>();
	const focusReturn = useDialogFocusReturn();

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
				onInstall={() => {
					installMutation.reset();
					setIsInstallOpen(true);
				}}
				onUpdate={(pack) => updateMutation.mutate(pack)}
				onDelete={(pack) => {
					deleteMutation.reset();
					setDeleting(pack);
				}}
			/>
			{isInstallOpen && (
				<ForkInstallPluginPackDialog
					error={installMutation.error}
					isInstalling={installMutation.isPending}
					onClose={() => setIsInstallOpen(false)}
					onCloseAutoFocus={focusReturn.restoreFocus}
					onInstall={(req) =>
						installMutation.mutate(req, {
							onSuccess: (pack) => {
								toast.success(`${pack.name} installed.`);
								setIsInstallOpen(false);
							},
						})
					}
				/>
			)}
			{deleting && (
				<ConfirmDialog
					type="delete"
					open
					title={`Delete ${deleting.name}?`}
					confirmText="Delete"
					description={
						<>
							<p className="m-0">
								Projects and chats that enabled this pack lose its skills and
								agents.
							</p>
							{Boolean(deleteMutation.error) && (
								<ErrorAlert className="mt-3" error={deleteMutation.error} />
							)}
						</>
					}
					confirmLoading={deleteMutation.isPending}
					onClose={() => {
						if (!deleteMutation.isPending) {
							setDeleting(undefined);
						}
					}}
					onCloseAutoFocus={focusReturn.restoreFocus}
					onConfirm={() =>
						deleteMutation.mutate(deleting.id, {
							// A lost response can still have deleted the pack, so the
							// refetched list decides whether the delete is done.
							onSettled: (_data, error, packId) => {
								if (
									!error ||
									isPluginPackGone(queryClient, organization.id, packId)
								) {
									toast.success(`${deleting.name} deleted.`);
									setDeleting(undefined);
								}
							},
						})
					}
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

	// Plugin packs share the organization skill permissions on the server.
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
