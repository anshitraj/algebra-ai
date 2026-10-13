// Package pdf writes simple, single-purpose PDF documents: pages of text in
// the three standard fonts every PDF reader has (Helvetica, Helvetica-Bold and
// Courier), thin rules, and clickable links. No dependency and no embedded
// fonts, so the output is small and the same on every machine.
//
// Text is written in WinAnsi (Latin-1); characters outside it become "?".
package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

// Font is one of the standard fonts.
type Font int

const (
	Helvetica Font = iota
	HelveticaBold
	Courier
)

var fontNames = [...]string{"Helvetica", "Helvetica-Bold", "Courier"}

// A4 in points.
const (
	PageWidth  = 595.28
	PageHeight = 841.89
)

type text struct {
	x, y float64
	size float64
	font Font
	gray float64
	s    string
}

type rule struct{ x1, y1, x2, y2, gray float64 }

type link struct {
	x1, y1, x2, y2 float64
	uri            string
}

// Page is one page; coordinates are points from the bottom-left corner.
type Page struct {
	texts []text
	rules []rule
	links []link
}

// Document is a PDF being built.
type Document struct {
	Title string
	pages []*Page
}

// AddPage starts a new page.
func (d *Document) AddPage() *Page {
	p := &Page{}
	d.pages = append(d.pages, p)
	return p
}

// Text writes s at (x, y) in font and size; gray is 0 (black) to 1 (white).
func (p *Page) Text(x, y float64, f Font, size, gray float64, s string) {
	p.texts = append(p.texts, text{x, y, size, f, gray, s})
}

// Rule draws a thin line.
func (p *Page) Rule(x1, y1, x2, y2, gray float64) {
	p.rules = append(p.rules, rule{x1, y1, x2, y2, gray})
}

// Link makes the rectangle (x1, y1)-(x2, y2) open uri when clicked.
func (p *Page) Link(x1, y1, x2, y2 float64, uri string) {
	p.links = append(p.links, link{x1, y1, x2, y2, uri})
}

// Width estimates how wide s is in font and size. Courier is exact (every
// glyph is 0.6 em); Helvetica uses an average, which is enough for wrapping.
func Width(f Font, size float64, s string) float64 {
	n := float64(len([]rune(s)))
	switch f {
	case Courier:
		return n * 0.6 * size
	case HelveticaBold:
		return n * 0.56 * size
	}
	return n * 0.52 * size
}

// Wrap breaks s into lines no wider than width. Words longer than a line are
// cut, so a signature or a hash wraps too.
func Wrap(f Font, size, width float64, s string) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			for Width(f, size, w) > width {
				cut := max(1, int(width/Width(f, size, "M")))
				r := []rune(w)
				if cut >= len(r) {
					break
				}
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, string(r[:cut]))
				w = string(r[cut:])
			}
			switch {
			case line == "":
				line = w
			case Width(f, size, line+" "+w) <= width:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		out = append(out, line)
	}
	return out
}

// escape writes s as a PDF literal string in WinAnsi.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\' || r == '(' || r == ')':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '’' || r == '‘':
			b.WriteByte('\'')
		case r == '“' || r == '”':
			b.WriteByte('"')
		case r == '–' || r == '—':
			b.WriteByte('-')
		case r == '·':
			b.WriteString("\\267")
		case r >= 32 && r < 127:
			b.WriteRune(r)
		case r >= 160 && r <= 255:
			fmt.Fprintf(&b, "\\%03o", r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

func num(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// Bytes renders the document.
func (d *Document) Bytes() []byte {
	if len(d.pages) == 0 {
		d.AddPage()
	}
	var objs []string // object bodies; object n is objs[n-1]
	add := func(body string) int {
		objs = append(objs, body)
		return len(objs)
	}
	catalog := add("") // filled in below
	pagesObj := add("")
	fontObjs := make([]int, len(fontNames))
	for i, name := range fontNames {
		fontObjs[i] = add(fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding /WinAnsiEncoding >>", name))
	}
	fonts := ""
	for i, o := range fontObjs {
		fonts += fmt.Sprintf("/F%d %d 0 R ", i+1, o)
	}
	var kids []string
	for _, p := range d.pages {
		var c bytes.Buffer
		for _, r := range p.rules {
			fmt.Fprintf(&c, "%s G 0.6 w %s %s m %s %s l S\n", num(r.gray), num(r.x1), num(r.y1), num(r.x2), num(r.y2))
		}
		for _, t := range p.texts {
			fmt.Fprintf(&c, "BT %s g /F%d %s Tf %s %s Td (%s) Tj ET\n", num(t.gray), int(t.font)+1, num(t.size), num(t.x), num(t.y), escape(t.s))
		}
		content := add(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", c.Len(), c.String()))
		var annots []string
		for _, l := range p.links {
			annots = append(annots, fmt.Sprintf("%d 0 R", add(fmt.Sprintf("<< /Type /Annot /Subtype /Link /Rect [%s %s %s %s] /Border [0 0 0] /A << /S /URI /URI (%s) >> >>",
				num(l.x1), num(l.y1), num(l.x2), num(l.y2), escape(l.uri)))))
		}
		page := fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources << /Font << %s>> >> /Contents %d 0 R",
			pagesObj, num(PageWidth), num(PageHeight), fonts, content)
		if len(annots) > 0 {
			page += " /Annots [" + strings.Join(annots, " ") + "]"
		}
		kids = append(kids, fmt.Sprintf("%d 0 R", add(page+" >>")))
	}
	info := add(fmt.Sprintf("<< /Title (%s) /Producer (Algebra) >>", escape(d.Title)))
	objs[catalog-1] = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj)
	objs[pagesObj-1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, body := range objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, catalog, info, xref)
	return out.Bytes()
}
