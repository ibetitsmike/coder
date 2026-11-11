// This file is fork code: the fork-plugin-packs experiment's organization
// plugin packs page in AI settings.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { Navigate } from "react-router";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	createPluginPackMutation,
	deletePluginPackMutation,
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
	const updateMutation = useMutation(
		updatePluginPackMutation(queryClient, organization.id),
	);
	const deleteMutation = useMutation(
		deletePluginPackMutation(queryClient, organization.id),
	);
	const [isInstallOpen, setIsInstallOpen] = useState(false);
	const [deleting, setDeleting] = useState<ForkPluginPack>();

	return (
		<>
			<ForkPluginPacksTableView
				packs={packsQuery.data ?? []}
				access={access}
				action={picker}
				error={packsQuery.error}
				isLoading={packsQuery.isLoading}
				isRetrying={packsQuery.isFetching}
				onRetry={() => void packsQuery.refetch()}
				updatingPackId={
					updateMutation.isPending ? updateMutation.variables : undefined
				}
				onInstall={() => {
					installMutation.reset();
					setIsInstallOpen(true);
				}}
				onUpdate={(pack) =>
					updateMutation.mutate(pack.id, {
						onSuccess: (updated) =>
							toast.success(
								`${pack.name} is at ${updated.commit_sha.slice(0, 10)}.`,
							),
						onError: (error) =>
							toast.error(
								getErrorMessage(error, `Failed to update ${pack.name}.`),
								{ description: getErrorDetail(error) },
							),
					})
				}
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
					onConfirm={() =>
						deleteMutation.mutate(deleting.id, {
							onSuccess: () => {
								toast.success(`${deleting.name} deleted.`);
								setDeleting(undefined);
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
