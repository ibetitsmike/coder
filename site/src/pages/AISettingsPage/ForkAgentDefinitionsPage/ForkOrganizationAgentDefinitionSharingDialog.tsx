// This file is fork code: the fork-agent-definitions experiment's sharing
// dialog for organization agent definitions.

import { useMutation, useQuery, useQueryClient } from "react-query";
import {
	organizationAgentDefinitionACL,
	organizationAgentDefinitionACLAvailable,
	updateAgentDefinitionACL,
} from "#/api/queries/forkAgentDefinitions";
import { toast } from "#/components/Toaster/toast";
import { ACLPrincipalAutocomplete } from "../components/ACLPrincipalAutocomplete";
import { selectedPrincipal, sharingDialogData } from "../components/aclSharing";
import { ResourceSharingDialog } from "../components/ResourceSharingDialog";

type ForkOrganizationAgentDefinitionSharingDialogProps = {
	organizationId: string;
	definitionName: string;
	onClose: () => void;
	onCloseAutoFocus?: (event: Event) => void;
};

export const ForkOrganizationAgentDefinitionSharingDialog: React.FC<
	ForkOrganizationAgentDefinitionSharingDialogProps
> = ({ organizationId, definitionName, onClose, onCloseAutoFocus }) => {
	const queryClient = useQueryClient();
	const aclOptions = organizationAgentDefinitionACL(
		organizationId,
		definitionName,
	);
	const aclQuery = useQuery({ ...aclOptions, refetchOnMount: "always" });
	const updateMutation = useMutation(updateAgentDefinitionACL(queryClient));
	const data = aclQuery.data ? sharingDialogData(aclQuery.data) : undefined;

	const close = () => {
		onClose();
		queryClient.removeQueries({ queryKey: aclOptions.queryKey, exact: true });
	};

	return (
		<ResourceSharingDialog
			title="Agent definition permissions"
			description={
				<>
					Manage which organization members and groups can use {definitionName}.
					Site owners, organization admins, and auditors can use it whenever it
					is enabled.
				</>
			}
			loadingLabel="Loading agent definition permissions"
			emptyTitle="No members or groups have permission yet"
			tableLabel="Agent definition permissions for members and groups"
			roleLabel="Use"
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
						organizationAgentDefinitionACLAvailable(
							organizationId,
							definitionName,
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
					{ organizationId, name: definitionName, req },
					{
						onSuccess: () => {
							toast.success(`Permissions for "${definitionName}" updated.`);
							close();
						},
					},
				)
			}
		/>
	);
};
