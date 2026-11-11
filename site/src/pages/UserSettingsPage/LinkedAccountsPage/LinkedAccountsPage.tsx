import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	createPluginLinkCode,
	deletePluginLink,
	linkablePlugins,
	linkablePluginsKey,
} from "#/api/queries/plugins";
import type {
	LinkablePlugin,
	PluginLinkCode,
	PluginUserLink,
} from "#/api/typesGenerated";
import { ConfirmDialog } from "#/components/Dialog/ConfirmDialog/ConfirmDialog";
import {
	SettingsHeader,
	SettingsHeaderDescription,
	SettingsHeaderTitle,
} from "#/components/SettingsHeader/SettingsHeader";
import { toast } from "#/components/Toaster/toast";
import { pageTitle } from "#/utils/page";
import { LinkedAccountsPageView } from "./LinkedAccountsPageView";

type IssuedLinkCode = {
	linkCode: PluginLinkCode;
	knownExternalIds: readonly string[];
};

type LinkToUnlink = {
	plugin: LinkablePlugin;
	link: PluginUserLink;
};

const linkCodePollIntervalMs = 3_000;

// A code is used once an external ID that was not linked when the code was
// made shows up.
const getUnusedLinkCodes = (
	plugins: readonly LinkablePlugin[] | undefined,
	issuedCodes: Readonly<Partial<Record<string, IssuedLinkCode>>>,
): Partial<Record<string, PluginLinkCode>> => {
	const unused: Partial<Record<string, PluginLinkCode>> = {};
	for (const plugin of plugins ?? []) {
		const issued = issuedCodes[plugin.id];
		if (
			issued &&
			plugin.links.every((link) =>
				issued.knownExternalIds.includes(link.external_id),
			)
		) {
			unused[plugin.id] = issued.linkCode;
		}
	}
	return unused;
};

const LinkedAccountsPage: React.FC = () => {
	const queryClient = useQueryClient();
	const [issuedCodes, setIssuedCodes] = useState<
		Partial<Record<string, IssuedLinkCode>>
	>({});
	const pluginsQuery = useQuery({
		...linkablePlugins(),
		// Poll while a code waits to be sent so the new link shows up and the
		// code hides on its own. Keep polling after a failed fetch: queries
		// don't retry, so stopping would leave the page stale until the user
		// leaves and returns to the tab.
		refetchInterval: ({ state }) =>
			Object.values(getUnusedLinkCodes(state.data, issuedCodes)).some(
				(linkCode) =>
					linkCode !== undefined &&
					new Date(linkCode.expires_at).getTime() > Date.now(),
			)
				? linkCodePollIntervalMs
				: false,
	});
	const createLinkCodeMutation = useMutation(createPluginLinkCode(queryClient));
	const deleteLinkMutation = useMutation(deletePluginLink(queryClient));
	const [linkToUnlink, setLinkToUnlink] = useState<LinkToUnlink>();
	const focusReturnTargetsRef = useRef<readonly (HTMLButtonElement | null)[]>(
		[],
	);

	return (
		<>
			<title>{pageTitle("Linked accounts", "Settings")}</title>
			<SettingsHeader>
				<SettingsHeaderTitle>Linked accounts</SettingsHeaderTitle>
				<SettingsHeaderDescription>
					Link an external chat account, such as a Signal number, to your Coder
					account. Messages from a linked account go to Coder Agents chats that
					you own, and replies are sent back.
				</SettingsHeaderDescription>
			</SettingsHeader>

			<LinkedAccountsPageView
				plugins={pluginsQuery.data}
				getPluginsError={pluginsQuery.error}
				linkCodes={getUnusedLinkCodes(pluginsQuery.data, issuedCodes)}
				// The last fetch time is the clock for expiry, so polling refreshes
				// it and an expired code renders as expired without a timer.
				now={new Date(pluginsQuery.dataUpdatedAt)}
				creatingLinkCodePluginId={
					createLinkCodeMutation.isPending
						? createLinkCodeMutation.variables
						: undefined
				}
				onCreateLinkCode={(plugin) => {
					createLinkCodeMutation.mutate(plugin.id, {
						onSuccess: (linkCode) => {
							// The mutation refetched the list, so read it instead of the
							// plugin from the click.
							const current =
								queryClient
									.getQueryData<LinkablePlugin[]>(linkablePluginsKey)
									?.find(({ id }) => id === plugin.id) ?? plugin;
							setIssuedCodes((codes) => ({
								...codes,
								[plugin.id]: {
									linkCode,
									knownExternalIds: current.links.map(
										(link) => link.external_id,
									),
								},
							}));
						},
						onError: (error) => {
							toast.error(
								getErrorMessage(error, "Failed to create a link code."),
								{ description: getErrorDetail(error) },
							);
						},
					});
				}}
				onUnlink={(plugin, link, focusReturnTargets) => {
					focusReturnTargetsRef.current = focusReturnTargets;
					setLinkToUnlink({ plugin, link });
				}}
			/>

			<ConfirmDialog
				type="delete"
				open={linkToUnlink !== undefined}
				confirmLoading={deleteLinkMutation.isPending}
				title="Unlink account?"
				description={
					linkToUnlink &&
					`Messages from ${linkToUnlink.link.external_id} will no longer reach your chats through ${linkToUnlink.plugin.name}.`
				}
				confirmText="Unlink"
				onClose={() => setLinkToUnlink(undefined)}
				// A ref keeps the targets after the state clears on close.
				onCloseAutoFocus={(event) => {
					const target = focusReturnTargetsRef.current.find(
						(button) => button?.isConnected && !button.disabled,
					);
					if (target) {
						event.preventDefault();
						target.focus();
					}
				}}
				onConfirm={() => {
					if (!linkToUnlink) {
						return;
					}
					const { plugin, link } = linkToUnlink;
					deleteLinkMutation.mutate(
						{ pluginId: plugin.id, externalId: link.external_id },
						{
							onSuccess: () => {
								// The unlinked row and its button go away.
								focusReturnTargetsRef.current =
									focusReturnTargetsRef.current.slice(1);
								toast.success(`Unlinked ${link.external_id}.`);
							},
							onError: (error) => {
								toast.error(
									getErrorMessage(error, "Failed to unlink the account."),
									{ description: getErrorDetail(error) },
								);
							},
							onSettled: () => setLinkToUnlink(undefined),
						},
					);
				}}
			/>
		</>
	);
};

export default LinkedAccountsPage;
