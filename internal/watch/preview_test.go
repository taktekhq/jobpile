package watch

import (
	"testing"

	imap "github.com/emersion/go-imap/v2"
)

func TestFindTextPartSingleTextPlainRoot(t *testing.T) {
	root := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"}
	path, sp, ok := FindTextPart(root)
	if !ok {
		t.Fatal("FindTextPart: want a match for a bare text/plain message, got none")
	}
	if sp.Subtype != "plain" {
		t.Errorf("subtype = %q, want plain", sp.Subtype)
	}
	if len(path) == 0 {
		t.Error("path must not be empty for a single-part message")
	}
}

func TestFindTextPartPrefersPlainOverHTML(t *testing.T) {
	plain := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"}
	html := &imap.BodyStructureSinglePart{Type: "text", Subtype: "html"}
	root := &imap.BodyStructureMultiPart{Subtype: "alternative", Children: []imap.BodyStructure{plain, html}}

	_, sp, ok := FindTextPart(root)
	if !ok {
		t.Fatal("FindTextPart: want a match, got none")
	}
	if sp.Subtype != "plain" {
		t.Errorf("subtype = %q, want plain (plain must win over html)", sp.Subtype)
	}
}

func TestFindTextPartFallsBackToHTML(t *testing.T) {
	html := &imap.BodyStructureSinglePart{Type: "text", Subtype: "html"}
	attachment := &imap.BodyStructureSinglePart{Type: "application", Subtype: "pdf"}
	root := &imap.BodyStructureMultiPart{Subtype: "mixed", Children: []imap.BodyStructure{html, attachment}}

	_, sp, ok := FindTextPart(root)
	if !ok {
		t.Fatal("FindTextPart: want a match, got none")
	}
	if sp.Subtype != "html" {
		t.Errorf("subtype = %q, want html", sp.Subtype)
	}
}

func TestFindTextPartNoTextPart(t *testing.T) {
	attachment := &imap.BodyStructureSinglePart{Type: "application", Subtype: "pdf"}
	_, _, ok := FindTextPart(attachment)
	if ok {
		t.Error("FindTextPart: want no match for an attachment-only message, got one")
	}
}

func TestFindTextPartNilBodyStructure(t *testing.T) {
	_, _, ok := FindTextPart(nil)
	if ok {
		t.Error("FindTextPart(nil): want no match, got one")
	}
}

func TestDecodePartPlain7Bit(t *testing.T) {
	sp := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "7BIT"}
	got := DecodePart(sp, []byte("Hi there,\nThanks for applying.\n"))
	want := "Hi there,\nThanks for applying.\n"
	if got != want {
		t.Errorf("DecodePart = %q, want %q", got, want)
	}
}

func TestDecodePartQuotedPrintable(t *testing.T) {
	sp := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "QUOTED-PRINTABLE", Params: map[string]string{"charset": "utf-8"}}
	got := DecodePart(sp, []byte("Caf=C3=A9 and na=C3=AFve text"))
	want := "Café and naïve text"
	if got != want {
		t.Errorf("DecodePart = %q, want %q", got, want)
	}
}

func TestDecodePartBase64(t *testing.T) {
	sp := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "BASE64"}
	// base64 of "Hello from base64\n"
	got := DecodePart(sp, []byte("SGVsbG8gZnJvbSBiYXNlNjQK"))
	want := "Hello from base64\n"
	if got != want {
		t.Errorf("DecodePart = %q, want %q", got, want)
	}
}

func TestDecodePartStripsHTML(t *testing.T) {
	sp := &imap.BodyStructureSinglePart{Type: "text", Subtype: "html", Encoding: "7BIT"}
	got := DecodePart(sp, []byte("<p>Hello <b>there</b></p>"))
	want := "Hello there"
	if got != want {
		t.Errorf("DecodePart = %q, want %q", got, want)
	}
}

func TestDecodePartNilSinglePart(t *testing.T) {
	got := DecodePart(nil, []byte("raw text"))
	if got != "raw text" {
		t.Errorf("DecodePart(nil, ...) = %q, want raw text", got)
	}
}
