import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
	MockAgentDefinitionResource,
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

	it("describes an agent definition when its row gets keyboard focus", async () => {
		const user = userEvent.setup();
		render(
			<ContextUsageIndicator
				usage={{
					usedTokens: 12_000,
					contextLimitTokens: 200_000,
					context: { dirty: false, resources: [MockAgentDefinitionResource] },
				}}
			/>,
		);
		await user.click(screen.getByRole("button", { name: /Context usage/ }));
		await user.click(screen.getByRole("button", { name: /Agent definitions/ }));
		const name = MockAgentDefinitionResource.subagent_name;
		for (
			let i = 0;
			i < 10 && document.activeElement?.textContent !== name;
			i++
		) {
			await user.tab();
		}

		expect(await screen.findByRole("tooltip")).toHaveTextContent(
			"Reviews diffs for correctness.",
		);
	});
});
