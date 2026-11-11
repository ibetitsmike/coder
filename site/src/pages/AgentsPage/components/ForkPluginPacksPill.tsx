// This file is fork code: the fork-plugin-packs experiment's composer pill.

import { PuzzleIcon } from "lucide-react";
import type React from "react";
import { useQuery } from "react-query";
import { chat } from "#/api/queries/chats";
import { enabledPluginPacks } from "#/api/queries/forkPluginPackEnablement";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { ComposerPillPopover } from "./ComposerPillPopover";
import { ForkPluginPackToggles } from "./ForkPluginPackToggles";

type ForkPluginPacksPillProps = {
	chatId: string;
};

/** Lets the chat owner turn plugin packs on or off for one chat. */
export const ForkPluginPacksPill: React.FC<ForkPluginPacksPillProps> = ({
	chatId,
}) => {
	const { user } = useAuthenticated();
	const chatQuery = useQuery(chat(chatId));
	const target = { kind: "chat", chatId } as const;
	const enabledQuery = useQuery(enabledPluginPacks(target));
	if (!chatQuery.data) {
		return null;
	}
	return (
		<ForkPluginPacksPillView enabledCount={enabledQuery.data?.length}>
			<ForkPluginPackToggles
				target={target}
				organizationId={chatQuery.data.organization_id}
				canEdit={chatQuery.data.owner_id === user.id}
			/>
		</ForkPluginPacksPillView>
	);
};

type ForkPluginPacksPillViewProps = {
	/** `undefined` while the chat's packs load or after loading fails. */
	enabledCount: number | undefined;
	children: React.ReactNode;
};

export const ForkPluginPacksPillView: React.FC<
	ForkPluginPacksPillViewProps
> = ({ enabledCount, children }) => (
	<ComposerPillPopover
		label={
			enabledCount === undefined
				? "Plugin packs"
				: `Plugin packs, ${enabledCount} enabled`
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
			Packs enabled on the project apply too.
		</p>
		{children}
	</ComposerPillPopover>
);
