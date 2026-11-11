// This file is fork code: the fork-agent-definitions experiment's available
// agents page.

import { useState } from "react";
import { useInfiniteQuery, useQuery } from "react-query";
import { Link, Navigate } from "react-router";
import { chatProjects } from "#/api/queries/chatProjects";
import { infiniteChats } from "#/api/queries/chats";
import { agentTypes } from "#/api/queries/forkAgentDefinitions";
import { workspaces } from "#/api/queries/workspaces";
import type { ForkAgentType } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Badge } from "#/components/Badge/Badge";
import { OrganizationField } from "#/components/OrganizationAutocomplete/OrganizationAutocomplete";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "#/components/Select/Select";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "#/components/Table/Table";
import { TableEmpty } from "#/components/TableEmpty/TableEmpty";
import { TableLoader } from "#/components/TableLoader/TableLoader";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import {
	getMemberOrganizations,
	useDashboard,
} from "#/modules/dashboard/useDashboard";
import { agentTypeSourceLabels } from "../../components/ChatElements/ForkCompactAgentTypeSelector";
import { SectionHeader } from "../../components/SectionHeader";
import {
	DEFAULT_AGENT_SIDEBAR_FILTERS,
	getSidebarChatListInput,
} from "../../utils/agentSidebarFilters";

const noneValue = "none";
const recentChatLimit = 3;

// Chats store a definition's name without its source prefix, so a name that
// several sources define matches the chats of each of them.
const storedAgentType = ({ name, source }: ForkAgentType) =>
	name.startsWith(`${source}/`) ? name.slice(source.length + 1) : name;

// A pick from another organization is dropped instead of being sent.
const keepPick = (options: readonly { id: string }[], id?: string) =>
	options.some((option) => option.id === id) ? id : undefined;

type ContextSelectProps = {
	readonly label: string;
	readonly options: readonly { readonly id: string; readonly name: string }[];
	readonly value: string | undefined;
	readonly onChange: (id: string | undefined) => void;
};

const ContextSelect: React.FC<ContextSelectProps> = ({
	label,
	options,
	value,
	onChange,
}) => (
	<Select
		value={value ?? noneValue}
		onValueChange={(next) => onChange(next === noneValue ? undefined : next)}
	>
		<SelectTrigger className="w-full sm:w-52" aria-label={label}>
			<SelectValue />
		</SelectTrigger>
		<SelectContent>
			<SelectItem value={noneValue}>No {label.toLowerCase()}</SelectItem>
			{options.map((option) => (
				<SelectItem key={option.id} value={option.id}>
					{option.name}
				</SelectItem>
			))}
		</SelectContent>
	</Select>
);

const ForkAvailableAgentsPage: React.FC = () => {
	const { user } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const memberOrganizations = getMemberOrganizations(organizations, user);
	const [selectedOrganizationId, setSelectedOrganizationId] =
		useState<string>();
	const [pickedProjectId, setPickedProjectId] = useState<string>();
	const [pickedWorkspaceId, setPickedWorkspaceId] = useState<string>();
	const isEnabled = experiments.includes("fork-agent-definitions");
	const organization =
		memberOrganizations.find((org) => org.id === selectedOrganizationId) ??
		memberOrganizations.find((org) => org.is_default) ??
		memberOrganizations.at(0);
	const organizationId = organization?.id ?? "";

	const projectsQuery = useQuery({ ...chatProjects(), enabled: isEnabled });
	const workspacesQuery = useQuery({
		...workspaces({ q: "owner:me", limit: 0 }),
		enabled: isEnabled,
	});
	// The sidebar's default chat list, so the page reuses its cache.
	const chatsQuery = useInfiniteQuery({
		...infiniteChats(getSidebarChatListInput(DEFAULT_AGENT_SIDEBAR_FILTERS)),
		enabled: isEnabled,
	});
	const projects = (projectsQuery.data ?? []).filter(
		(project) => project.organization_id === organizationId,
	);
	const workspaceOptions = (workspacesQuery.data?.workspaces ?? []).filter(
		(workspace) => workspace.organization_id === organizationId,
	);
	const projectId = keepPick(projects, pickedProjectId);
	const workspaceId = keepPick(workspaceOptions, pickedWorkspaceId);
	const agentTypesQuery = useQuery({
		...agentTypes(organizationId, {
			project: projectId,
			workspace: workspaceId,
		}),
		enabled: isEnabled && organizationId !== "",
	});

	if (!isEnabled) {
		return <Navigate to="/agents/settings" replace />;
	}

	const types = agentTypesQuery.data ?? [];
	// Root chats that started as an agent type and child chats spawned as one.
	const chats = (chatsQuery.data?.pages.flat() ?? [])
		.flatMap((root) => [root, ...root.children])
		.filter((chat) => chat.organization_id === organizationId)
		.sort((a, b) => b.updated_at.localeCompare(a.updated_at));
	let tableBody: React.ReactNode;
	if (agentTypesQuery.isLoading) {
		tableBody = <TableLoader />;
	} else if (types.length === 0) {
		tableBody = <TableEmpty message="No agents are available." />;
	} else {
		tableBody = types.map((type) => {
			const typeChats = chats
				.filter((chat) => chat.agent_type === storedAgentType(type))
				.slice(0, recentChatLimit);
			return (
				<TableRow key={type.name}>
					<TableCell>
						<div className="font-medium text-content-primary">{type.name}</div>
						<div className="text-content-secondary">{type.description}</div>
					</TableCell>
					<TableCell>
						<Badge size="sm">
							{agentTypeSourceLabels[type.source] ?? type.source}
						</Badge>
					</TableCell>
					<TableCell className="text-content-secondary">
						{type.root_available ? "Available" : type.root_unavailable_reason}
					</TableCell>
					<TableCell>
						{typeChats.length === 0 ? (
							<span className="text-content-secondary">None</span>
						) : (
							<ul className="m-0 flex list-none flex-col gap-1 p-0">
								{typeChats.map((chat) => (
									<li key={chat.id} className="max-w-60 truncate">
										<Link to={`/agents/${chat.id}`}>{chat.title}</Link>
									</li>
								))}
							</ul>
						)}
					</TableCell>
				</TableRow>
			);
		});
	}

	return (
		<div className="flex flex-col gap-8">
			<SectionHeader
				label="Available agents"
				description="The agent types your chats in this organization can start as or spawn with spawn_agent. Pick a project or a workspace to add its agents."
				action={
					organization && (
						<OrganizationField
							id="available-agents-organization"
							className="w-full sm:w-60"
							organizations={memberOrganizations}
							organization={organization}
							onChange={(org) => setSelectedOrganizationId(org.id)}
							showLabel={false}
						/>
					)
				}
			/>
			<div className="flex flex-col gap-2 sm:flex-row">
				<ContextSelect
					label="Project"
					options={projects}
					value={projectId}
					onChange={setPickedProjectId}
				/>
				<ContextSelect
					label="Workspace"
					options={workspaceOptions}
					value={workspaceId}
					onChange={setPickedWorkspaceId}
				/>
			</div>
			{agentTypesQuery.error && <ErrorAlert error={agentTypesQuery.error} />}
			{!agentTypesQuery.isLoadingError && (
				<Table aria-label="Available agents">
					<TableHeader>
						<TableRow>
							<TableHead className="w-full">Agent</TableHead>
							<TableHead className="whitespace-nowrap">Tier</TableHead>
							<TableHead className="whitespace-nowrap">Availability</TableHead>
							<TableHead className="whitespace-nowrap">Recent chats</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody size="lg">{tableBody}</TableBody>
				</Table>
			)}
		</div>
	);
};

export default ForkAvailableAgentsPage;
