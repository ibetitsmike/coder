// This file is fork code: the fork-pinned-skills experiment's composer pill.

import { CheckIcon, PinIcon } from "lucide-react";
import type React from "react";
import {
	type UseQueryResult,
	useMutation,
	useQuery,
	useQueryClient,
} from "react-query";
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
	MAX_PINNED_SKILLS_PER_CHAT,
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
import { canManageChat } from "./ChatActionsMenuItems";
import { EditableSkillChip, SkillChip } from "./ChatMessageInput/SkillChip";
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
		chatQuery.data && canManageChat(chatQuery.data, user.id)
			? chatQuery.data
			: undefined;

	return (
		<ForkPinnedSkillsPillView
			pins={pins}
			error={pinsQuery.error}
			isRetrying={pinsQuery.isFetching}
			onRetry={() => void pinsQuery.refetch()}
			canEdit={Boolean(editableChat)}
			isPending={updateMutation.isPending}
			onUnpin={(pin) => {
				if (!pins) {
					return;
				}
				updateMutation.mutate(pins.filter((p) => !isSamePin(p, pin)));
			}}
		>
			{editableChat && pins && (
				<PinnableSkillList
					chat={editableChat}
					workspaceSkills={workspaceSkills}
					pins={pins}
					disabled={updateMutation.isPending}
					onToggle={(skill, pinned) =>
						updateMutation.mutate(
							pinned
								? [...pins, skill]
								: pins.filter((p) => !isSamePin(p, skill)),
						)
					}
				/>
			)}
		</ForkPinnedSkillsPillView>
	);
};

type ForkPinnedSkillsPillViewProps = {
	/** `undefined` while the pins load or after loading fails. */
	pins: readonly ChatPinnedSkill[] | undefined;
	error: unknown;
	isRetrying: boolean;
	onRetry: () => void;
	canEdit: boolean;
	/** Each change replaces the whole list, so the next one waits for it. */
	isPending: boolean;
	onUnpin: (pin: ChatPinnedSkill) => void;
	/** The skill picker, shown below the pins. */
	children?: React.ReactNode;
};

/**
 * The composer pill for a chat's pinned skills. The popover lists the pins
 * as chips, removable when `canEdit`, above `children`.
 */
export const ForkPinnedSkillsPillView: React.FC<
	ForkPinnedSkillsPillViewProps
> = ({
	pins,
	error,
	isRetrying,
	onRetry,
	canEdit,
	isPending,
	onUnpin,
	children,
}) => (
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
			Pinned skills apply to every message in this chat until they are unpinned.
		</p>
		{Boolean(error) && (
			<ErrorAlert
				className={pins ? "mb-3" : undefined}
				error={error}
				actions={<RetryButton isRetrying={isRetrying} onRetry={onRetry} />}
			/>
		)}
		{pins ? (
			<>
				{pins.length === 0 ? (
					<p className="m-0 text-content-secondary">No skills are pinned.</p>
				) : (
					<ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
						{pins.map((pin) => (
							<li key={getSkillAlias(pin.source, pin.name)}>
								{canEdit ? (
									<EditableSkillChip
										source={pin.source}
										name={pin.name}
										data-selected={false}
										disabled={isPending}
										onRemove={() => onUnpin(pin)}
									/>
								) : (
									<SkillChip source={pin.source} name={pin.name} />
								)}
							</li>
						))}
					</ul>
				)}
				{children}
			</>
		) : (
			!error && <Loader />
		)}
	</ComposerPillPopover>
);

type SkillSourceQuery = Pick<
	UseQueryResult<unknown, unknown>,
	"error" | "isFetching" | "refetch"
>;

type PinnableSkillListProps = {
	chat: Chat;
	workspaceSkills: readonly SkillMetadata[] | undefined;
	pins: readonly ChatPinnedSkill[];
	disabled: boolean;
	onToggle: (skill: ChatPinnedSkill, pinned: boolean) => void;
};

/** Loads the skills the chat's turns can use and lists them for pinning. */
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
	// The chat page reads these keys with the same staleTime, so opening the
	// picker reuses its cache.
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
	const sources: {
		list: SkillSourceList<SkillMetadata>;
		queries: readonly SkillSourceQuery[];
	}[] = [
		{
			list: {
				source: "personal",
				skills: personalQuery.data?.filter((skill) => skill.enabled),
			},
			queries: [personalQuery],
		},
		{
			list: {
				source: "org",
				skills: activeOrganizationSkills(
					organizationSkillsQuery.data,
					organizationPreferencesQuery.data,
				),
			},
			queries: [organizationSkillsQuery, organizationPreferencesQuery],
		},
		{
			list: {
				source: "project",
				skills: hasProject
					? usableChatProjectSkills(
							chatProjectUsability(projectQuery, projectSkillsQuery.error),
							projectSkillsQuery.data?.filter((skill) => skill.enabled),
						)
					: [],
			},
			queries: hasProject ? [projectSkillsQuery, projectQuery] : [],
		},
		{ list: { source: "workspace", skills: workspaceSkills }, queries: [] },
	];
	const failedQueries = sources
		.filter(({ list }) => list.skills === undefined)
		.flatMap(({ queries }) => queries)
		.filter((query) => Boolean(query.error));

	return (
		<ForkPinnableSkillListView
			skillLists={sources.map(({ list }) => list)}
			pins={pins}
			disabled={disabled}
			error={failedQueries[0]?.error}
			isRetrying={failedQueries.some((query) => query.isFetching)}
			onRetry={() => {
				for (const query of failedQueries) {
					void query.refetch();
				}
			}}
			onToggle={onToggle}
		/>
	);
};

type ForkPinnableSkillListViewProps = {
	/** A list whose `skills` is `undefined` has not loaded. */
	skillLists: readonly SkillSourceList<SkillMetadata>[];
	pins: readonly ChatPinnedSkill[];
	disabled: boolean;
	/** The error of a source that has not loaded, if any. */
	error: unknown;
	isRetrying: boolean;
	onRetry: () => void;
	onToggle: (skill: ChatPinnedSkill, pinned: boolean) => void;
};

/**
 * A searchable list of skills, where selecting one pins or unpins it. Only
 * pinned skills stay selectable once the chat has the most pins it can hold.
 */
export const ForkPinnableSkillListView: React.FC<
	ForkPinnableSkillListViewProps
> = ({ skillLists, pins, disabled, error, isRetrying, onRetry, onToggle }) => {
	const isUnresolved = skillLists.some((list) => list.skills === undefined);
	const skillCount = skillLists.reduce(
		(count, list) => count + (list.skills?.length ?? 0),
		0,
	);
	const isFull = pins.length >= MAX_PINNED_SKILLS_PER_CHAT;

	return (
		<div className="mt-3 flex flex-col gap-2">
			{Boolean(error) && (
				<ErrorAlert
					error={error}
					actions={<RetryButton isRetrying={isRetrying} onRetry={onRetry} />}
				/>
			)}
			<Command className="border border-solid border-border">
				<CommandInput
					aria-label="Search skills to pin"
					placeholder="Search skills to pin"
				/>
				<CommandList className="max-h-56">
					{!isUnresolved && (
						<CommandEmpty>
							{skillCount === 0
								? "No skills available to pin."
								: "No skills found."}
						</CommandEmpty>
					)}
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
			{isUnresolved && !error && (
				<p className="m-0 text-content-secondary">Loading skills...</p>
			)}
			{isFull && (
				<p className="m-0 text-content-secondary">
					A chat can pin at most {MAX_PINNED_SKILLS_PER_CHAT} skills.
				</p>
			)}
		</div>
	);
};
