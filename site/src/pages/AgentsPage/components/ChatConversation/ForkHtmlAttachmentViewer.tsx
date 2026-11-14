// This file is fork code: the sandboxed viewer of the fork-html-attachments
// experiment, and the bridge that turns a click in a page into a user message.

import { DownloadIcon } from "lucide-react";
import type React from "react";
import {
	createContext,
	useContext,
	useEffect,
	useId,
	useRef,
	useState,
} from "react";
import { useMutation, useQueryClient } from "react-query";
import { useParams } from "react-router";
import { getErrorMessage } from "#/api/errors";
import { createChatMessageByChatId } from "#/api/queries/chats";
import type * as TypesGen from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { getChatFileURL } from "../../utils/chatAttachments";
import { getAttachmentDownload } from "./AttachmentBlocks";
import { AttachmentDownloadLink } from "./AttachmentDownloadLink";
import { FileProbeProvider } from "./FileProbeContext";
import {
	forkHtmlMessageIntervalMs,
	formatForkHtmlMessage,
	getForkHtmlAttachmentURL,
	latestForkHtmlAttachmentIds,
	parseForkHtmlMessage,
} from "./forkHtmlAttachments";
import type { ParsedMessageEntry, RenderBlock } from "./types";

type HtmlFilePart = TypesGen.ChatFilePart & { file_id: string; name: string };

const LatestHtmlAttachmentsContext = createContext<
	ReadonlyMap<string, string> | undefined
>(undefined);

/**
 * Wraps the upstream FileProbeProvider so the timeline needs one hook line,
 * and tells each HTML attachment whether it is the newest agent page of its
 * name.
 */
export const ForkFileProbeProvider: React.FC<
	React.PropsWithChildren<{
		evictedFileIds: ReadonlySet<string>;
		entries: readonly Pick<ParsedMessageEntry, "message">[];
		liveBlocks?: readonly RenderBlock[];
	}>
> = ({ evictedFileIds, entries, liveBlocks, children }) => (
	<FileProbeProvider evictedFileIds={evictedFileIds}>
		<LatestHtmlAttachmentsContext.Provider
			value={latestForkHtmlAttachmentIds(entries, liveBlocks)}
		>
			{children}
		</LatestHtmlAttachmentsContext.Provider>
	</FileProbeProvider>
);

type ForkHtmlAttachmentPreviewProps = {
	block: HtmlFilePart;
	/**
	 * The upstream card, shown for a page that a newer one replaced and for
	 * an upload by the user.
	 */
	children: React.ReactNode;
};

/** Only the newest agent attachment of a name renders as a live page. */
export const ForkHtmlAttachmentPreview: React.FC<
	ForkHtmlAttachmentPreviewProps
> = ({ block, children }) => {
	const latestFileId = useContext(LatestHtmlAttachmentsContext)?.get(
		block.name,
	);
	if (latestFileId !== block.file_id) {
		return children;
	}
	return <ForkHtmlAttachmentViewer block={block} />;
};

/**
 * Renders the page in an iframe that may run scripts but has an opaque
 * origin, so it cannot read the Coder session, and whose server policy
 * blocks network access. The sandbox still lets a page navigate its own
 * frame, so the viewer closes the frame after its first navigation.
 *
 * A page proposes text for the chat with
 * `parent.postMessage({ type: "coder:chat-message", text }, "*")`. The viewer
 * takes only proposals from its own frame, right after a click in that frame
 * and no input in the dashboard, and at most one per
 * forkHtmlMessageIntervalMs. It shows the exact message and sends it only
 * when the user confirms outside the frame.
 */
const ForkHtmlAttachmentViewer: React.FC<{ block: HtmlFilePart }> = ({
	block,
}) => {
	const { agentId } = useParams();
	const queryClient = useQueryClient();
	const sendMessage = useMutation(createChatMessageByChatId(queryClient));
	const { reset: resetSend, isPending: sendPending } = sendMessage;
	const frameRef = useRef<HTMLIFrameElement>(null);
	const proposalRef = useRef<HTMLElement>(null);
	const sendRef = useRef<HTMLButtonElement>(null);
	const focusSend = useRef(false);
	const lastProposedAt = useRef(Number.NEGATIVE_INFINITY);
	const lastDashboardInputAt = useRef(Number.NEGATIVE_INFINITY);
	const [proposal, setProposal] = useState<string | null>(null);
	const headerId = useId();
	const proposalId = useId();
	const download = getAttachmentDownload(block, getChatFileURL(block.file_id));
	const frameLoads = useRef(0);
	const focusReopen = useRef(false);
	const focusFrame = useRef(false);
	const [navigatedAway, setNavigatedAway] = useState(false);
	const [frameKey, setFrameKey] = useState(0);

	// The page itself is the first load. Any later load is the page leaving
	// for another address, or reloading. A proposal still open may come from
	// that other address, so it goes too. Focus in the frame would fall to the
	// document body when the frame goes, so it moves to the reopen button,
	// and back to the frame when the page shows again. A pending send disables
	// the focused Send button, so focus on the body with a proposal open was
	// on the proposal too.
	const onFrameLoad = () => {
		frameLoads.current += 1;
		if (frameLoads.current > 1) {
			const active = document.activeElement;
			const open = proposalRef.current;
			focusReopen.current =
				active === frameRef.current ||
				(open !== null && (active === document.body || open.contains(active)));
			setNavigatedAway(true);
			setProposal(null);
		}
	};

	const reopenRef = (button: HTMLButtonElement | null) => {
		if (button && focusReopen.current) {
			focusReopen.current = false;
			button.focus();
		}
	};

	const setFrame = (frame: HTMLIFrameElement | null) => {
		frameRef.current = frame;
		if (frame && focusFrame.current) {
			focusFrame.current = false;
			frame.focus();
		}
	};

	const reopen = (event: React.MouseEvent<HTMLButtonElement>) => {
		focusFrame.current = document.activeElement === event.currentTarget;
		frameLoads.current = 0;
		setFrameKey((key) => key + 1);
		setNavigatedAway(false);
	};

	// Input inside a frame never reaches the dashboard, so recent dashboard
	// input means the activation came from the dashboard, not the page. That
	// includes Tab: a key press that moves focus into the frame activates the
	// dashboard too. The viewer counts its own mount as input, because a click
	// in the dashboard, such as on the Attachments menu, may have mounted it
	// before its listeners existed.
	useEffect(() => {
		lastDashboardInputAt.current = Date.now();
		const onInput = () => {
			lastDashboardInputAt.current = Date.now();
		};
		window.addEventListener("pointerdown", onInput, true);
		window.addEventListener("keydown", onInput, true);
		return () => {
			window.removeEventListener("pointerdown", onInput, true);
			window.removeEventListener("keydown", onInput, true);
		};
	}, []);

	useEffect(() => {
		// An open proposal stays as shown: the page cannot swap its text
		// while the user reads it. A send still in flight, for a proposal
		// that a navigation closed, keeps the next one out until it settles.
		if (!agentId || proposal !== null || sendPending) {
			return;
		}
		const onMessage = (event: MessageEvent) => {
			const frameWindow = frameRef.current?.contentWindow;
			if (!frameWindow || event.source !== frameWindow) {
				return;
			}
			// A click in the frame activates this page and focuses the frame,
			// and the dashboard does not see it. Without all three, a script
			// in the page could propose messages on its own, or ride a click
			// in the dashboard. These checks come before parsing, so a page
			// cannot make the dashboard parse its messages on its own.
			const now = Date.now();
			if (
				!navigator.userActivation?.isActive ||
				document.activeElement !== frameRef.current ||
				now - lastDashboardInputAt.current < forkHtmlMessageIntervalMs
			) {
				return;
			}
			const text = parseForkHtmlMessage(event.data);
			if (
				text === null ||
				now - lastProposedAt.current < forkHtmlMessageIntervalMs
			) {
				return;
			}
			lastProposedAt.current = now;
			resetSend();
			setProposal(formatForkHtmlMessage(block.name, text));
		};
		window.addEventListener("message", onMessage);
		return () => window.removeEventListener("message", onMessage);
	}, [agentId, block.name, proposal, resetSend, sendPending]);

	// Focus on the proposal, or on a button that a pending send disabled,
	// would fall to the document body, so it goes back to the page.
	const closeProposal = () => {
		const active = document.activeElement;
		if (active === document.body || proposalRef.current?.contains(active)) {
			frameRef.current?.focus();
		}
		setProposal(null);
	};

	// The proposal stays open until the send succeeds, so a failed send can
	// be retried. A pending send disables the focused Send button, so focus
	// falls to the document body, and after a failure it goes back to Send.
	const send = () => {
		if (!agentId || proposal === null) {
			return;
		}
		sendMessage.mutate(
			{
				chatId: agentId,
				req: { content: [{ type: "text", text: proposal }] },
			},
			{
				onSuccess: closeProposal,
				onError: () => {
					focusSend.current = document.activeElement === document.body;
				},
			},
		);
	};

	// The error may render in the same commit that ends the pending state,
	// so this runs after every commit.
	useEffect(() => {
		if (!sendPending && focusSend.current) {
			focusSend.current = false;
			sendRef.current?.focus();
		}
	});

	let status = "";
	if (sendMessage.isPending) {
		status = "Sending to the chat";
	} else if (sendMessage.isError) {
		status = getErrorMessage(sendMessage.error, "Could not send to the chat.");
	} else if (sendMessage.isSuccess) {
		status = "Sent to the chat";
	} else if (proposal !== null) {
		// Focus stays in the page, so the live status tells keyboard and
		// screen reader users that a proposal opened.
		status = "The page asks to send a message. Review it below the page.";
	}

	return (
		<figure aria-labelledby={headerId} className="flex w-full flex-col gap-1">
			{/* The header sits outside the frame, so the page cannot hide or
			    restyle it. */}
			<figcaption className="flex min-w-0 items-center gap-1 text-xs text-content-secondary">
				<span id={headerId} className="min-w-0 break-words">
					Page attached by the agent: {download.displayName}. It cannot see your
					session. Do not type passwords or secrets.
				</span>
				<AttachmentDownloadLink
					{...download}
					className="flex size-6 shrink-0 items-center justify-center rounded-md text-content-secondary transition-colors hover:bg-surface-secondary hover:text-content-primary focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
				>
					<DownloadIcon aria-hidden="true" className="size-3.5" />
				</AttachmentDownloadLink>
			</figcaption>
			{navigatedAway ? (
				<div className="flex h-96 w-full flex-col items-center justify-center gap-2 rounded-md border border-solid border-border p-4 text-center text-sm text-content-secondary">
					<p className="m-0">
						The page tried to load another address, so the viewer closed it.
					</p>
					<Button ref={reopenRef} size="sm" variant="outline" onClick={reopen}>
						Show the page again
					</Button>
				</div>
			) : (
				<iframe
					key={frameKey}
					ref={setFrame}
					src={getForkHtmlAttachmentURL(block.file_id)}
					sandbox="allow-scripts"
					referrerPolicy="no-referrer"
					loading="lazy"
					title={download.displayName}
					onLoad={onFrameLoad}
					className="h-96 w-full rounded-md border border-solid border-border bg-white"
				/>
			)}
			{proposal !== null && (
				<section
					ref={proposalRef}
					aria-labelledby={proposalId}
					className="flex flex-col gap-2 rounded-md border border-solid border-border p-2 text-sm"
				>
					<p id={proposalId} className="m-0 text-content-secondary">
						The page asks to send this message to the chat:
					</p>
					{/* A long message scrolls, and the scroller takes focus so a
					    keyboard user can read all of it before Send. */}
					<pre
						// oxlint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- a scrolling region must be keyboard reachable.
						tabIndex={0}
						role="region"
						aria-label="Message to send"
						className="m-0 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-sm font-sans text-content-primary focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
					>
						{proposal}
					</pre>
					<div className="flex justify-end gap-2">
						<Button
							size="sm"
							variant="outline"
							disabled={sendMessage.isPending}
							onClick={closeProposal}
						>
							Cancel
						</Button>
						<Button
							ref={sendRef}
							size="sm"
							disabled={sendMessage.isPending}
							onClick={send}
						>
							Send
						</Button>
					</div>
				</section>
			)}
			<span
				role="status"
				className="break-words text-xs text-content-secondary empty:hidden"
			>
				{status}
			</span>
		</figure>
	);
};
