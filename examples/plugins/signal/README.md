# Signal bridge plugin

This plugin lets users talk to Coder Agents from Signal. It is an example of
the experimental plugin system (`--experiments=plugins`). The plugin runs in a
Wasm sandbox in coderd and can only use the capabilities that an owner grants
at install time.

The plugin talks to a
[signal-cli-rest-api](https://github.com/bbernhard/signal-cli-rest-api)
server. Run that server in `normal` or `native` mode, because the plugin polls
`GET /v1/receive/{account}`. In `json-rpc` mode that endpoint is a WebSocket,
and plugins can't open one.

Each user adds the server as a linked device of their own Signal account and
talks to their agent in **Note to Self**. The plugin needs no phone number of
its own, but each user trusts the server with their account.

## Build

The plugin can only reach the origin that it is built with, so set the URL of
your signal-cli-rest-api server at build time:

```shell
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared \
  -ldflags "-X main.apiURL=http://signal-cli-rest-api:8080" \
  -o signal.wasm ./examples/plugins/signal
```

## Install

An owner installs the plugin in an organization. The CLI shows the
capabilities that the plugin requests and asks you to grant them:

```shell
coder exp plugins install signal.wasm --allow-private-cidr 10.0.0.0/8
```

`--allow-private-cidr` lets the plugin reach a server on a private network,
for example in the same cluster. Leave it out for a public server.

Give the plugin a signal-cli-rest-api server of its own. The plugin receives
the messages of every account on the server, so another program that reads
from the same server would miss messages.

## Connect your account

1. In Coder, open **Settings** > **Linked accounts** and select **Connect
   account** for the Signal plugin. Coder shows a QR code from the server and
   a `/link CODE` command. The code expires after 10 minutes and works once.
1. In Signal on your phone, open **Settings** > **Linked devices**, select
   **Link a new device** and scan the QR code. Within a few seconds, the plugin
   writes a welcome message to your **Note to Self** chat.
1. Send the `/link CODE` command to **Note to Self**.

The plugin acts only on what you write to **Note to Self**. It ignores your
other conversations and asks the server not to download attachments, stories,
avatars or stickers. The server still holds the keys of your account and
decrypts every message that you receive, so whoever runs the server can read
your messages. Connect your account only to a server that you trust.

Replies from the plugin in **Note to Self** start with 🤖. The plugin ignores
messages that start with 🤖, so two Coder deployments that are linked to one
account don't answer each other.

To disconnect, unlink the account in Coder, and then remove the **Coder**
device in Signal under **Settings** > **Linked devices**. Unlinking in Coder
alone leaves the server a linked device of your account. The owner of the
server can then delete the account from the server:

```shell
curl -X DELETE http://signal-cli-rest-api:8080/v1/devices/+15557654321/local-data
```

## Use

- Send any text to **Note to Self** to talk to your agent. The text goes to
  the chat that replied to you last, or to a new chat.
- Send `/new TEXT` to start a new chat.
- Send `/help` to see the commands.

Replies from the agent come back to **Note to Self** with the chat title in
brackets. Each chat runs as the linked Coder user, so the agent has the same
access as that user.

The plugin polls at most 4 accounts every 5 seconds, so with many connected
accounts, a message can take longer to reach the agent.
