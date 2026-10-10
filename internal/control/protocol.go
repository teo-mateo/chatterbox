// Package control is the local protocol between a running listener and the
// short-lived commands: line-delimited JSON over a Unix stream socket. The
// client sends one request line; the listener answers with zero or more
// progress lines and exactly one final line, then closes.
package control

// Version is the protocol version carried in every line.
const Version = 1

// MaxRequestBytes bounds one request line.
const MaxRequestBytes = 1 << 20

// Operations.
const (
	OpPublish       = "publish"
	OpSend          = "send"
	OpNames         = "names"
	OpChannels      = "channels"
	OpCreateChannel = "create_channel"
	OpStatus        = "status"
)

// Error codes of a failed response. Their exit codes are in the cli package.
const (
	CodeNotConnected       = "not_connected"
	CodeBadRequest         = "bad_request"
	CodeUnknownNick        = "unknown_nick"
	CodeRejected           = "rejected"
	CodeTimeout            = "timeout"
	CodeTooLarge           = "too_large"
	CodeChannelExists      = "channel_exists"
	CodeUnsupportedVersion = "unsupported_version"
)

// Values of Response.Sent on a failed publish or send.
const (
	SentNo      = "no"
	SentPartial = "partial"
	SentUnknown = "unknown"
)

// Request is the single line a command sends.
type Request struct {
	V         int    `json:"v"`
	Op        string `json:"op"`
	Text      string `json:"text,omitempty"`
	To        string `json:"to,omitempty"`
	Important bool   `json:"important,omitempty"`
	Private   bool   `json:"private,omitempty"`
	Name      string `json:"name,omitempty"`
}

// Progress reports how far a split message has got. An empty progress line
// only tells the client that the listener is still working.
type Progress struct {
	Part int `json:"part,omitempty"`
	Of   int `json:"of,omitempty"`
}

// Participant is one entry of a names response.
type Participant struct {
	Nick string `json:"nick"`
	Kind string `json:"kind"`
}

// ChannelInfo is one entry of a channels response. LastActivity is nil when
// the server does not expose it.
type ChannelInfo struct {
	Name         string  `json:"name"`
	Users        int     `json:"users"`
	LastActivity *string `json:"last_activity"`
}

// Response is a progress line (Progress set) or the final line (OK set).
type Response struct {
	V        int       `json:"v"`
	Progress *Progress `json:"progress,omitempty"`
	OK       *bool     `json:"ok,omitempty"`

	Nick         string        `json:"nick,omitempty"`
	Channel      string        `json:"channel,omitempty"`
	Parts        int           `json:"parts,omitempty"`
	Participants []Participant `json:"participants,omitempty"`
	Channels     []ChannelInfo `json:"channels,omitempty"`
	State        string        `json:"state,omitempty"`
	PID          int           `json:"pid,omitempty"`

	Code      string `json:"code,omitempty"`
	Sent      string `json:"sent,omitempty"`
	PartsSent int    `json:"parts_sent,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Final reports whether this is the last line of an exchange.
func (r Response) Final() bool { return r.OK != nil }

// Succeeded reports whether this is a successful final line.
func (r Response) Succeeded() bool { return r.OK != nil && *r.OK }

// OKResponse starts a successful final line.
func OKResponse() Response {
	ok := true
	return Response{V: Version, OK: &ok}
}

// ErrResponse builds a failed final line.
func ErrResponse(code, msg string) Response {
	ok := false
	return Response{V: Version, OK: &ok, Code: code, Error: msg}
}

// ProgressResponse builds a progress line.
func ProgressResponse(part, of int) Response {
	return Response{V: Version, Progress: &Progress{Part: part, Of: of}}
}
