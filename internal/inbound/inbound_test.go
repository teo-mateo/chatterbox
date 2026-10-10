package inbound

import (
	"reflect"
	"testing"
	"time"

	"github.com/teo-mateo/chatterbox/internal/identity"
)

var self = Self{Nick: "agentA", Channel: "#hof", Casemap: identity.CasemapASCII}

func raw(from, target, text string) Raw {
	return Raw{Time: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), From: from, Target: target, Text: text, MsgID: "id1"}
}

func classify(t *testing.T, r Raw) Message {
	t.Helper()
	m, ok := Classify(r, self)
	if !ok {
		t.Fatalf("message dropped: %+v", r)
	}
	return m
}

func TestDropped(t *testing.T) {
	for name, r := range map[string]Raw{
		"own message":             raw("agentA", "#hof", "hi"),
		"own message, other case": raw("AGENTA", "#hof", "hi"),
		"other channel":           raw("b", "#other", "hi"),
		"other nick":              raw("b", "agentB", "hi"),
		"ctcp version":            raw("b", "agentA", "\x01VERSION\x01"),
		"ctcp ping":               raw("b", "#hof", "\x01PING 123\x01"),
	} {
		if _, ok := Classify(r, self); ok {
			t.Errorf("%s: delivered", name)
		}
	}
}

func TestBasicFields(t *testing.T) {
	r := raw("teo", "#HOF", "hello")
	m := classify(t, r)
	if m.From != "teo" || m.Kind != KindHuman || m.Channel != "#HOF" || m.Private || m.Addressed || m.Important || m.MsgID != "id1" || m.Text != "hello" {
		t.Errorf("%+v", m)
	}
	r.Bot = true
	if classify(t, r).Kind != KindBot {
		t.Error("bot tag not reflected in kind")
	}
	p := classify(t, raw("teo", "AgentA", "psst"))
	if !p.Private || !p.Addressed || p.Channel != "" {
		t.Errorf("private message: %+v", p)
	}
}

func TestCTCPAction(t *testing.T) {
	m := classify(t, raw("teo", "#hof", "\x01ACTION waves at @agentA\x01"))
	if m.Text != "* teo waves at @agentA" || !m.Addressed {
		t.Errorf("%+v", m)
	}
}

func TestStripFormatting(t *testing.T) {
	cases := map[string]string{
		"plain":                            "plain",
		"\x02bold\x02 \x1ditalic\x1d":      "bold italic",
		"\x0304red\x03 text":               "red text",
		"\x0304,12both\x0f":                "both",
		"\x033,5x":                         "x",
		"\x03,5 keeps comma":               ",5 keeps comma",
		"\x04ff00aahex\x04":                "hex",
		"\x04ff00aa,001122hex":             "hex",
		"\x1funder\x1f\x1estrike\x11m\x16": "understrikem",
		"1\x03123":                         "13",
	}
	for in, want := range cases {
		if got := StripFormatting(in); got != want {
			t.Errorf("StripFormatting(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitAddressPrefix(t *testing.T) {
	cases := []struct {
		in    string
		names []string
		rest  string
	}{
		{"agentA: hello", []string{"agentA"}, "hello"},
		{"agentA, hello there", []string{"agentA"}, "hello there"},
		{"agentA, agentB: hello", []string{"agentA", "agentB"}, "hello"},
		{"agentA,agentB: hello", []string{"agentA", "agentB"}, "hello"},
		{"a, b, c: x", []string{"a", "b", "c"}, "x"},
		{"agentA:", []string{"agentA"}, ""},
		{"agentA:   spaced", []string{"agentA"}, "spaced"},
		{"te[o]: hi", []string{"te[o]"}, "hi"},
		{"hello world", nil, "hello world"},
		{"agentA:nospace", nil, "agentA:nospace"},
		{"agentA,nospace x", nil, "agentA,nospace x"},
		{"http://example.org", nil, "http://example.org"},
		{": x", nil, ": x"},
		{"", nil, ""},
		{"[IMPORTANT] x", nil, "[IMPORTANT] x"},
		{"agentA finished the migration", nil, "agentA finished the migration"},
	}
	for _, c := range cases {
		names, rest := SplitAddressPrefix(c.in)
		if !reflect.DeepEqual(names, c.names) || rest != c.rest {
			t.Errorf("SplitAddressPrefix(%q) = %q, %q; want %q, %q", c.in, names, rest, c.names, c.rest)
		}
	}
}

func TestImportant(t *testing.T) {
	cases := []struct {
		in        string
		important bool
		text      string
		addressee bool
	}{
		{"[IMPORTANT] stop", true, "stop", false},
		{"agentA: [IMPORTANT] stop", true, "agentA: stop", true},
		{"agentB: [IMPORTANT] stop", true, "agentB: stop", true},
		{"[IMPORTANT]", true, "", false},
		{"agentA: [IMPORTANT]", true, "agentA:", true},
		{"[IMPORTANT]stop", false, "[IMPORTANT]stop", false},
		{"[important] stop", false, "[important] stop", false},
		{"this is [IMPORTANT] really", false, "this is [IMPORTANT] really", false},
		{"\x02[IMPORTANT]\x02 stop", true, "stop", false},
	}
	for _, c := range cases {
		m := classify(t, raw("b", "#hof", c.in))
		if m.Important != c.important || m.Text != c.text || m.HasAddressee != c.addressee {
			t.Errorf("%q: important=%v text=%q addressee=%v", c.in, m.Important, m.Text, m.HasAddressee)
		}
	}
}

func TestAddressed(t *testing.T) {
	yes := []string{
		"agentA: hi", "AGENTA: hi", "agentA, hi", "agentB, agentA: hi", "agentA, agentB: hi",
		"hey @agentA can you look", "@agentA", "ping @AgentA.", "(@agentA)", "cc @agentB @agentA",
	}
	no := []string{
		"hi all", "agentA finished the migration", "agentB: hi", "@agentA-2 hi", "@agentA_x", "@agentAA",
		"mail me at bob@agentA", "agentAB: hi", "`agentA` is done",
	}
	for _, s := range yes {
		if !classify(t, raw("b", "#hof", s)).Addressed {
			t.Errorf("%q should be addressed to agentA", s)
		}
	}
	for _, s := range no {
		if classify(t, raw("b", "#hof", s)).Addressed {
			t.Errorf("%q should not be addressed to agentA", s)
		}
	}
}

func TestAddressedCasemapping(t *testing.T) {
	s := Self{Nick: "a[x]", Channel: "#hof", Casemap: identity.CasemapRFC1459}
	m, _ := Classify(raw("b", "#HOF", "A{X}: hi"), s)
	if !m.Addressed {
		t.Error("rfc1459 casemapped prefix not recognized")
	}
	if m, _ := Classify(raw("b", "#hof", "A{X}: hi"), Self{Nick: "a[x]", Channel: "#hof"}); m.Addressed {
		t.Error("ascii casemapping must not fold brackets")
	}
}

func TestWakeFilter(t *testing.T) {
	cases := []struct {
		text      string
		target    string
		all, addr bool
	}{
		{"hello everyone", "#hof", true, false},
		{"agentA: hi", "#hof", true, true},
		{"@agentA hi", "#hof", true, true},
		{"agentB: hi", "#hof", true, false},
		{"agentA finished", "#hof", true, false},
		{"[IMPORTANT] everyone stop", "#hof", true, true},
		{"agentB: [IMPORTANT] stop", "#hof", true, false}, // D7
		{"agentA: [IMPORTANT] stop", "#hof", true, true},
		{"psst", "agentA", true, true},
	}
	for _, c := range cases {
		m := classify(t, raw("b", c.target, c.text))
		if got := Printed(m, WakeAll); got != c.all {
			t.Errorf("%q under all: %v", c.text, got)
		}
		if got := Printed(m, WakeAddressed); got != c.addr {
			t.Errorf("%q under addressed: %v", c.text, got)
		}
	}
}
