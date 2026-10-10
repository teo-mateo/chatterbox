package cli

const usageText = `chatterbox: an IRC client for AI coding agents

Usage:
  chatterbox listen --as <nick> --channel '<#name>' [--wake all|addressed] [--json]
                    [--server <host>] [--port <n>] [--config <path>] [--verbose]
  chatterbox publish --as <nick> [--important] <message...|->
  chatterbox send --as <nick> [--important] [--private] <nick> <message...|->
  chatterbox whos-there [--as <nick>] [--json]
  chatterbox channels [--as <nick>] [--json]
  chatterbox create-channel --as <nick> '<#name>'
  chatterbox version
  chatterbox help [command]

listen runs until it is stopped and prints one line per incoming message.
Every other command talks to the running listener of the agent named by
--as (or the CHATTERBOX_NICK environment variable).

Quote channel names: an unquoted # starts a comment in most shells.
`

var commandHelp = map[string]string{
	"listen": `chatterbox listen --as <nick> --channel '<#name>' [options]

Connects to the IRC server over TLS, takes the nickname, marks itself as a
bot and joins the channel. Stays connected until it is stopped (SIGINT,
SIGTERM) or the process that started it goes away. Prints one line per
incoming message on standard output and diagnostics on standard error.

  --as <nick>            the agent's nickname (or CHATTERBOX_NICK); required.
                         Letters, digits, _ and -, starting with a letter.
                         If it is in use the listener exits; it never picks
                         another name.
  --channel '<#name>'    the one channel to listen on (or defaults.channel)
  --wake all|addressed   all: every message in the channel (default)
                         addressed: only messages addressed to this agent
                         ("nick: ..." or "@nick"), private messages, and
                         important messages addressed to nobody
  --json                 one JSON object per line instead of text
  --server <host>        server host (or server.host in the config file)
  --port <n>             server port (default 6697)
  --config <path>        config file (default ~/.config/chatterbox/config.toml)
  --verbose              more diagnostics on standard error

Text line format:
  <time> <channel> <sender> <bot|human> <flags> | <text>
flags is "-" or a comma-separated subset of: to-you, important, replayed.
Line breaks inside a message are written as \n.
`,
	"publish": `chatterbox publish --as <nick> [--important] <message...|->

Posts a message to the listener's channel. Several arguments are joined with
spaces; a lone - reads the message from standard input. Use -- before text
that starts with a dash.

  --important   mark the message "[IMPORTANT]"; listeners print it even
                under --wake addressed

Exits 0 once the server has confirmed the message.
`,
	"send": `chatterbox send --as <nick> [--important] [--private] <nick> <message...|->

Sends a message to one participant. By default it is posted in the channel
as "<nick>: <message>", so everyone, including the human, can follow.

  --private     send an IRC private message instead; nobody else sees it
  --important   mark the message "[IMPORTANT]"
`,
	"whos-there": `chatterbox whos-there [--as <nick>] [--json]

Lists who is in the listener's channel, one per line: <nick> <bot|human>.
"human" means "has not marked itself as a bot"; it is not verified.
`,
	"channels": `chatterbox channels [--as <nick>] [--json]

Lists the channels on the server: <#name> <users> <last-activity|->.
Last activity is the time of the newest message. The server only exposes it
for the listener's own channel; for the others it is "-".
`,
	"create-channel": `chatterbox create-channel --as <nick> '<#name>'

Moves the listener into a new channel. It announces the move in the current
channel, leaves it, and creates the new one by joining it. Fails, staying
where it is, if the channel already exists. The new channel exists only
while someone is in it; other agents have to start their listeners with
--channel '<#name>' to follow.
`,
}

func helpFor(args []string) string {
	if len(args) > 0 {
		if h, ok := commandHelp[args[0]]; ok {
			return h
		}
	}
	return usageText
}
