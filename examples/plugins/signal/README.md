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

Users reach the plugin in one of two ways. A deployment can offer both.

| Way         | How users talk to the plugin                                                                      | What it needs                                      |
|-------------|---------------------------------------------------------------------------------------------------|----------------------------------------------------|
| Bot account | Users message a phone number that is registered with signal-cli-rest-api.                         | A phone number for the bot.                        |
| Own account | Each user adds the server as a linked device of their own account and writes to **Note to Self**. | A server that each user trusts with their account. |

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
coder exp plugins install signal.wasm \
  --config account=+15551234567 \
  --allow-private-cidr 10.0.0.0/8
```

- `account` is the phone number of the bot account. Leave it out if users
  only connect their own accounts.
- `--allow-private-cidr` lets the plugin reach a server on a private network,
  for example in the same cluster. Leave it out for a public server.

Give the plugin a signal-cli-rest-api server of its own. The plugin receives
the messages of every account on the server, so another program that reads
from the same server would miss messages.

## Link through the bot account

1. In Coder, open **Settings** > **Linked accounts** and select **Get link
   code** for the Signal plugin.
1. Send `/link CODE` to the Signal number of the bot. The code expires after
   10 minutes and works once.

## Connect your own account

1. Open the QR code page of the server,
   `http://signal-cli-rest-api:8080/v1/qrcodelink?device_name=Coder`. If users
   can't reach the server, the owner of the server opens the page for them.
1. In Signal on your phone, open **Settings** > **Linked devices**, select
   **Link a new device** and scan the code. Within a few seconds, the plugin
   writes a welcome message to your **Note to Self** chat.
1. In Coder, open **Settings** > **Linked accounts** and select **Get link
   code** for the Signal plugin.
1. Send `/link CODE` to **Note to Self**.

The plugin acts only on what you write to **Note to Self**. It ignores your
other conversations and asks the server not to download attachments, stories,
avatars or stickers. The server still holds the keys of your account and
decrypts every message that you receive, so whoever runs the server can read
your messages. Connect your account only to a server that you trust.

Replies from the plugin in **Note to Self** start with 🤖. The plugin ignores
messages that start with 🤖, so two Coder deployments that are linked to one
account don't answer each other.

To disconnect, unlink the account in Coder, and then remove the **Coder**
device in Signal under **Settings** > **Linked devices**. The owner of the
server can then delete the account from the server:

```shell
curl -X DELETE http://signal-cli-rest-api:8080/v1/devices/+15557654321/local-data
```

## Use

- Send any text to talk to your agent. The text goes to the chat that replied
  to you last, or to a new chat.
- Send `/new TEXT` to start a new chat.
- Send `/help` to see the commands.

Replies from the agent come back to Signal with the chat title in brackets.
Each chat runs as the linked Coder user, so the agent has the same access as
that user.

The plugin polls at most 4 accounts every 5 seconds, so with many connected
accounts, a message can take longer to reach the agent.
