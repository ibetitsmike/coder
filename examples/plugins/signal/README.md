# Signal bridge plugin

This plugin lets users talk to Coder Agents from Signal. It is an example of
the experimental plugin system (`--experiments=plugins`). The plugin runs in a
Wasm sandbox in coderd and can only use the capabilities that an owner grants
at install time.

The plugin talks to a
[signal-cli-rest-api](https://github.com/bbernhard/signal-cli-rest-api) server
that has a registered phone number. Run that server in `normal` or `native`
mode, because the plugin polls `GET /v1/receive/{number}`.

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

- `account` is the phone number that signal-cli-rest-api is registered with.
- `--allow-private-cidr` lets the plugin reach a server on a private network,
  for example in the same cluster. Leave it out for a public server.

## Link a Signal account

1. In Coder, open **Settings** > **Linked accounts** and select **Get link
   code** for the Signal plugin.
1. Send `/link CODE` to the Signal number of the bot. The code expires after
   10 minutes and works once.

## Use

- Send any text to talk to your agent. The text goes to the chat that replied
  to you last, or to a new chat.
- Send `/new TEXT` to start a new chat.
- Send `/help` to see the commands.

Replies from the agent come back to Signal with the chat title in brackets.
Each chat runs as the linked Coder user, so the agent has the same access as
that user.
