// This file is fork code: the fork-workflows experiment's sharing dialog for
// organization saved workflows.

import { useMutation, useQuery, useQueryClient } from "react-query";
import {
	organizationSavedWorkflowACL,
	organizationSavedWorkflowACLAvailable,
	updateSavedWorkflowACL,
} from "#/api/queries/forkSavedWorkflows";
import { toast } from "#/components/Toaster/toast";
import { ACLPrincipalAutocomplete } from "#/pages/AISettingsPage/components/ACLPrincipalAutocomplete";
import {
	selectedPrincipal,
	sharingDialogData,
} from "#/pages/AISettingsPage/components/aclSharing";
import { ResourceSharingDialog } from "#/pages/AISettingsPage/components/ResourceSharingDialog";

type ForkOrganizationSavedWorkflowSharingDialogProps = {
	organizationId: string;
	workflowName: string;
	onClose: () => void;
	onCloseAutoFocus?: (event: Event) => void;
};

/** Shares one organization saved workflow with members and groups. */
export const ForkOrganizationSavedWorkflowSharingDialog: React.FC<
	ForkOrganizationSavedWorkflowSharingDialogProps
> = ({ organizationId, workflowName, onClose, onCloseAutoFocus }) => {
	const queryClient = useQueryClient();
	const aclOptions = organizationSavedWorkflowACL(organizationId, workflowName);
	const aclQuery = useQuery({ ...aclOptions, refetchOnMount: "always" });
	const updateMutation = useMutation(updateSavedWorkflowACL(queryClient));
	// The editor copies its first data into draft state, so a cached ACL
	// from an earlier mount must not seed it before the refetch settles.
	const data =
		aclQuery.data && aclQuery.isFetchedAfterMount
			? sharingDialogData(aclQuery.data)
			: undefined;

	const close = () => {
		onClose();
		queryClient.removeQueries({ queryKey: aclOptions.queryKey, exact: true });
	};

	return (
		<ResourceSharingDialog
			title="Saved workflow permissions"
			description={
				<>
					Manage which organization members and groups can run {workflowName}.
					Site owners, organization admins, and auditors can always run
					organization workflows.
				</>
			}
			loadingLabel="Loading saved workflow permissions"
			emptyTitle="No members or groups have permission yet"
			tableLabel="Saved workflow permissions for members and groups"
			roleLabel="Run"
			confirmText="Save permissions"
			data={data}
			loadError={data ? null : aclQuery.error}
			refetchError={data ? aclQuery.error : null}
			saveError={updateMutation.error}
			isSaving={updateMutation.isPending}
			readRole="read"
			deletedRole=""
			renderAutocomplete={({ value, onChange, excludedPrincipalIds }) => (
				<ACLPrincipalAutocomplete
					value={value}
					onChange={onChange}
					availableQueryOptions={(options) =>
						organizationSavedWorkflowACLAvailable(
							organizationId,
							workflowName,
							options,
						)
					}
					excludedPrincipalIds={excludedPrincipalIds}
				/>
			)}
			getPrincipal={selectedPrincipal}
			onClose={close}
			onCloseAutoFocus={onCloseAutoFocus}
			onSave={(req) =>
				updateMutation.mutate(
					{ organizationId, name: workflowName, req },
					{
						onSuccess: () => {
							toast.success(`Permissions for "${workflowName}" updated.`);
							close();
						},
					},
				)
			}
		/>
	);
};
