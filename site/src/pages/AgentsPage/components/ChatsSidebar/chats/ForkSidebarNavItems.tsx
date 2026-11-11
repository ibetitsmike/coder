// This file is fork code: Agents sidebar nav items for fork experiments, so
// ChatsPanel.tsx, an upstream file, renders one element.

import { BotIcon } from "lucide-react";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { SettingsNavItem } from "../settings/SettingsNavItem";

type ForkSidebarNavItemsProps = {
	/** Passed as the link state so settings can return to this page. */
	readonly state: unknown;
};

export const ForkSidebarNavItems: React.FC<ForkSidebarNavItemsProps> = ({
	state,
}) => {
	const { experiments } = useDashboard();
	if (!experiments.includes("fork-agent-definitions")) {
		return null;
	}
	return (
		<SettingsNavItem
			icon={BotIcon}
			label="Agents"
			active={false}
			to="/agents/settings/agent-definitions"
			state={state}
		/>
	);
};
