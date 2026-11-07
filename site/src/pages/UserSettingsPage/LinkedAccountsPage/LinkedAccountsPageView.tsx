import { useId, useRef } from "react";
import type {
	LinkablePlugin,
	PluginLinkCode,
	PluginUserLink,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import { CopyButton } from "#/components/CopyButton/CopyButton";
import { EmptyState } from "#/components/EmptyState/EmptyState";
import { Loader } from "#/components/Loader/Loader";
import { Spinner } from "#/components/Spinner/Spinner";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "#/components/Table/Table";
import { TableEmpty } from "#/components/TableEmpty/TableEmpty";
import { DATE_FORMAT, formatDateTime } from "#/utils/time";

type LinkedAccountsPageViewProps = {
	plugins?: readonly LinkablePlugin[];
	getPluginsError?: unknown;
	linkCodes: Readonly<Partial<Record<string, PluginLinkCode>>>;
	now: Date;
	creatingLinkCodePluginId?: string;
	onCreateLinkCode: (plugin: LinkablePlugin) => void;
	/**
	 * focusReturnTargets lists the buttons, in order of preference, that should
	 * get focus back when the unlink confirmation closes.
	 */
	onUnlink: (
		plugin: LinkablePlugin,
		link: PluginUserLink,
		focusReturnTargets: readonly (HTMLButtonElement | null)[],
	) => void;
};

export const LinkedAccountsPageView: React.FC<LinkedAccountsPageViewProps> = ({
	plugins,
	getPluginsError,
	linkCodes,
	now,
	creatingLinkCodePluginId,
	onCreateLinkCode,
	onUnlink,
}) => {
	if (!plugins) {
		return getPluginsError ? (
			<ErrorAlert error={getPluginsError} />
		) : (
			<Loader />
		);
	}

	return (
		<div className="flex flex-col gap-8">
			{Boolean(getPluginsError) && <ErrorAlert error={getPluginsError} />}
			{plugins.length === 0 ? (
				<EmptyState
					message="No plugins available"
					description="An administrator must install a plugin that supports chats before you can link an external account."
				/>
			) : (
				plugins.map((plugin) => (
					<PluginSection
						key={plugin.id}
						plugin={plugin}
						linkCode={linkCodes[plugin.id]}
						now={now}
						isCreatingLinkCode={creatingLinkCodePluginId === plugin.id}
						onCreateLinkCode={() => onCreateLinkCode(plugin)}
						onUnlink={(link, focusReturnTargets) =>
							onUnlink(plugin, link, focusReturnTargets)
						}
					/>
				))
			)}
		</div>
	);
};

type PluginSectionProps = {
	plugin: LinkablePlugin;
	linkCode?: PluginLinkCode;
	now: Date;
	isCreatingLinkCode: boolean;
	onCreateLinkCode: () => void;
	onUnlink: (
		link: PluginUserLink,
		focusReturnTargets: readonly (HTMLButtonElement | null)[],
	) => void;
};

const PluginSection: React.FC<PluginSectionProps> = ({
	plugin,
	linkCode,
	now,
	isCreatingLinkCode,
	onCreateLinkCode,
	onUnlink,
}) => {
	const headingId = useId();
	const linkCodeButtonRef = useRef<HTMLButtonElement>(null);

	return (
		<section aria-labelledby={headingId} className="flex flex-col gap-4">
			<div className="flex items-start justify-between gap-4">
				<div className="flex flex-col gap-1">
					<h2
						id={headingId}
						className="m-0 text-base font-semibold text-content-primary"
					>
						{plugin.name}
					</h2>
					<p className="m-0 text-sm text-content-secondary">
						Version {plugin.version} in {plugin.organization_name}
					</p>
				</div>
				<Button
					ref={linkCodeButtonRef}
					variant="outline"
					disabled={isCreatingLinkCode}
					onClick={onCreateLinkCode}
				>
					<Spinner loading={isCreatingLinkCode} />
					Get link code
				</Button>
			</div>

			{linkCode && <LinkCode linkCode={linkCode} now={now} />}

			<Table aria-label={`${plugin.name} linked accounts`}>
				<TableHeader>
					<TableRow>
						<TableHead>Account</TableHead>
						<TableHead>Linked</TableHead>
						<TableHead className="w-[1%]">
							<span className="sr-only">Actions</span>
						</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{plugin.links.length === 0 ? (
						<TableEmpty
							isCompact
							message="No linked accounts"
							description="Get a link code and send it to the bot to link an account."
						/>
					) : (
						plugin.links.map((link) => (
							<TableRow key={link.external_id}>
								<TableCell className="font-mono">{link.external_id}</TableCell>
								<TableCell>
									{formatDateTime(link.created_at, DATE_FORMAT.FULL_DATETIME)}
								</TableCell>
								<TableCell>
									<Button
										variant="outline"
										size="sm"
										aria-label={`Unlink ${link.external_id}`}
										onClick={(event) =>
											onUnlink(link, [
												event.currentTarget,
												linkCodeButtonRef.current,
											])
										}
									>
										Unlink
									</Button>
								</TableCell>
							</TableRow>
						))
					)}
				</TableBody>
			</Table>
		</section>
	);
};

type LinkCodeProps = {
	linkCode: PluginLinkCode;
	now: Date;
};

const LinkCode: React.FC<LinkCodeProps> = ({ linkCode, now }) => {
	const command = `/link ${linkCode.code}`;
	const expiresAt = new Date(linkCode.expires_at);

	if (expiresAt <= now) {
		return (
			<div className="rounded-lg border border-solid border-border bg-surface-secondary p-4">
				<p className="m-0 text-sm text-content-secondary">
					The link code{" "}
					<code className="font-mono font-semibold">{linkCode.code}</code>{" "}
					expired. Get a new link code to link an account.
				</p>
			</div>
		);
	}

	return (
		<div className="flex flex-col gap-2 rounded-lg border border-solid border-border bg-surface-secondary p-4">
			<div className="flex items-center gap-2">
				<span className="font-mono text-3xl font-semibold tracking-wider text-content-primary">
					{linkCode.code}
				</span>
				<CopyButton text={command} label="Copy link command" />
			</div>
			<p className="m-0 text-sm text-content-primary">
				Send <code className="font-mono font-semibold">{command}</code> to the
				bot.
			</p>
			<p className="m-0 text-xs text-content-secondary">
				The code works once and expires at{" "}
				{formatDateTime(expiresAt, DATE_FORMAT.FULL_DATETIME)}.
			</p>
		</div>
	);
};
