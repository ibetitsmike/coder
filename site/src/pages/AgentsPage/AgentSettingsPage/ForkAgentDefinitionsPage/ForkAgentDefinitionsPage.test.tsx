import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import { userAgentDefinitionsKey } from "#/api/queries/forkAgentDefinitions";
import type {
	Experiment,
	ForkAgentDefinition,
	Organization,
} from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
	mockApiError,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import ForkAgentDefinitionsPage from "./ForkAgentDefinitionsPage";

const renderPage = (experiments: Experiment[] = ["fork-agent-definitions"]) => {
	vi.spyOn(API, "getExperiments").mockResolvedValue(experiments);
	vi.spyOn(API, "getAuthenticatedUser").mockResolvedValue({
		...MockUserMember,
		organization_ids: [MockOrganization2.id, MockDefaultOrganization.id],
	});
	vi.spyOn(API, "getOrganizations").mockResolvedValue([
		MockOrganization2,
		MockDefaultOrganization,
	]);
	const getDefinitions = vi
		.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinitions")
		.mockResolvedValue([MockForkAgentDefinitionMetadata]);
	const { queryClient, router } = renderWithAuth(<ForkAgentDefinitionsPage />, {
		path: "/agents/settings/agent-definitions",
		route: "/agents/settings/agent-definitions",
		extraRoutes: [{ path: "/agents/settings", element: <div /> }],
	});
	return { getDefinitions, queryClient, router };
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkAgentDefinitionsPage", () => {
	it("redirects to the settings index without listing when the experiment is off", async () => {
		const { getDefinitions, router } = renderPage([]);

		await waitFor(() =>
			expect(router.state.location.pathname).toBe("/agents/settings"),
		);
		expect(getDefinitions).not.toHaveBeenCalled();
	});

	it("lists the default organization first and follows the picker", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();

		await waitFor(() =>
			expect(getDefinitions).toHaveBeenCalledWith(MockDefaultOrganization.id),
		);
		expect(getDefinitions).not.toHaveBeenCalledWith(MockOrganization2.id);

		await user.click(
			await screen.findByRole("combobox", {
				name: `Organization ${MockDefaultOrganization.display_name}`,
			}),
		);
		await user.click(
			screen.getByRole("option", {
				name: new RegExp(MockOrganization2.display_name),
			}),
		);
		await waitFor(() =>
			expect(getDefinitions).toHaveBeenLastCalledWith(MockOrganization2.id),
		);
	});

	it("disables a definition in the selected organization", async () => {
		const user = userEvent.setup();
		renderPage();
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockResolvedValue({
				...MockForkAgentDefinitionMetadata,
				enabled: false,
				content: "",
			});

		await user.click(
			await screen.findByRole("switch", {
				name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);

		await waitFor(() =>
			expect(update).toHaveBeenCalledWith(
				MockDefaultOrganization.id,
				MockForkAgentDefinitionMetadata.name,
				{ enabled: false },
			),
		);
	});

	it("keeps an accepted toggle when the list refetch fails", async () => {
		const user = userEvent.setup();
		const { getDefinitions, queryClient } = renderPage();
		const key = userAgentDefinitionsKey(MockDefaultOrganization.id);
		const updated = { ...MockForkAgentDefinitionMetadata, enabled: false };
		vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		).mockResolvedValue({ ...updated, content: "" });

		const toggle = await screen.findByRole("switch", {
			name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
		});
		getDefinitions.mockRejectedValue(mockApiError({ message: "Offline." }));
		await user.click(toggle);

		await waitFor(() =>
			expect(queryClient.getQueryState(key)?.status).toBe("error"),
		);
		expect(queryClient.getQueryData(key)).toEqual([updated]);
	});

	it("reports a failed toggle with the server message", async () => {
		const user = userEvent.setup();
		renderPage();
		const toastError = vi.spyOn(toast, "error");
		vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		).mockRejectedValue(
			mockApiError({ message: "Permission denied.", detail: "read-only" }),
		);

		await user.click(
			await screen.findByRole("switch", {
				name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);

		await waitFor(() =>
			expect(toastError).toHaveBeenCalledWith("Permission denied.", {
				description: "read-only",
			}),
		);
	});

	it("deletes a definition only after confirmation", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		const toastSuccess = vi.spyOn(toast, "success");
		const remove = vi
			.spyOn(forkAgentDefinitionsApi, "deleteUserAgentDefinition")
			.mockResolvedValue();

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
		expect(remove).not.toHaveBeenCalled();
		getDefinitions.mockResolvedValue([]);
		await user.click(await screen.findByRole("button", { name: "Delete" }));

		await waitFor(() =>
			expect(remove).toHaveBeenCalledWith(
				MockDefaultOrganization.id,
				MockForkAgentDefinitionMetadata.name,
			),
		);
		await waitFor(() =>
			expect(toastSuccess).toHaveBeenCalledWith("Agent definition deleted."),
		);
		// The deleted row took its menu button, so focus moves to New.
		await waitFor(() =>
			expect(
				screen.getByRole("button", { name: "New agent definition" }),
			).toHaveFocus(),
		);
	});

	it("drops a deleted definition when the list refetch fails", async () => {
		const user = userEvent.setup();
		const { getDefinitions, queryClient } = renderPage();
		const key = userAgentDefinitionsKey(MockDefaultOrganization.id);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"deleteUserAgentDefinition",
		).mockResolvedValue();

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
		getDefinitions.mockRejectedValue(mockApiError({ message: "Offline." }));
		await user.click(await screen.findByRole("button", { name: "Delete" }));

		await waitFor(() =>
			expect(queryClient.getQueryState(key)?.status).toBe("error"),
		);
		expect(queryClient.getQueryData(key)).toEqual([]);
	});

	it("treats a failed delete as done when the refetched list no longer has it", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		const toastSuccess = vi.spyOn(toast, "success");
		vi.spyOn(
			forkAgentDefinitionsApi,
			"deleteUserAgentDefinition",
		).mockRejectedValue(
			mockApiError({ message: "Agent definition not found." }),
		);

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
		getDefinitions.mockResolvedValue([]);
		await user.click(await screen.findByRole("button", { name: "Delete" }));

		await waitFor(() =>
			expect(toastSuccess).toHaveBeenCalledWith("Agent definition deleted."),
		);
	});

	it("creates a definition from the form in the selected organization", async () => {
		const user = userEvent.setup();
		renderPage();
		const toastSuccess = vi.spyOn(toast, "success");
		const create = vi
			.spyOn(forkAgentDefinitionsApi, "createUserAgentDefinition")
			.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });

		await user.click(
			await screen.findByRole("button", { name: "New agent definition" }),
		);
		const dialog = await screen.findByRole("dialog");
		await user.type(within(dialog).getByLabelText("Name"), "code-reviewer");
		await user.type(
			within(dialog).getByLabelText("Description"),
			"Reviews diffs.",
		);
		await user.type(within(dialog).getByLabelText("Model"), "gpt-5");
		await user.type(within(dialog).getByLabelText("Body"), "Review the diff.");
		await user.click(
			within(dialog).getByRole("button", { name: "Create agent definition" }),
		);

		await waitFor(() =>
			expect(create).toHaveBeenCalledWith(MockDefaultOrganization.id, {
				content:
					"---\nname: code-reviewer\ndescription: Reviews diffs.\nmodel: gpt-5\n---\nReview the diff.\n",
			}),
		);
		await waitFor(() =>
			expect(toastSuccess).toHaveBeenCalledWith("Agent definition created."),
		);
	});

	it("keeps one row for a created definition a refetch already listed", async () => {
		const user = userEvent.setup();
		const { getDefinitions, queryClient } = renderPage();
		const key = userAgentDefinitionsKey(MockDefaultOrganization.id);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		).mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });

		await user.click(
			await screen.findByRole("button", { name: "New agent definition" }),
		);
		const dialog = await screen.findByRole("dialog");
		await user.type(
			within(dialog).getByLabelText("Name"),
			MockForkAgentDefinitionMetadata.name,
		);
		await user.type(within(dialog).getByLabelText("Body"), "Review the diff.");
		getDefinitions.mockRejectedValue(mockApiError({ message: "Offline." }));
		await user.click(
			within(dialog).getByRole("button", { name: "Create agent definition" }),
		);

		await waitFor(() =>
			expect(queryClient.getQueryState(key)?.status).toBe("error"),
		);
		expect(queryClient.getQueryData(key)).toEqual([
			MockForkAgentDefinitionMetadata,
		]);
	});

	it("creates a definition from an imported file with all its front-matter", async () => {
		const user = userEvent.setup();
		renderPage();
		const create = vi
			.spyOn(forkAgentDefinitionsApi, "createUserAgentDefinition")
			.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });

		await user.click(
			await screen.findByRole("button", { name: "New agent definition" }),
		);
		const dialog = await screen.findByRole("dialog");
		await user.upload(
			within(dialog).getByLabelText("Upload agent definition file"),
			new File(
				["---\nmodel: sonnet\ntools: Read, Grep\n---\nWrite release notes.\n"],
				"release-notes.md",
			),
		);
		await waitFor(() =>
			expect(within(dialog).getByLabelText("Name")).toHaveValue(
				"release-notes",
			),
		);
		await user.click(
			within(dialog).getByRole("button", { name: "Create agent definition" }),
		);

		await waitFor(() =>
			expect(create).toHaveBeenCalledWith(MockDefaultOrganization.id, {
				content:
					"---\nname: release-notes\nmodel: sonnet\ntools: Read, Grep\n---\nWrite release notes.\n",
			}),
		);
	});

	it("keeps the editor and its input through Escape so a rejected create can be resubmitted", async () => {
		const user = userEvent.setup();
		renderPage();
		let rejectCreate: (error: unknown) => void = () => {};
		const create = vi
			.spyOn(forkAgentDefinitionsApi, "createUserAgentDefinition")
			.mockImplementationOnce(
				() =>
					new Promise((_resolve, reject) => {
						rejectCreate = reject;
					}),
			)
			.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });

		await user.click(
			await screen.findByRole("button", { name: "New agent definition" }),
		);
		const dialog = await screen.findByRole("dialog");
		await user.type(within(dialog).getByLabelText("Name"), "code-reviewer");
		await user.type(within(dialog).getByLabelText("Body"), "Review the diff.");
		await user.click(
			within(dialog).getByRole("button", { name: "Create agent definition" }),
		);
		await user.keyboard("{Escape}");
		rejectCreate(
			mockApiError({
				message: "Invalid agent definition content.",
				detail: 'front-matter field "readonly" must be a boolean',
			}),
		);

		const submit = await screen.findByRole("button", {
			name: "Create agent definition",
		});
		await waitFor(() => expect(submit).toBeEnabled());
		await user.click(submit);

		await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
		expect(create).toHaveBeenLastCalledWith(MockDefaultOrganization.id, {
			content: "---\nname: code-reviewer\n---\nReview the diff.\n",
		});
	});

	it("treats a failed create as done when the refetched list newly has it", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		const toastSuccess = vi.spyOn(toast, "success");
		vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		).mockRejectedValue(mockApiError({ message: "Upstream timeout." }));

		await user.click(
			await screen.findByRole("button", { name: "New agent definition" }),
		);
		const dialog = await screen.findByRole("dialog");
		await user.type(within(dialog).getByLabelText("Name"), "release-notes");
		await user.type(
			within(dialog).getByLabelText("Body"),
			"Write release notes.",
		);
		getDefinitions.mockResolvedValue([
			MockForkAgentDefinitionMetadata,
			{
				...MockForkAgentDefinitionMetadata,
				id: "agent-definition-release-notes",
				name: "release-notes",
			},
		]);
		await user.click(
			within(dialog).getByRole("button", { name: "Create agent definition" }),
		);

		await waitFor(() =>
			expect(toastSuccess).toHaveBeenCalledWith("Agent definition created."),
		);
	});

	it("keeps a rejected create failed when the name was already listed", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		const toastSuccess = vi.spyOn(toast, "success");
		vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		).mockRejectedValue(
			mockApiError({ message: "An agent definition with this name exists." }),
		);

		await user.click(
			await screen.findByRole("button", { name: "New agent definition" }),
		);
		const dialog = await screen.findByRole("dialog");
		await user.type(
			within(dialog).getByLabelText("Name"),
			MockForkAgentDefinitionMetadata.name,
		);
		await user.type(within(dialog).getByLabelText("Body"), "Review the diff.");
		await user.click(
			within(dialog).getByRole("button", { name: "Create agent definition" }),
		);

		await waitFor(() => expect(getDefinitions).toHaveBeenCalledTimes(2));
		await waitFor(() =>
			expect(
				within(dialog).getByRole("button", { name: "Create agent definition" }),
			).toBeEnabled(),
		);
		expect(toastSuccess).not.toHaveBeenCalled();
	});

	it("edits a reopened definition from cached data when its refetch fails", async () => {
		const user = userEvent.setup();
		const { queryClient } = renderPage();
		// The test client drops unobserved queries at once; the app keeps them.
		queryClient.setQueryDefaults(
			userAgentDefinitionsKey(MockDefaultOrganization.id),
			{ gcTime: Number.POSITIVE_INFINITY },
		);
		const getDefinition = vi
			.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinition")
			.mockResolvedValueOnce({
				...MockForkAgentDefinitionMetadata,
				content:
					"---\nname: code-reviewer\ndescription: Reviews diffs.\n---\nReview the diff.\n",
			})
			.mockRejectedValue(mockApiError({ message: "Upstream timeout." }));
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });
		const openEditor = async () => {
			await user.click(
				await screen.findByRole("button", {
					name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
				}),
			);
			await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
		};

		await openEditor();
		await screen.findByLabelText("Description");
		await user.keyboard("{Escape}");
		await openEditor();
		await waitFor(() => expect(getDefinition).toHaveBeenCalledTimes(2));
		const description = await screen.findByLabelText("Description");
		await user.clear(description);
		await user.type(description, "Reviews diffs for bugs.");
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);

		await waitFor(() =>
			expect(update).toHaveBeenCalledWith(
				MockDefaultOrganization.id,
				MockForkAgentDefinitionMetadata.name,
				{
					content:
						"---\nname: code-reviewer\ndescription: Reviews diffs for bugs.\n---\nReview the diff.\n",
				},
			),
		);
	});

	it("reopens a saved definition with its saved content when the refetch fails", async () => {
		const user = userEvent.setup();
		const { getDefinitions, queryClient } = renderPage();
		queryClient.setQueryDefaults(
			userAgentDefinitionsKey(MockDefaultOrganization.id),
			{ gcTime: Number.POSITIVE_INFINITY },
		);
		const content = (description: string) =>
			`---\nname: code-reviewer\ndescription: ${description}\n---\nReview the diff.\n`;
		const saved = {
			...MockForkAgentDefinitionMetadata,
			updated_at: "2030-01-01T00:00:00Z",
		};
		vi.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinition")
			.mockResolvedValueOnce({
				...MockForkAgentDefinitionMetadata,
				content: content("Reviews diffs."),
			})
			.mockRejectedValue(mockApiError({ message: "Upstream timeout." }));
		vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		).mockResolvedValue({
			...saved,
			content: content("Reviews diffs for bugs."),
		});
		const toastSuccess = vi.spyOn(toast, "success");
		const openEditor = async () => {
			await user.click(
				await screen.findByRole("button", {
					name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
				}),
			);
			await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
		};

		await openEditor();
		const description = await screen.findByLabelText("Description");
		await user.clear(description);
		await user.type(description, "Reviews diffs for bugs.");
		getDefinitions.mockResolvedValue([saved]);
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);
		await waitFor(() =>
			expect(toastSuccess).toHaveBeenCalledWith("Agent definition saved."),
		);
		await openEditor();

		expect(await screen.findByLabelText("Description")).toHaveValue(
			"Reviews diffs for bugs.",
		);
	});

	it.each([
		{
			change: "recreated",
			current: { id: "agent-definition-recreated" },
		},
		{
			change: "edited elsewhere",
			current: { updated_at: "2030-01-01T00:00:00Z" },
		},
	])(
		"waits for a definition $change instead of opening its old cached content",
		async ({ current }) => {
			const user = userEvent.setup();
			const { getDefinitions, queryClient } = renderPage();
			const listKey = userAgentDefinitionsKey(MockDefaultOrganization.id);
			queryClient.setQueryDefaults(listKey, {
				gcTime: Number.POSITIVE_INFINITY,
			});
			const listed = { ...MockForkAgentDefinitionMetadata, ...current };
			vi.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinition")
				.mockResolvedValueOnce({
					...MockForkAgentDefinitionMetadata,
					content:
						"---\nname: code-reviewer\ndescription: Old.\n---\nOld body.\n",
				})
				.mockResolvedValue({
					...listed,
					content:
						"---\nname: code-reviewer\ndescription: Current.\n---\nNew body.\n",
				});
			const openEditor = async () => {
				await user.click(
					await screen.findByRole("button", {
						name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
					}),
				);
				await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
			};

			await openEditor();
			await screen.findByLabelText("Description");
			getDefinitions.mockResolvedValue([listed]);
			await queryClient.refetchQueries({ queryKey: listKey, exact: true });
			await user.keyboard("{Escape}");
			await openEditor();

			expect(await screen.findByLabelText("Description")).toHaveValue(
				"Current.",
			);
		},
	);

	it("keeps the tool list of an edited definition", async () => {
		const user = userEvent.setup();
		renderPage();
		const toastSuccess = vi.spyOn(toast, "success");
		const getDefinition = vi
			.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinition")
			.mockResolvedValue({
				...MockForkAgentDefinitionMetadata,
				content:
					"---\nname: code-reviewer\ndescription: Reviews diffs.\ntools:\n  - Read\n  - Grep\n---\nReview the diff.\n",
			});
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
		const description = await screen.findByLabelText("Description");
		expect(getDefinition).toHaveBeenCalledWith(
			MockDefaultOrganization.id,
			MockForkAgentDefinitionMetadata.name,
		);
		await user.clear(description);
		await user.type(description, "Reviews diffs for bugs.");
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);

		await waitFor(() =>
			expect(update).toHaveBeenCalledWith(
				MockDefaultOrganization.id,
				MockForkAgentDefinitionMetadata.name,
				{
					content:
						"---\nname: code-reviewer\ndescription: Reviews diffs for bugs.\ntools:\n  - Read\n  - Grep\n---\nReview the diff.\n",
				},
			),
		);
		await waitFor(() =>
			expect(toastSuccess).toHaveBeenCalledWith("Agent definition saved."),
		);
	});

	it("retries a failed load and keeps edits when a failed save refetches", async () => {
		const user = userEvent.setup();
		renderPage();
		const content = (description: string) =>
			`---\nname: code-reviewer\ndescription: ${description}\n---\nReview the diff.\n`;
		const getDefinition = vi
			.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinition")
			.mockRejectedValueOnce(mockApiError({ message: "Upstream timeout." }))
			.mockResolvedValueOnce({
				...MockForkAgentDefinitionMetadata,
				content: content("Reviews diffs."),
			})
			.mockResolvedValue({
				...MockForkAgentDefinitionMetadata,
				content: content("Changed in another tab."),
			});
		vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		).mockRejectedValue(mockApiError({ message: "Permission denied." }));

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
		await user.click(await screen.findByRole("button", { name: "Retry" }));
		const description = await screen.findByLabelText("Description");
		await user.clear(description);
		await user.type(description, "Reviews diffs for bugs.");
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);

		await waitFor(() => expect(getDefinition).toHaveBeenCalledTimes(3));
		expect(screen.getByLabelText("Description")).toHaveValue(
			"Reviews diffs for bugs.",
		);
	});

	it("keeps the delete dialog open through Escape so a failed delete can be retried", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		let rejectDelete: (error: unknown) => void = () => {};
		const remove = vi
			.spyOn(forkAgentDefinitionsApi, "deleteUserAgentDefinition")
			.mockImplementationOnce(
				() =>
					new Promise((_resolve, reject) => {
						rejectDelete = reject;
					}),
			)
			.mockResolvedValue();

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
		await user.click(await screen.findByRole("button", { name: "Delete" }));
		await user.keyboard("{Escape}");
		rejectDelete(mockApiError({ message: "Upstream timeout." }));
		// A failed delete may still have removed the definition.
		await waitFor(() => expect(getDefinitions).toHaveBeenCalledTimes(2));
		const retry = await screen.findByRole("button", { name: "Delete" });
		await waitFor(() => expect(retry).toBeEnabled());
		await user.click(retry);

		await waitFor(() => expect(remove).toHaveBeenCalledTimes(2));
	});

	it("keeps each pending toggle on its own row", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		const other = {
			...MockForkAgentDefinitionMetadata,
			id: "agent-definition-release-notes",
			name: "release-notes",
		};
		getDefinitions.mockResolvedValue([MockForkAgentDefinitionMetadata, other]);
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockImplementation(() => new Promise(() => {}));
		const firstSwitch = await screen.findByRole("switch", {
			name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
		});

		await user.click(firstSwitch);
		await user.click(
			await screen.findByRole("switch", { name: `Enable ${other.name}` }),
		);
		await user.click(firstSwitch);

		await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
		expect(update.mock.calls.map(([, name]) => name)).toEqual([
			MockForkAgentDefinitionMetadata.name,
			other.name,
		]);
	});

	it("keeps a pending toggle in its own organization", async () => {
		const user = userEvent.setup();
		renderPage();
		let resolveFirst: (definition: ForkAgentDefinition) => void = () => {};
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockImplementationOnce(
				() =>
					new Promise((resolve) => {
						resolveFirst = resolve;
					}),
			)
			.mockImplementation(() => new Promise(() => {}));
		const switchName = `Enable ${MockForkAgentDefinitionMetadata.name}`;
		const selectOrganization = async (from: Organization, to: Organization) => {
			await user.click(
				await screen.findByRole("combobox", {
					name: `Organization ${from.display_name}`,
				}),
			);
			// Option names start with the avatar's initials.
			await user.click(
				screen.getByRole("option", { name: new RegExp(`${to.display_name}$`) }),
			);
			await screen.findByRole("combobox", {
				name: `Organization ${to.display_name}`,
			});
		};

		await user.click(await screen.findByRole("switch", { name: switchName }));
		await selectOrganization(MockDefaultOrganization, MockOrganization2);
		await user.click(await screen.findByRole("switch", { name: switchName }));
		await waitFor(() =>
			expect(update).toHaveBeenLastCalledWith(
				MockOrganization2.id,
				MockForkAgentDefinitionMetadata.name,
				{ enabled: false },
			),
		);

		// The first request settles after the switch and must release its
		// own organization's row, not the other organization's.
		resolveFirst({
			...MockForkAgentDefinitionMetadata,
			enabled: false,
			content: "",
		});
		await selectOrganization(MockOrganization2, MockDefaultOrganization);
		const firstSwitch = await screen.findByRole("switch", { name: switchName });
		await waitFor(() => expect(firstSwitch).toBeEnabled());
		await user.click(firstSwitch);

		await waitFor(() => expect(update).toHaveBeenCalledTimes(3));
		expect(update).toHaveBeenLastCalledWith(
			MockDefaultOrganization.id,
			MockForkAgentDefinitionMetadata.name,
			{ enabled: false },
		);
	});
});
