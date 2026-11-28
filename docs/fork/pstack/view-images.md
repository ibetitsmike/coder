# View images

This page is for a Coder Agents user who runs pstack skills, and shows how to load an image into the model's context and show the user an HTML page.
This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

`view_image` exists only when the deployment enables the `fork-view-image` experiment.
It loads a PNG, JPEG, GIF, or WebP file from the workspace into the model's context as an image.
It detects the type from the file's content, not its name, so a text file named `shot.png` fails with `/path/shot.png is text/plain, not a PNG, JPEG, GIF or WebP image. Use read_file for text files`.
On the Anthropic provider, and on Bedrock through the Anthropic API, a file over 3,932,160&nbsp;bytes fails, so that its base64 form fits Anthropic's 5&nbsp;MiB per-image limit.
On other models, a file over 10&nbsp;MiB, the chat tool media limit, fails.
The error names the limit and its reason, such as `... is larger than 3932160 bytes, the view_image limit: ...`.
An image wider or taller than 8000&nbsp;pixels fails, because Anthropic rejects one.
An image over 25,000,000&nbsp;pixels in total fails too, which bounds the memory of the decode below.
A truncated or damaged file fails with `... is not a valid image/png image: ...`, because the tool checks the file's structure: the PNG chunk checksums, the JPEG and GIF end markers, and the WebP chunks, which must fit in the file and hold image data.
The tool then decodes a PNG, a JPEG, or the first frame of a GIF, so damaged image data behind an intact structure fails as well.
WebP gets only the structure check, because Go has no WebP decoder in its standard library.
An animated GIF fails with `... is an animated GIF with N frames, and view_image accepts only still images`, because OpenAI rejects animated GIFs.
One model response can call `view_image` at most 8 times, since only 8 viewed images stay in context.
Further calls in that response fail with `view_image runs at most 8 times per response`.
The tool doesn't resize images, so scale a large one down first.
The tool uses the path as given, as `read_file` does, so it doesn't trim spaces from it.

If the chat's model doesn't take images, the call fails with `model "<model>" on provider "<provider>" does not accept image input. Switch to a model with image input to view images`.
Google models don't take GIF, so a GIF fails there with `does not accept image/gif images`.
The OpenAI-compatible, OpenRouter, and Vercel providers report that they take any image, so Coder Agents can't tell a text-only model behind them, and the provider gets the image anyway.

The image stays in the chat's stored history like other tool results, and later turns send it again.
Each request carries at most the 8 newest `view_image` images, always with the newest one.
Together with the prompt's other images, such as `computer` screenshots and attachments, including those of the message being answered, a request carries at most 20 images, because Anthropic tightens its per-image limits above 20.
Their size and the size of those other images add up to at most 10&nbsp;MiB, so the base64 form fits Bedrock's 20&nbsp;MB request limit, except that the newest one stays even when it goes over.
An older one becomes the note `[image no longer in context: only the newest viewed images are kept. Call view_image again to see it.]`, so call `view_image` again to see it.
After a switch to a model with a lower size limit, such as from OpenAI to Anthropic, and in the summary request of a compaction model override with a lower limit, an earlier image over the new limit becomes the note `[image omitted: ... is larger than the view_image limit of ... bytes for this model. ...]` and doesn't count toward the 8 images.
While the experiment is off, a chat whose client supplies its own `view_image` tool keeps that tool's results as they are, on turns that offer the client's tools.
After a compaction, the images before the summary are no longer sent, like any other tool result.
The web UI shows the call as a tool card with the image.
Plan-mode turns and `explore` children don't get `view_image`.

## HTML pages

With the `fork-html-attachments` experiment, the web UI shows an attached `.html` or `.htm` file as a live page, inline and in the turn's **Attachments** menu.
Above the page, outside it, a fixed line names the page and warns the user, for example "Page attached by the agent: choices.html. It cannot see your session. Do not type passwords or secrets."
A download link sits next to it.
Without the experiment, the page shows as a download card.
When you attach a file with the same name again in the same chat, the new page replaces the earlier one, and the earlier attachment shows as a download card.
Only pages that the agent attaches run.
An `.html` file that the user uploads shows as a download card.

The page runs in a sandbox:

- Scripts run, but the page has its own opaque origin, so it can't read the user's Coder session, cookies, or storage, or reach the dashboard.
- The page can't fetch or load anything over the network, so it can't call the Coder API or load anything from elsewhere.
  Put scripts, styles, images, and fonts in the file, with images and fonts as `data:` URLs.
- Forms don't submit.
  Handle a choice in a button's click handler instead.
- The sandbox has no `allow-modals` and no `allow-downloads`, so `alert`, `confirm`, `prompt`, and downloads from inside the page do nothing.
- The page route sends the same sandbox policy on every response, errors included, so opening its URL directly never runs a script on the Coder origin.

The page can propose the user's choice as a new user message in the chat:

```js
parent.postMessage({ type: "coder:chat-message", text: "Option B" }, "*");
```

The web UI shows the exact message below the page with **Send** and **Cancel**, and sends it only when the user selects **Send**.
A long message scrolls, and keyboard users can reach it with Tab.
If the message fails to send, the proposal stays open with the error, and focus goes to **Send**, so the user can select it again.
Focus stays in the page, and a status line below it announces the proposal.
When the proposal closes, focus goes back to the page, unless the user acted elsewhere in the dashboard while the message was sending.
The chat gets the text under a line that names the page, such as `Message from the attached page "choices.html":`.
The web UI shows a proposal only when all of these hold:

- The message has exactly the fields `type` and `text`.
- The trimmed text has 1 to 4000&nbsp;characters.
- The user just selected something in the page, and the page's frame has focus.
  A message that a script sends on its own is dropped.
- The dashboard saw no pointer or key press, Tab included, in the last 5&nbsp;seconds.
  A keyboard user who tabs into the page waits 5&nbsp;seconds before a choice in it counts.
  The page appearing counts as such input too, because a selection in the dashboard, such as on the **Attachments** menu, may have opened it.
- No proposal from the page came in the last 5&nbsp;seconds.
- No proposal is open, and no message from the page is still sending.
  While a proposal is open, the page can't change its text.

A page can take focus on its own.
If a chat shows two attached pages, a hostile page that takes focus right after the user selects something in the other page can propose a message as if the user had selected it.
The proposal names the page that sent it, and nothing reaches the chat until the user selects **Send**.

Two gaps stay open, so treat what the user does in a page as visible to whoever wrote it.
The fixed line above the page tells the user not to type secrets.

- WebRTC: the content security policy doesn't cover it.
  The policy sets `webrtc 'block'`, but Chromium ignores that directive, so a page can still send data out over WebRTC.
- Navigation: the sandbox lets the page navigate its own frame, and that navigation can carry what the user typed in its URL.
  The dashboard's own policy (`frame-src`) blocks frames from other sites, so the browser refuses such a navigation before any request.
  It still lets the frame load the Coder origin, the deployment's workspace app hosts, and any frame hosts the deployment adds to its policy.
  When the frame loads anything after the page, the viewer closes it, drops an open proposal, and offers to show the page again.
  A page that navigates away before its first load finishes is not detected, because the viewer can't read where a sandboxed frame is.
