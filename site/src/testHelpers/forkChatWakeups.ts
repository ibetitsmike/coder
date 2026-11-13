import type { ChatWakeup } from "#/api/typesGenerated";
import { MockChat } from "./chatEntities";

export const MockChatWakeupInterval: ChatWakeup = {
	id: "11111111-1111-4111-8111-111111111111",
	chat_id: MockChat.id,
	organization_id: MockChat.organization_id,
	created_by: MockChat.owner_id,
	source: "schedule_wakeup",
	kind: "interval",
	prompt: "Check the deploy job and post the result to the chat.",
	interval_seconds: 300,
	timezone: "UTC",
	events: [],
	status: "active",
	next_run_at: "2099-01-01T09:00:00Z",
	run_count: 2,
	created_at: "2026-11-10T08:00:00Z",
	updated_at: "2026-11-10T08:05:00Z",
};

export const MockChatWakeupCron: ChatWakeup = {
	id: "22222222-2222-4222-8222-222222222222",
	chat_id: MockChat.id,
	organization_id: MockChat.organization_id,
	created_by: MockChat.owner_id,
	source: "/loop",
	kind: "cron",
	prompt: "Summarize the open pull requests.",
	cron: "0 9 * * *",
	timezone: "America/New_York",
	events: [],
	status: "active",
	next_run_at: "2099-01-02T14:00:00Z",
	max_runs: 10,
	run_count: 1,
	created_at: "2026-11-10T08:00:00Z",
	updated_at: "2026-11-10T08:00:00Z",
};

export const MockChatWakeupEvents: ChatWakeup = {
	id: "33333333-3333-4333-8333-333333333333",
	chat_id: MockChat.id,
	organization_id: MockChat.organization_id,
	created_by: MockChat.owner_id,
	source: "wait_for_events",
	kind: "events",
	prompt: "React to the review feedback when it arrives.",
	timezone: "UTC",
	events: [
		{ type: "child_done", chat_id: "12345678-abcd-4123-8123-123456789abc" },
		{
			type: "pull_request",
			url: "https://github.com/example/repo/pull/1",
			on: ["review", "checks"],
		},
		{
			type: "process_exit",
			process_id: "55dc01f8-7543-59a4-ae4f-eb42eeebb656",
			command: "make test 2>&1 | tee test.log",
		},
	],
	status: "active",
	next_run_at: "2099-01-01T09:01:00Z",
	run_count: 0,
	created_at: "2026-11-10T08:00:00Z",
	updated_at: "2026-11-10T08:00:00Z",
};

export const MockChatWakeupDelay: ChatWakeup = {
	id: "66666666-6666-4666-8666-666666666666",
	chat_id: MockChat.id,
	organization_id: MockChat.organization_id,
	created_by: MockChat.owner_id,
	source: "schedule_wakeup",
	kind: "delay",
	prompt: "Retry the flaky test once the runner is free.",
	timezone: "UTC",
	events: [],
	status: "active",
	next_run_at: "2099-01-01T10:00:00Z",
	max_runs: 1,
	run_count: 0,
	created_at: "2026-11-10T08:00:00Z",
	updated_at: "2026-11-10T08:00:00Z",
};

export const MockChatWakeupPaused: ChatWakeup = {
	id: "44444444-4444-4444-8444-444444444444",
	chat_id: MockChat.id,
	organization_id: MockChat.organization_id,
	created_by: MockChat.owner_id,
	source: "schedule_wakeup",
	kind: "interval",
	prompt: "Watch the release branch for failures.",
	interval_seconds: 900,
	timezone: "UTC",
	events: [],
	status: "paused",
	status_reason: "daily_run_cap",
	run_count: 1,
	created_at: "2026-11-10T08:00:00Z",
	updated_at: "2026-11-10T08:10:00Z",
};

export const MockChatWakeupCompleted: ChatWakeup = {
	id: "55555555-5555-4555-8555-555555555555",
	chat_id: MockChat.id,
	organization_id: MockChat.organization_id,
	created_by: MockChat.owner_id,
	source: "schedule_wakeup",
	kind: "interval",
	prompt: "Check whether the nightly build finished.",
	interval_seconds: 600,
	timezone: "UTC",
	events: [],
	status: "completed",
	status_reason: "Reached 5 of 5 runs",
	next_run_at: "2026-11-10T07:00:00Z",
	max_runs: 5,
	run_count: 5,
	last_run_at: "2026-11-10T07:00:00Z",
	created_at: "2026-11-10T06:00:00Z",
	updated_at: "2026-11-10T07:00:00Z",
};

export const MockScheduleWakeupArgs = {
	prompt: MockChatWakeupInterval.prompt,
	interval_seconds: 300,
	max_runs: 10,
};

export const MockScheduleWakeupResult = {
	id: MockChatWakeupInterval.id,
	kind: "interval",
	next_run_at: "2099-01-01T09:00:00Z",
	max_runs: 10,
};

export const MockWaitForEventsResult = {
	id: MockChatWakeupEvents.id,
	expires_at: "2099-01-02T08:00:00Z",
	children: [
		{
			chat_id: "12345678-abcd-4123-8123-123456789abc",
			title: "Fix the flaky test",
			status: "running",
		},
		{
			chat_id: "23456789-bcde-4234-8234-23456789abcd",
			title: "Update the docs",
			status: "waiting",
		},
	],
	pull_request: {
		url: "https://github.com/example/repo/pull/1",
		on: ["review", "comment", "checks", "closed"],
		head_sha: "0123456789abcdef0123456789abcdef01234567",
		checks_state: "pending",
	},
};

export const MockWaitForEventsProcessesResult = {
	id: MockChatWakeupEvents.id,
	expires_at: "2099-01-02T08:00:00Z",
	children: [],
	processes: [
		{
			process_id: "55dc01f8-7543-59a4-ae4f-eb42eeebb656",
			command: "make test 2>&1 | tee test.log",
			output_pattern: "^(FAIL|panic:)",
		},
		{
			process_id: "5ff79573-6d5c-58c8-945e-d48524770e4f",
			command: "./scripts/deploy.sh staging",
		},
	],
};

export const MockNotifyUserArgs = {
	title: "Deploy finished",
	message: "The staging deploy passed all checks.",
};
