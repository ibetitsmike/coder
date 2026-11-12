// This file is fork code: the fork-pinned-skills experiment's composer pill.

import { CheckIcon, PinIcon } from "lucide-react";
import type React from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	chatProjectSkills,
	chatProjectUsability,
	usableChatProjectSkills,
} from "#/api/queries/chatProjectSkills";
import { chatProject } from "#/api/queries/chatProjects";
import { chat } from "#/api/queries/chats";
import {
	chatPinnedSkills,
	updateChatPinnedSkillsMutation,
} from "#/api/queries/forkChatPinnedSkills";
import {
	organizationSkillPreferences,
	organizationSkills,
} from "#/api/queries/organizationSkills";
import { userSkills } from "#/api/queries/userSkills";
import type { Chat, ChatPinnedSkill } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import {
	Command,
	CommandEmpty,
	CommandInput,
	CommandItem,
	CommandList,
} from "#/components/Command/Command";
import { Loader } from "#/components/Loader/Loader";
import { toast } from "#/components/Toaster/toast";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { getSkillAlias, type SkillSourceList } from "../utils/skillAliases";
import { activeOrganizationSkills } from "../utils/skills";
import { EditableSkillChip, SkillChip } from "./ChatMessageInput/SkillChip";
import { MAX_SKILL_INVOCATIONS_PER_MESSAGE } from "./ChatMessageInput/SkillInvocationNode";
import type { SkillMetadata } from "./ChatMessageInput/SkillsTriggerMenu";
import { ComposerPillPopover } from "./ComposerPillPopover";
import { RetryButton } from "./RetryButton";

const isSamePin = (a: ChatPinnedSkill, b: ChatPinnedSkill) =>
	a.source === b.source && a.name === b.name;

type ForkPinnedSkillsPillProps = {
	chatId: string;
	/** The chat's workspace skills, as the "/" menu lists them. */
	workspaceSkills: readonly SkillMetadata[] | undefined;
};

/** Lists the chat's pinned skills and lets the chat owner pin or unpin them. */
export const ForkPinnedSkillsPill: React.FC<ForkPinnedSkillsPillProps> = ({
	chatId,
	workspaceSkills,
}) => {
	const { user } = useAuthenticated();
	const queryClient = useQueryClient();
	const chatQuery = useQuery({ ...chat(chatId), refetchOnMount: false });
	const pinsQuery = useQuery(chatPinnedSkills(chatId));
	const updateMutation = useMutation({
		...updateChatPinnedSkillsMutation(queryClient, chatId),
		onError: (error) =>
			toast.error(getErrorMessage(error, "Failed to update pinned skills."), {
				description: getErrorDetail(error),
			}),
	});
	const pins = pinsQuery.data;
	const editableChat =
		chatQuery.data?.owner_id === user.id ? chatQuery.data : undefined;
	// Each request replaces the whole list, so a second one sent before the
	// first settles would drop the first change.
	const savePins = (next: readonly ChatPinnedSkill[]) => {
		if (!updateMutation.isPending) {
			updateMutation.mutate(next);
		}
	};

	return (
		<ComposerPillPopover
			label={pins ? `Pinned skills, ${pins.length} pinned` : "Pinned skills"}
			heading="Pinned skills"
			trigger={
				<>
					<PinIcon
						aria-hidden="true"
						className="size-3.5 shrink-0 text-content-secondary"
					/>
					{pins && pins.length > 0 && (
						<span className="text-content-secondary tabular-nums">
							{pins.length}
						</span>
					)}
				</>
			}
		>
			<p className="mb-3 mt-1 text-content-secondary">
				Pinned skills apply to every message in this chat until they are
				unpinned.
			</p>
			{pinsQuery.error ? (
				<ErrorAlert
					error={pinsQuery.error}
					actions={
						<RetryButton
							isRetrying={pinsQuery.isFetching}
							onRetry={() => void pinsQuery.refetch()}
						/>
					}
				/>
			) : !pins ? (
				<Loader />
			) : (
				<>
					{pins.length === 0 ? (
						<p className="m-0 text-content-secondary">No skills are pinned.</p>
					) : (
						<ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
							{pins.map((pin) => (
								<li key={getSkillAlias(pin.source, pin.name)}>
									{editableChat ? (
										<EditableSkillChip
											source={pin.source}
											name={pin.name}
											data-selected={false}
											onRemove={() =>
												savePins(pins.filter((p) => !isSamePin(p, pin)))
											}
										/>
									) : (
										<SkillChip source={pin.source} name={pin.name} />
									)}
								</li>
							))}
						</ul>
					)}
					{editableChat && (
						<PinnableSkillList
							chat={editableChat}
							workspaceSkills={workspaceSkills}
							pins={pins}
							disabled={updateMutation.isPending}
							onToggle={(skill, pinned) =>
								savePins(
									pinned
										? [...pins, skill]
										: pins.filter((p) => !isSamePin(p, skill)),
								)
							}
						/>
					)}
				</>
			)}
		</ComposerPillPopover>
	);
};

type PinnableSkillListProps = {
	chat: Chat;
	workspaceSkills: readonly SkillMetadata[] | undefined;
	pins: readonly ChatPinnedSkill[];
	disabled: boolean;
	onToggle: (skill: ChatPinnedSkill, pinned: boolean) => void;
};

/**
 * Lists the skills the chat's turns can load. The chat page already runs
 * these queries, so opening the list rarely fetches anything new.
 */
const PinnableSkillList: React.FC<PinnableSkillListProps> = ({
	chat,
	workspaceSkills,
	pins,
	disabled,
	onToggle,
}) => {
	const organizationId = chat.organization_id;
	const projectId = chat.project_id;
	const hasProject = Boolean(projectId);
	const personalQuery = useQuery({ ...userSkills(), staleTime: 60_000 });
	const organizationSkillsQuery = useQuery({
		...organizationSkills(organizationId),
		staleTime: 60_000,
	});
	const organizationPreferencesQuery = useQuery({
		...organizationSkillPreferences(organizationId),
		staleTime: 60_000,
	});
	const projectSkillsQuery = useQuery({
		...chatProjectSkills(organizationId, projectId ?? ""),
		enabled: hasProject,
		staleTime: 60_000,
	});
	const projectQuery = useQuery({
		...chatProject(projectId),
		enabled: hasProject,
	});
	const skillLists: SkillSourceList<SkillMetadata>[] = [
		{
			source: "personal",
			skills: personalQuery.data?.filter((skill) => skill.enabled),
		},
		{
			source: "org",
			skills: activeOrganizationSkills(
				organizationSkillsQuery.data,
				organizationPreferencesQuery.data,
			),
		},
		{
			source: "project",
			skills: hasProject
				? usableChatProjectSkills(
						chatProjectUsability(projectQuery, projectSkillsQuery.error),
						projectSkillsQuery.data?.filter((skill) => skill.enabled),
					)
				: [],
		},
		{ source: "workspace", skills: workspaceSkills },
	];
	const isUnresolved = skillLists.some((list) => list.skills === undefined);
	const isFetching =
		personalQuery.isFetching ||
		organizationSkillsQuery.isFetching ||
		organizationPreferencesQuery.isFetching ||
		projectSkillsQuery.isFetching ||
		projectQuery.isFetching;
	const isFull = pins.length >= MAX_SKILL_INVOCATIONS_PER_MESSAGE;

	return (
		<div className="mt-3 flex flex-col gap-2">
			<Command className="rounded-md border border-solid border-border">
				<CommandInput
					aria-label="Search skills to pin"
					placeholder="Search skills to pin"
				/>
				<CommandList className="max-h-56">
					{!isUnresolved && <CommandEmpty>No skills found.</CommandEmpty>}
					{skillLists.flatMap(({ source, skills }) =>
						(skills ?? []).map((skill) => {
							const alias = getSkillAlias(source, skill.name);
							const isPinned = pins.some((pin) =>
								isSamePin(pin, { source, name: skill.name }),
							);
							return (
								<CommandItem
									key={alias}
									value={alias}
									keywords={[skill.description]}
									disabled={disabled || (isFull && !isPinned)}
									onSelect={() =>
										onToggle({ source, name: skill.name }, !isPinned)
									}
								>
									<span className="min-w-0 flex-1 truncate font-mono text-xs">
										{alias}
									</span>
									{isPinned && (
										<>
											<CheckIcon aria-hidden="true" />
											<span className="sr-only">pinned</span>
										</>
									)}
								</CommandItem>
							);
						}),
					)}
				</CommandList>
			</Command>
			{isUnresolved && (
				<p className="m-0 text-content-secondary">
					{isFetching
						? "Loading skills..."
						: "Some skills could not be loaded."}
				</p>
			)}
			{isFull && (
				<p className="m-0 text-content-secondary">
					A chat can pin at most {MAX_SKILL_INVOCATIONS_PER_MESSAGE} skills.
				</p>
			)}
		</div>
	);
};
