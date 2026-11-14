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
import type { ParsedMessageEntry } from "./types";

type HtmlFilePart = TypesGen.ChatFilePart & { file_id: string; name: string };

const LatestHtmlAttachmentsContext = createContext<
	ReadonlyMap<string, string> | undefined
>(undefined);

/**
 * Wraps the upstream FileProbeProvider so the timeline needs one hook line,
 * and tells each HTML attachment whether a newer one with its name exists.
 */
export const ForkFileProbeProvider: React.FC<
	React.PropsWithChildren<{
		evictedFileIds: ReadonlySet<string>;
		entries: readonly Pick<ParsedMessageEntry, "message">[];
	}>
> = ({ evictedFileIds, entries, children }) => (
	<FileProbeProvider evictedFileIds={evictedFileIds}>
		<LatestHtmlAttachmentsContext.Provider
			value={latestForkHtmlAttachmentIds(entries)}
		>
			{children}
		</LatestHtmlAttachmentsContext.Provider>
	</FileProbeProvider>
);

type ForkHtmlAttachmentPreviewProps = {
	block: HtmlFilePart;
	/** The upstream card, shown for a page that a newer one replaced. */
	children: React.ReactNode;
};

/** Only the newest attachment of a name renders as a live page. */
export const ForkHtmlAttachmentPreview: React.FC<
	ForkHtmlAttachmentPreviewProps
> = ({ block, children }) => {
	const latestFileId = useContext(LatestHtmlAttachmentsContext)?.get(
		block.name,
	);
	if (latestFileId !== undefined && latestFileId !== block.file_id) {
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
 * takes only proposals from its own frame, right after a click, and at most
 * one per forkHtmlMessageIntervalMs. It shows the exact message and sends it
 * only when the user confirms outside the frame.
 */
const ForkHtmlAttachmentViewer: React.FC<{ block: HtmlFilePart }> = ({
	block,
}) => {
	const { agentId } = useParams();
	const queryClient = useQueryClient();
	const sendMessage = useMutation(createChatMessageByChatId(queryClient));
	const frameRef = useRef<HTMLIFrameElement>(null);
	const lastProposedAt = useRef(Number.NEGATIVE_INFINITY);
	const [proposal, setProposal] = useState<string | null>(null);
	const headerId = useId();
	const proposalId = useId();
	const download = getAttachmentDownload(block, getChatFileURL(block.file_id));
	const frameLoads = useRef(0);
	const [navigatedAway, setNavigatedAway] = useState(false);
	const [frameKey, setFrameKey] = useState(0);

	// The page itself is the first load. Any later load is the page leaving
	// for another address, or reloading. A proposal still open may come from
	// that other address, so it goes too.
	const onFrameLoad = () => {
		frameLoads.current += 1;
		if (frameLoads.current > 1) {
			setNavigatedAway(true);
			setProposal(null);
		}
	};

	const reopen = () => {
		frameLoads.current = 0;
		setFrameKey((key) => key + 1);
		setNavigatedAway(false);
	};

	useEffect(() => {
		// An open proposal stays as shown: the page cannot swap its text
		// while the user reads it.
		if (!agentId || proposal !== null) {
			return;
		}
		const onMessage = (event: MessageEvent) => {
			const frameWindow = frameRef.current?.contentWindow;
			if (!frameWindow || event.source !== frameWindow) {
				return;
			}
			const text = parseForkHtmlMessage(event.data);
			// A click in the frame also activates this page. Without one,
			// a script in the page could propose messages on its own.
			if (text === null || !navigator.userActivation?.isActive) {
				return;
			}
			const now = Date.now();
			if (now - lastProposedAt.current < forkHtmlMessageIntervalMs) {
				return;
			}
			lastProposedAt.current = now;
			setProposal(formatForkHtmlMessage(block.name, text));
		};
		window.addEventListener("message", onMessage);
		return () => window.removeEventListener("message", onMessage);
	}, [agentId, block.name, proposal]);

	const send = () => {
		if (!agentId || proposal === null) {
			return;
		}
		sendMessage.mutate({
			chatId: agentId,
			req: { content: [{ type: "text", text: proposal }] },
		});
		setProposal(null);
	};

	let status = "";
	if (sendMessage.isPending) {
		status = "Sending to the chat";
	} else if (sendMessage.isError) {
		status = getErrorMessage(sendMessage.error, "Could not send to the chat.");
	} else if (sendMessage.isSuccess) {
		status = "Sent to the chat";
	}

	return (
		<figure aria-labelledby={headerId} className="flex w-full flex-col gap-1">
			{/* The header sits outside the frame, so the page cannot hide or
			    restyle it. */}
			<figcaption className="flex min-w-0 items-center gap-1 text-xs text-content-secondary">
				<span id={headerId} className="min-w-0">
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
					<Button size="sm" variant="outline" onClick={reopen}>
						Show the page again
					</Button>
				</div>
			) : (
				<iframe
					key={frameKey}
					ref={frameRef}
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
					aria-labelledby={proposalId}
					className="flex flex-col gap-2 rounded-md border border-solid border-border p-2 text-sm"
				>
					<p id={proposalId} className="m-0 text-content-secondary">
						The page asks to send this message to the chat:
					</p>
					<pre className="m-0 max-h-40 overflow-auto whitespace-pre-wrap break-words font-sans text-content-primary">
						{proposal}
					</pre>
					<div className="flex justify-end gap-2">
						<Button
							size="sm"
							variant="outline"
							onClick={() => setProposal(null)}
						>
							Cancel
						</Button>
						<Button size="sm" onClick={send}>
							Send
						</Button>
					</div>
				</section>
			)}
			<span
				role="status"
				className="truncate text-xs text-content-secondary empty:hidden"
			>
				{status}
			</span>
		</figure>
	);
};
