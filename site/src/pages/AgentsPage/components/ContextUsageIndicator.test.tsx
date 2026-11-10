import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
	MockChatContextAgentDefinitions,
	MockChatContextAgentDefinitionWarnings,
	MockChatContextDirty,
} from "#/testHelpers/chatEntities";
import { ContextUsageIndicator } from "./ContextUsageIndicator";

describe("ContextUsageIndicator", () => {
	it("refreshes context after opening the popover with a click", async () => {
		const user = userEvent.setup();
		const onRefreshContext = vi.fn();
		render(
			<ContextUsageIndicator
				usage={{
					usedTokens: 12_000,
					contextLimitTokens: 200_000,
					context: MockChatContextDirty,
				}}
				onRefreshContext={onRefreshContext}
			/>,
		);
		await user.click(screen.getByRole("button", { name: /Context usage/ }));
		await user.click(screen.getByRole("button", { name: "Refresh context" }));
		expect(onRefreshContext).toHaveBeenCalledTimes(1);
	});

	it("shows agent definition warnings and errors", async () => {
		const user = userEvent.setup();
		render(
			<ContextUsageIndicator
				usage={{
					usedTokens: 12_000,
					contextLimitTokens: 200_000,
					context: MockChatContextAgentDefinitions,
				}}
			/>,
		);
		await user.click(screen.getByRole("button", { name: /Context usage/ }));
		const section = screen.getByRole("button", { name: /Agent definitions/ });
		expect(section).toHaveAccessibleName(/2 issues/);
		await user.click(section);
		expect(screen.getByText(/unknown front-matter field/)).toHaveTextContent(
			'missing description; unknown front-matter field "temperature" ignored',
		);
		expect(screen.getByText(/is not kebab-case/)).toHaveTextContent(
			'agent name "Broken" is not kebab-case',
		);
	});

	it("flags agent definition warnings on the trigger", () => {
		render(
			<ContextUsageIndicator
				usage={{
					usedTokens: 12_000,
					contextLimitTokens: 200_000,
					context: MockChatContextAgentDefinitionWarnings,
				}}
			/>,
		);
		expect(
			screen.getByRole("button", { name: /Context usage/ }),
		).toHaveAccessibleName(/Some agent definitions have warnings\./);
	});
});
