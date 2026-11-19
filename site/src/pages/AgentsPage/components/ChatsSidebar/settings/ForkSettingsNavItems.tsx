// This file is fork code: settings nav items for fork experiments, so
// SettingsPanel.tsx, an upstream file, needs one line.

import { BotIcon, ScrollTextIcon, WorkflowIcon } from "lucide-react";
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
	const showAgentDefinitions = experiments.includes("fork-agent-definitions");
	const showWorkflows = experiments.includes("fork-workflows");
	return (
		<>
			{showAgentDefinitions && (
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
			)}
			{showWorkflows && (
				<SettingsNavItem
					icon={ScrollTextIcon}
					label="Workflows"
					active={settingsSection === "workflows"}
					to="/agents/settings/workflows"
					state={state}
				/>
			)}
		</>
	);
};
