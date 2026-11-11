// This file is fork code: the fork-plugin-packs experiment's organization
// plugin pack table.

import type { ForkPluginPack } from "#/api/typesGenerated";
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
import { RetryButton } from "#/pages/AgentsPage/components/RetryButton";
import { SectionHeader } from "#/pages/AgentsPage/components/SectionHeader";
import { formatUpdatedAt } from "#/pages/AgentsPage/components/SkillsTableView";

const title = "Plugin packs";

type ForkPluginPacksTableViewProps = {
	packs: readonly ForkPluginPack[];
	action?: React.ReactNode;
	error: unknown;
	isLoading: boolean;
	isRetrying: boolean;
	onRetry: () => void;
};

/**
 * Lists an organization's installed plugin packs. It shows a loader while
 * `isLoading`, a retryable empty state when the first load fails, and a
 * retryable alert above the cached rows when a later refetch fails.
 */
export const ForkPluginPacksTableView: React.FC<
	ForkPluginPacksTableViewProps
> = ({ packs, action, error, isLoading, isRetrying, onRetry }) => {
	let tableBody: React.ReactNode;
	if (isLoading) {
		tableBody = <TableLoader />;
	} else if (packs.length === 0 && error) {
		tableBody = (
			<TableEmpty
				message="Failed to load plugin packs"
				cta={
					<RetryButton
						variant="outline"
						isRetrying={isRetrying}
						onRetry={onRetry}
					/>
				}
			/>
		);
	} else if (packs.length === 0) {
		tableBody = (
			<TableEmpty
				message="No plugin packs installed"
				description="Install a Claude Code or Cursor plugin from Git, then enable it on a project or chat to use its skills and agents."
			/>
		);
	} else {
		tableBody = packs.map((pack) => (
			<TableRow key={pack.id}>
				<TableCell className="max-w-48 truncate" title={pack.name}>
					{pack.name}
				</TableCell>
				<TableCell
					className="w-full max-w-0 truncate"
					title={`${pack.source_url} ${pack.source_path}`.trim()}
				>
					{pack.source_url}
					{pack.source_path && (
						<span className="text-content-secondary">
							{" "}
							/ {pack.source_path}
						</span>
					)}
				</TableCell>
				<TableCell className="whitespace-nowrap">
					{pack.ref}{" "}
					<code className="text-content-secondary" title={pack.commit_sha}>
						{pack.commit_sha.slice(0, 10)}
					</code>
				</TableCell>
				<TableCell className="hidden whitespace-nowrap sm:table-cell">
					{formatUpdatedAt(pack.updated_at)}
				</TableCell>
			</TableRow>
		));
	}

	return (
		<div className="flex flex-col gap-8">
			<SectionHeader
				label={title}
				description="Plugin packs installed in this organization. Projects and chats choose which packs they use."
				action={action}
			/>
			{Boolean(error) && packs.length > 0 && (
				<ErrorAlert
					error={error}
					actions={<RetryButton isRetrying={isRetrying} onRetry={onRetry} />}
				/>
			)}
			<Table aria-label={title}>
				<TableHeader>
					<TableRow>
						<TableHead className="whitespace-nowrap">Name</TableHead>
						<TableHead className="w-full">Source</TableHead>
						<TableHead className="whitespace-nowrap">Ref</TableHead>
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
