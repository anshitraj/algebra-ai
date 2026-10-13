package pdf

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestDocumentIsAWellFormedPDF(t *testing.T) {
	d := &Document{Title: "Receipt (test) \\ é"}
	p := d.AddPage()
	p.Text(50, 800, HelveticaBold, 20, 0, "Algebra (receipt)")
	p.Text(50, 780, Courier, 8, 0, "3NqD8Aru...NxTSva5X")
	p.Rule(50, 770, 545, 770, 0.8)
	p.Link(50, 760, 200, 772, "https://explorer.solana.com/tx/abc?cluster=devnet")
	b := d.Bytes()

	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("header and trailer")
	}
	// Every xref entry points at the object it names.
	m := regexp.MustCompile(`startxref\n(\d+)\n`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no startxref")
	}
	at, _ := strconv.Atoi(string(m[1]))
	if !bytes.HasPrefix(b[at:], []byte("xref\n")) {
		t.Fatalf("startxref points at %q", b[at:at+10])
	}
	lines := strings.Split(string(b[at:]), "\n")
	n, _ := strconv.Atoi(strings.Fields(lines[1])[1])
	for i := 1; i < n; i++ {
		off, _ := strconv.Atoi(lines[2+i][:10])
		if want := fmt.Sprintf("%d 0 obj", i); !bytes.HasPrefix(b[off:], []byte(want)) {
			t.Fatalf("object %d is not at %d: %q", i, off, b[off:off+12])
		}
	}
	for _, want := range []string{`(Algebra \(receipt\))`, "/URI (https://explorer.solana.com/tx/abc?cluster=devnet)", "/BaseFont /Courier", `\\ \351`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestWrapCutsLongWords(t *testing.T) {
	sig := strings.Repeat("A", 300)
	lines := Wrap(Courier, 6, 120, sig)
	if len(lines) < 5 {
		t.Fatalf("a long signature wraps: %d lines", len(lines))
	}
	for _, l := range lines {
		if Width(Courier, 6, l) > 120 {
			t.Fatalf("line too wide: %d chars", len(l))
		}
	}
	if strings.Join(lines, "") != sig {
		t.Fatal("nothing is lost")
	}
}
