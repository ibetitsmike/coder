// This file is fork code: settings nav items for fork experiments, so
// SettingsPanel.tsx, an upstream file, needs one line.

import { BotIcon, WorkflowIcon } from "lucide-react";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { SettingsNavItem } from "./SettingsNavItem";

type ForkSettingsNavItemsProps = {
	readonly settingsSection: string | undefined;
	readonly state: unknown;
};

export const ForkSettingsNavItems: React.FC<ForkSettingsNavItemsProps> = ({
	settingsSection,
	state,
}) => {
	const { experiments } = useDashboard();
	if (!experiments.includes("fork-agent-definitions")) {
		return null;
	}
	return (
		<>
			<SettingsNavItem
				icon={WorkflowIcon}
				label="Agent definitions"
				active={settingsSection === "agent-definitions"}
				to="/agents/settings/agent-definitions"
				state={state}
			/>
			<SettingsNavItem
				icon={BotIcon}
				label="Available agents"
				active={settingsSection === "available-agents"}
				to="/agents/settings/available-agents"
				state={state}
			/>
		</>
	);
};
