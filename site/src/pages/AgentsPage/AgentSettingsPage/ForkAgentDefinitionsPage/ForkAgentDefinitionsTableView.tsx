// This file is fork code: the fork-agent-definitions experiment's table.

import type { ForkAgentDefinitionMetadata } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
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
import { RetryButton } from "../../components/RetryButton";
import { SectionHeader } from "../../components/SectionHeader";
import { formatUpdatedAt } from "../../components/SkillsTableView";

const title = "Agent definitions";

type ForkAgentDefinitionsTableViewProps = {
	definitions: readonly ForkAgentDefinitionMetadata[];
	/** Rendered in the section header, for example an organization picker. */
	action?: React.ReactNode;
	error: unknown;
	isLoading: boolean;
	isRetrying: boolean;
	onRetry: () => void;
};

export const ForkAgentDefinitionsTableView: React.FC<
	ForkAgentDefinitionsTableViewProps
> = ({ definitions, action, error, isLoading, isRetrying, onRetry }) => {
	let tableBody: React.ReactNode;
	if (isLoading) {
		tableBody = <TableLoader />;
	} else if (definitions.length === 0 && error) {
		tableBody = (
			<TableEmpty
				message="Failed to load agent definitions"
				cta={
					<RetryButton
						variant="outline"
						isRetrying={isRetrying}
						onRetry={onRetry}
					/>
				}
			/>
		);
	} else if (definitions.length === 0) {
		tableBody = (
			<TableEmpty message="No personal agent definitions in this organization yet." />
		);
	} else {
		tableBody = definitions.map((definition) => (
			<TableRow key={definition.id}>
				<TableCell className="max-w-48 truncate" title={definition.name}>
					{definition.name}
				</TableCell>
				<TableCell
					className="w-full max-w-0 truncate"
					title={definition.description || undefined}
				>
					{definition.description || (
						<span className="text-content-disabled">No description</span>
					)}
				</TableCell>
				<TableCell className="whitespace-nowrap">
					{definition.enabled ? (
						"Enabled"
					) : (
						<span className="text-content-disabled">Disabled</span>
					)}
				</TableCell>
				<TableCell className="hidden whitespace-nowrap sm:table-cell">
					{formatUpdatedAt(definition.updated_at)}
				</TableCell>
			</TableRow>
		));
	}

	return (
		<div className="flex flex-col gap-8">
			<SectionHeader
				label={title}
				description="Custom agent types your chats can spawn with spawn_agent in this organization. Each definition is a Claude Code or Cursor agent markdown file."
				action={action}
			/>

			{Boolean(error) && (
				<ErrorAlert
					error={error}
					actions={
						definitions.length > 0 && (
							<RetryButton isRetrying={isRetrying} onRetry={onRetry} />
						)
					}
				/>
			)}

			<Table aria-label={title}>
				<TableHeader>
					<TableRow>
						<TableHead className="whitespace-nowrap">Name</TableHead>
						<TableHead className="w-full">Description</TableHead>
						<TableHead className="whitespace-nowrap">Status</TableHead>
						<TableHead className="hidden whitespace-nowrap sm:table-cell">
							Updated
						</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody size="lg">{tableBody}</TableBody>
			</Table>
		</div>
	);
};
