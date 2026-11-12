// This file is fork code: the fork-plugin-packs experiment's composer pill.

import { PuzzleIcon } from "lucide-react";
import type React from "react";
import { useQuery } from "react-query";
import { getErrorMessage } from "#/api/errors";
import { chat } from "#/api/queries/chats";
import { enabledPluginPacks } from "#/api/queries/forkPluginPackEnablement";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { ComposerPillError, ComposerPillPopover } from "./ComposerPillPopover";
import { ForkPluginPackToggles } from "./ForkPluginPackToggles";

type ForkPluginPacksPillProps = {
	chatId: string;
};

/** Lets the chat owner turn plugin packs on or off for one chat. */
export const ForkPluginPacksPill: React.FC<ForkPluginPacksPillProps> = ({
	chatId,
}) => {
	const { user } = useAuthenticated();
	// The chat page already loads this chat, so mounting the pill reuses the
	// cached chat instead of fetching it again.
	const chatQuery = useQuery({ ...chat(chatId), refetchOnMount: false });
	const target = { kind: "chat", chatId } as const;
	const enabledQuery = useQuery(enabledPluginPacks(target));
	// Subagent chats carry no project and inherit the root chat's, so the
	// project's packs come from the root.
	const rootChatId =
		chatQuery.data &&
		!chatQuery.data.project_id &&
		chatQuery.data.root_chat_id !== chatId
			? chatQuery.data.root_chat_id
			: undefined;
	const rootChatQuery = useQuery({
		...chat(rootChatId ?? ""),
		enabled: rootChatId !== undefined,
		refetchOnMount: false,
	});
	if (!chatQuery.data || (rootChatId && !rootChatQuery.data)) {
		const error = chatQuery.error ?? rootChatQuery.error;
		return error ? <ForkPluginPacksPillError error={error} /> : null;
	}
	return (
		<ForkPluginPacksPillView
			enabledCount={enabledQuery.data?.length}
			isSubagent={Boolean(chatQuery.data.parent_chat_id)}
		>
			<ForkPluginPackToggles
				target={target}
				organizationId={chatQuery.data.organization_id}
				canEdit={chatQuery.data.owner_id === user.id}
				projectId={
					chatQuery.data.project_id ?? rootChatQuery.data?.project_id
				}
			/>
		</ForkPluginPacksPillView>
	);
};

type ForkPluginPacksPillErrorProps = { error: unknown };

/** The pill shown when the chat or its root chat fails to load. */
export const ForkPluginPacksPillError: React.FC<
	ForkPluginPacksPillErrorProps
> = ({ error }) => (
	<ComposerPillError
		label="Plugin packs failed to load"
		heading="Chat plugin packs"
		message={getErrorMessage(error, "Failed to load the chat.")}
	/>
);

type ForkPluginPacksPillViewProps = {
	/** `undefined` while the chat's packs load or after loading fails. */
	enabledCount: number | undefined;
	/** Subagent chats also use every pack enabled on their ancestor chats. */
	isSubagent: boolean;
	children: React.ReactNode;
};

/**
 * The composer pill for a chat's plugin packs. The trigger counts only the
 * packs enabled directly on the chat, the popover notes which other packs
 * apply, and `children` holds the toggles.
 */
export const ForkPluginPacksPillView: React.FC<
	ForkPluginPacksPillViewProps
> = ({ enabledCount, isSubagent, children }) => (
	<ComposerPillPopover
		label={
			enabledCount === undefined
				? "Plugin packs"
				: `Plugin packs, ${enabledCount} enabled on this chat`
		}
		heading="Chat plugin packs"
		trigger={
			<>
				<PuzzleIcon
					aria-hidden="true"
					className="size-3.5 shrink-0 text-content-secondary"
				/>
				{enabledCount !== undefined && enabledCount > 0 && (
					<span className="text-content-secondary tabular-nums">
						{enabledCount}
					</span>
				)}
			</>
		}
	>
		<p className="mb-3 mt-1 text-content-secondary">
			{isSubagent &&
				"Packs enabled on an ancestor chat apply too, and packs turned off on one stay off. "}
			If this chat belongs to a project, turning off one of the project's packs
			here turns it off only for this chat and its subagents. Skills from a
			newly enabled pack appear after the chat's next message.
		</p>
		{children}
	</ComposerPillPopover>
);
