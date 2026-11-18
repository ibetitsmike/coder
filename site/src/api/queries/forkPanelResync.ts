// This file is fork code. It stays apart from forkChatWatch.ts to avoid an
// import cycle with the panel queries that read it.

/**
 * The resync poll of the panels that the chat watch stream refreshes. It
 * catches a dropped pubsub message and a change to a chat the viewer does
 * not own, which the per-owner stream never carries.
 */
export const forkPanelResyncInterval = 60_000;
