import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { render } from "#/testHelpers/renderHelpers";
import { ForkAgentDefinitionsTableView } from "./ForkAgentDefinitionsTableView";

it("starts a new definition from the empty state", async () => {
	const user = userEvent.setup();
	const onCreate = vi.fn();
	render(
		<ForkAgentDefinitionsTableView
			definitions={[]}
			description="Custom agent types."
			emptyMessage="No personal agent definitions in this organization yet."
			error={undefined}
			isLoading={false}
			isRetrying={false}
			onRetry={vi.fn()}
			onToggleEnabled={vi.fn()}
			onCreate={onCreate}
			onEdit={vi.fn()}
			onDelete={vi.fn()}
		/>,
	);

	const [, emptyStateButton] = screen.getAllByRole("button", {
		name: "New agent definition",
	});
	await user.click(emptyStateButton);

	expect(onCreate).toHaveBeenCalledOnce();
});
