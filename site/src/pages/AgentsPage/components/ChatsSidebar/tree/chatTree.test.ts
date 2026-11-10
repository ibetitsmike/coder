import { describe, expect, it } from "vitest";
import { MockChat } from "#/testHelpers/chatEntities";
import { getArchiveBlockedReason } from "../../ChatActionsMenuItems";
import { buildChatTree, collectDescendantChats } from "./chatTree";

const mockRoot = { ...MockChat, id: "root-1", title: "Root" };
const mockChild = {
	...MockChat,
	id: "child-1",
	title: "Coordinator",
	parent_chat_id: "root-1",
	root_chat_id: "root-1",
};
const mockGrandchild = {
	...MockChat,
	id: "grandchild-1",
	title: "Worker",
	parent_chat_id: "child-1",
	root_chat_id: "root-1",
};

describe("buildChatTree", () => {
	it("links a three-level family embedded out of order", () => {
		// The list response embeds every descendant flat under the
		// root, and the grandchild can appear before its parent.
		const tree = buildChatTree([
			{ ...mockRoot, children: [mockGrandchild, mockChild] },
		]);

		expect(tree.rootIds).toEqual(["root-1"]);
		expect(tree.parentById.get("child-1")).toBe("root-1");
		expect(tree.parentById.get("grandchild-1")).toBe("child-1");
		expect(tree.childrenById.get("root-1")).toEqual(["child-1"]);
		expect(tree.childrenById.get("child-1")).toEqual(["grandchild-1"]);
		expect(tree.childrenById.get("grandchild-1")).toEqual([]);
	});

	it("keeps depth-1 input unchanged", () => {
		const tree = buildChatTree([{ ...mockRoot, children: [mockChild] }]);

		expect(tree.rootIds).toEqual(["root-1"]);
		expect(tree.parentById.get("child-1")).toBe("root-1");
		expect(tree.childrenById.get("root-1")).toEqual(["child-1"]);
		expect(tree.childrenById.get("child-1")).toEqual([]);
	});

	it("keeps a nested child list when a stale flat parent entry follows its root", () => {
		const tree = buildChatTree([
			{ ...mockRoot, children: [mockGrandchild, mockChild] },
			mockChild,
		]);

		expect(tree.rootIds).toEqual(["root-1"]);
		expect(tree.childrenById.get("child-1")).toEqual(["grandchild-1"]);
	});

	it("falls back to the root when an embedded child's parent is missing", () => {
		const staleChild = { ...mockChild, parent_chat_id: "missing-root" };
		const tree = buildChatTree([{ ...mockRoot, children: [staleChild] }]);

		expect(tree.rootIds).toEqual(["root-1"]);
		expect(tree.parentById.get("child-1")).toBe("root-1");
		expect(tree.childrenById.get("root-1")).toEqual(["child-1"]);
	});
});

describe("collectDescendantChats", () => {
	it("collects the descendants of a mid-level node for archive blocking", () => {
		const runningGrandchild = { ...mockGrandchild, status: "running" as const };
		const tree = buildChatTree([
			{ ...mockRoot, children: [mockChild, runningGrandchild] },
		]);

		expect(
			collectDescendantChats(tree, "root-1").map((chat) => chat.id),
		).toEqual(expect.arrayContaining(["child-1", "grandchild-1"]));
		expect(collectDescendantChats(tree, "root-1")).toHaveLength(2);
		expect(collectDescendantChats(tree, "child-1")).toEqual([
			runningGrandchild,
		]);
		expect(collectDescendantChats(tree, "grandchild-1")).toEqual([]);

		// The tree walk feeds the archive menu, so a waiting mockChild
		// whose grandchild is running cannot be archived.
		expect(
			getArchiveBlockedReason(
				"waiting",
				collectDescendantChats(tree, "child-1"),
			),
		).toBe("active");
	});
});
