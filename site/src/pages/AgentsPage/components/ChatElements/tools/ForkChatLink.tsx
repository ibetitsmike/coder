// This file is fork code: the chat link row of the fork chat tool cards.

import { ExternalLinkIcon } from "lucide-react";
import type React from "react";
import { Link, useLocation } from "react-router";
import { safeBuildAgentChatPath } from "../../../utils/navigation";

type ForkChatLinkProps = {
	chatId: string;
	children: React.ReactNode;
};

/** Links to a chat, keeping the current query string, like the list_agents card. */
export const ForkChatLink: React.FC<ForkChatLinkProps> = ({
	chatId,
	children,
}) => {
	const location = useLocation();
	const chatPath = safeBuildAgentChatPath({ chatId });
	if (chatPath === null) {
		return <div className="text-[13px] text-content-secondary">{children}</div>;
	}
	return (
		<Link
			to={{ pathname: chatPath, search: location.search }}
			className="flex w-fit items-center gap-1.5 text-[13px] text-content-secondary opacity-50 transition-opacity hover:opacity-100"
		>
			{children}
			<ExternalLinkIcon className="size-3 shrink-0" />
		</Link>
	);
};
