// Package wbxml implements the subset of WAP Binary XML that Exchange
// ActiveSync uses on the wire.
package wbxml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Node is a decoded WBXML element.
//
// Page is the ActiveSync code page the element belongs to (for example 0 for
// AirSync, 2 for Email, 17 for AirSyncBase). Name is the element name as
// defined by the code page. Unknown tokens are named xNN so that callers can
// still ignore them safely.
type Node struct {
	Page     int
	Name     string
	Token    byte
	Text     string
	Children []*Node

	// Binary marks Text as raw opaque data rather than a UTF-8 string.
	Binary bool
	// ForceContent forces the content bit even when the element is empty.
	ForceContent bool
}

// N builds an element node.
func N(page int, name string, children ...*Node) *Node {
	return &Node{Page: page, Name: name, Children: children}
}

// T builds an element with text content.
func T(page int, name, text string) *Node {
	return &Node{Page: page, Name: name, Text: text}
}

// B builds an element with opaque (binary) content.
func B(page int, name string, data []byte) *Node {
	return &Node{Page: page, Name: name, Text: string(data), Binary: true}
}

// Child returns the first direct child with the given name.
func (n *Node) Child(name string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ChildrenNamed returns all direct children with the given name.
func (n *Node) ChildrenNamed(name string) []*Node {
	if n == nil {
		return nil
	}
	var out []*Node
	for _, c := range n.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// ChildText returns the text of the first matching child.
func (n *Node) ChildText(name string) string {
	if c := n.Child(name); c != nil {
		return c.Text
	}
	return ""
}

// Path walks a chain of child names.
func (n *Node) Path(names ...string) *Node {
	cur := n
	for _, name := range names {
		if cur = cur.Child(name); cur == nil {
			return nil
		}
	}
	return cur
}

// Lookup resolves element names and tokens for the code pages in use.
type Lookup interface {
	Token(page int, name string) (byte, bool)
	Name(page int, token byte) (string, bool)
}

// Encode serialises a tree into a WBXML 1.3 document.
func Encode(root *Node, lookup Lookup) ([]byte, error) {
	if root == nil {
		return nil, errors.New("wbxml: nil root")
	}
	var body bytes.Buffer
	e := &encoder{lookup: lookup}
	if err := e.node(&body, root); err != nil {
		return nil, err
	}
	out := make([]byte, 0, body.Len()+4)
	// Version 1.3, public id 1, UTF-8, empty string table.
	out = append(out, 0x03, 0x01, 0x6A, 0x00)
	return append(out, body.Bytes()...), nil
}

type encoder struct {
	lookup Lookup
	page   int
}

func (e *encoder) node(buf *bytes.Buffer, n *Node) error {
	if n.Page != e.page {
		buf.WriteByte(0x00)
		writeMB(buf, uint32(n.Page))
		e.page = n.Page
	}
	tok, ok := e.lookup.Token(n.Page, n.Name)
	if !ok {
		return fmt.Errorf("wbxml: unknown element %q on page %d", n.Name, n.Page)
	}
	hasContent := len(n.Children) > 0 || n.Text != "" || n.ForceContent
	b := tok
	if hasContent {
		b |= 0x40
	}
	buf.WriteByte(b)
	if !hasContent {
		return nil
	}
	switch {
	case len(n.Children) > 0:
		for _, c := range n.Children {
			if err := e.node(buf, c); err != nil {
				return err
			}
		}
	case n.Binary:
		buf.WriteByte(0xC3)
		writeMB(buf, uint32(len(n.Text)))
		buf.WriteString(n.Text)
	default:
		// STR_I is a NUL-terminated inline string, not length-prefixed.
		buf.WriteByte(0x03)
		buf.WriteString(strings.ReplaceAll(n.Text, "\x00", ""))
		buf.WriteByte(0x00)
	}
	buf.WriteByte(0x01)
	return nil
}

// Decode parses a WBXML document and returns its root element.
func Decode(data []byte, lookup Lookup) (*Node, error) {
	if len(data) < 4 {
		return nil, errors.New("wbxml: document too short")
	}
	// ActiveSync clients emit WBXML 1.3; a few legacy ones still send 1.2.
	// Both use the same token layout, so accept either.
	if data[0] != 0x03 && data[0] != 0x02 {
		return nil, fmt.Errorf("wbxml: unsupported version %#x", data[0])
	}
	d := &decoder{data: data, pos: 1, lookup: lookup}
	if _, err := d.mb(); err != nil { // public id
		return nil, err
	}
	if _, err := d.mb(); err != nil { // charset
		return nil, err
	}
	stLen, err := d.mb()
	if err != nil {
		return nil, err
	}
	if int(stLen) > len(data)-d.pos {
		return nil, errors.New("wbxml: invalid string table")
	}
	d.strTable = data[d.pos : d.pos+int(stLen)]
	d.pos += int(stLen)

	var root *Node
	for d.pos < len(d.data) {
		switch b := d.data[d.pos]; {
		case b == 0x00:
			d.pos++
			p, err := d.mb()
			if err != nil {
				return nil, err
			}
			d.page = int(p)
		case b == 0x01:
			d.pos++
		default:
			n, err := d.element()
			if err != nil {
				return nil, err
			}
			if root == nil {
				root = n
			}
		}
	}
	if root == nil {
		return nil, errors.New("wbxml: empty document")
	}
	return root, nil
}

type decoder struct {
	data     []byte
	pos      int
	page     int
	strTable []byte
	lookup   Lookup
}

func (d *decoder) element() (*Node, error) {
	if d.pos >= len(d.data) {
		return nil, io.ErrUnexpectedEOF
	}
	b := d.data[d.pos]
	d.pos++
	hasAttrs := b&0x80 != 0
	hasContent := b&0x40 != 0
	tok := b & 0x3F

	name, _ := d.lookup.Name(d.page, tok)
	if name == "" {
		name = fmt.Sprintf("x%02X", tok)
	}
	n := &Node{Page: d.page, Name: name, Token: tok}
	if hasAttrs {
		if err := d.skipAttrs(); err != nil {
			return nil, err
		}
	}
	if !hasContent {
		return n, nil
	}
	for {
		if d.pos >= len(d.data) {
			return nil, io.ErrUnexpectedEOF
		}
		b := d.data[d.pos]
		switch {
		case b == 0x01:
			d.pos++
			return n, nil
		case b == 0x00:
			d.pos++
			p, err := d.mb()
			if err != nil {
				return nil, err
			}
			d.page = int(p)
		case b == 0x03:
			d.pos++
			s, err := d.inlineString()
			if err != nil {
				return nil, err
			}
			n.Text += s
		case b == 0xC3:
			d.pos++
			l, err := d.mb()
			if err != nil {
				return nil, err
			}
			if int(l) > len(d.data)-d.pos {
				return nil, io.ErrUnexpectedEOF
			}
			n.Text += string(d.data[d.pos : d.pos+int(l)])
			d.pos += int(l)
			n.Binary = true
		case b&0x40 == 0 && b&0x80 == 0 && b < 0x05:
			// Stray control byte, ignore.
			d.pos++
		case b == 0x83:
			// STR_T: string table reference.
			d.pos++
			off, err := d.mb()
			if err != nil {
				return nil, err
			}
			n.Text += d.strAt(off)
		case b < 0x40 && !d.known(b):
			// String table reference.
			off, err := d.mb()
			if err != nil {
				return nil, err
			}
			n.Text += d.strAt(off)
		default:
			child, err := d.element()
			if err != nil {
				return nil, err
			}
			n.Children = append(n.Children, child)
		}
	}
}

func (d *decoder) known(tok byte) bool {
	_, ok := d.lookup.Name(d.page, tok)
	return ok
}

func (d *decoder) skipAttrs() error {
	for {
		if d.pos >= len(d.data) {
			return io.ErrUnexpectedEOF
		}
		b := d.data[d.pos]
		d.pos++
		if b == 0x01 {
			return nil
		}
		if d.pos >= len(d.data) {
			return io.ErrUnexpectedEOF
		}
		switch v := d.data[d.pos]; {
		case v == 0x03:
			d.pos++
			if _, err := d.inlineString(); err != nil {
				return err
			}
		case v == 0xC3:
			d.pos++
			l, err := d.mb()
			if err != nil {
				return err
			}
			d.pos += int(l)
		default:
			if _, err := d.mb(); err != nil {
				return err
			}
		}
	}
}

func (d *decoder) mb() (uint32, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		if d.pos >= len(d.data) {
			return 0, io.ErrUnexpectedEOF
		}
		b := d.data[d.pos]
		d.pos++
		v = v<<7 | uint32(b&0x7F)
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, errors.New("wbxml: multi-byte integer overflow")
}

func (d *decoder) inlineString() (string, error) {
	start := d.pos
	for d.pos < len(d.data) && d.data[d.pos] != 0x00 {
		d.pos++
	}
	if d.pos >= len(d.data) {
		return "", io.ErrUnexpectedEOF
	}
	s := string(d.data[start:d.pos])
	d.pos++ // skip the NUL terminator
	return s, nil
}

func (d *decoder) strAt(off uint32) string {
	if int(off) >= len(d.strTable) {
		return ""
	}
	rest := d.strTable[off:]
	if i := strings.IndexByte(string(rest), 0); i >= 0 {
		return string(rest[:i])
	}
	return string(rest)
}

func writeMB(buf *bytes.Buffer, v uint32) {
	var tmp [5]byte
	i := len(tmp)
	for {
		i--
		tmp[i] = byte(v & 0x7F)
		v >>= 7
		if v == 0 {
			break
		}
	}
	for j := i; j < len(tmp); j++ {
		b := tmp[j]
		if j != len(tmp)-1 {
			b |= 0x80
		}
		buf.WriteByte(b)
	}
}
