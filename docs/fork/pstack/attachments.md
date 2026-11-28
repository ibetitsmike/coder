# Attachments

This page belongs to [pstack on Coder Agents](../pstack-adapter.md) and, like it, isn't in `docs/manifest.json`.

`attach_file` copies a workspace file into the chat, so the user can open or download it from the conversation.
A file over 10&nbsp;MiB fails with `attachment exceeds 10 MiB size limit`.
The web UI shows a raster image, such as a PNG or a JPEG, as a thumbnail.
Plain text, Markdown, CSV, and JSON open in a text preview.
With the `fork-chat-todos` experiment, the web UI shows an MP4 as a player with a download link, both inline and in the turn's **Attachments** menu.
Without the experiment, an MP4 shows as a download card, as an SVG does.
Coder Agents doesn't send an attached file back to the model in later turns, so describe in your reply what the file shows.
To look at an image file yourself, use [`view_image`](view-images.md).
