// This file is fork code: wires the fork-agent-definitions table to the API.

import { useQuery } from "react-query";
import { userAgentDefinitions } from "#/api/queries/forkAgentDefinitions";
import { ForkAgentDefinitionsTableView } from "./ForkAgentDefinitionsTableView";

type ForkAgentDefinitionsTableProps = {
	organizationId: string;
	action?: React.ReactNode;
};

export const ForkAgentDefinitionsTable: React.FC<
	ForkAgentDefinitionsTableProps
> = ({ organizationId, action }) => {
	const definitionsQuery = useQuery(userAgentDefinitions(organizationId));

	return (
		<ForkAgentDefinitionsTableView
			definitions={definitionsQuery.data ?? []}
			action={action}
			error={definitionsQuery.error}
			isLoading={definitionsQuery.isLoading}
			isRetrying={definitionsQuery.isFetching}
			onRetry={() => {
				void definitionsQuery.refetch();
			}}
		/>
	);
};
