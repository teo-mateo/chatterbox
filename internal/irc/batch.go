package irc

import "strings"

// Batch is a closed IRCv3 batch with everything that arrived inside it, in
// order.
type Batch struct {
	Ref    string
	Type   string
	Params []string // parameters after the type
	Open   Message  // the "BATCH +ref" line, with its tags
	Items  []Item
}

// Item is what the assembler hands out: a single message, or a complete
// top-level batch (nested batches are items of their parent).
type Item struct {
	Msg   *Message
	Batch *Batch
}

// Assembler groups incoming lines into batches.
type Assembler struct {
	open map[string]*openBatch
}

type openBatch struct {
	batch  *Batch
	parent string
}

// Feed consumes one message. It returns an item when something is complete
// at the top level: a message outside any batch, or a top-level batch that
// just closed. While a batch is open it returns nil.
func (a *Assembler) Feed(m Message) *Item {
	if a.open == nil {
		a.open = map[string]*openBatch{}
	}
	parent := Tag(&m, "batch")
	if _, known := a.open[parent]; !known {
		parent = ""
	}

	if m.Command == "BATCH" && len(m.Params) >= 1 {
		ref := m.Params[0]
		switch {
		case strings.HasPrefix(ref, "+") && len(m.Params) >= 2:
			ref = ref[1:]
			a.open[ref] = &openBatch{
				batch:  &Batch{Ref: ref, Type: m.Params[1], Params: append([]string(nil), m.Params[2:]...), Open: m},
				parent: parent,
			}
			return nil
		case strings.HasPrefix(ref, "-"):
			ob, ok := a.open[ref[1:]]
			if !ok {
				return nil
			}
			delete(a.open, ref[1:])
			item := Item{Batch: ob.batch}
			if p, ok := a.open[ob.parent]; ok && ob.parent != "" {
				p.batch.Items = append(p.batch.Items, item)
				return nil
			}
			return &item
		}
		return nil
	}

	if parent != "" {
		ob := a.open[parent]
		ob.batch.Items = append(ob.batch.Items, Item{Msg: &m})
		return nil
	}
	return &Item{Msg: &m}
}

// Reset drops every open batch (the connection ended).
func (a *Assembler) Reset() { a.open = nil }

// OpenCount is the number of batches currently open.
func (a *Assembler) OpenCount() int { return len(a.open) }

// MultilineText joins the lines of a draft/multiline batch: with a line
// break between them, except lines tagged draft/multiline-concat, which are
// appended directly.
func MultilineText(b *Batch) string {
	var sb strings.Builder
	first := true
	for _, it := range b.Items {
		if it.Msg == nil || (it.Msg.Command != "PRIVMSG" && it.Msg.Command != "NOTICE") {
			continue
		}
		if !first && !it.Msg.HasTag("draft/multiline-concat") {
			sb.WriteByte('\n')
		}
		sb.WriteString(Param(it.Msg, -1))
		first = false
	}
	return sb.String()
}

// MultilineCommand returns PRIVMSG or NOTICE for a multiline batch.
func MultilineCommand(b *Batch) string {
	for _, it := range b.Items {
		if it.Msg != nil && (it.Msg.Command == "PRIVMSG" || it.Msg.Command == "NOTICE") {
			return it.Msg.Command
		}
	}
	return "PRIVMSG"
}
