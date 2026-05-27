package main

import (
	"strings"
	"testing"
)

func TestWrapPlainTextDescription(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantChanged bool
		wantOut     string
	}{
		{"empty stays empty", "", false, ""},
		{"already-html is untouched", "<p>Hello</p>", false, "<p>Hello</p>"},
		{"single line wraps in p", "Hello world", true, "<p>Hello world</p>"},
		{"two paragraphs", "First.\n\nSecond.", true, "<p>First.</p><p>Second.</p>"},
		{"line break inside paragraph becomes br", "Line one\nLine two", true, "<p>Line one<br>Line two</p>"},
		{"plain text with html-unsafe chars is escaped", "5 < 10 & true", true, "<p>5 &lt; 10 &amp; true</p>"},
		{"trailing blank lines are ignored", "Hello\n\n", true, "<p>Hello</p>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := wrapPlainTextDescription(c.in)
			if changed != c.wantChanged {
				t.Errorf("changed = %v, want %v", changed, c.wantChanged)
			}
			if got != c.wantOut {
				t.Errorf("output = %q, want %q", got, c.wantOut)
			}
		})
	}
}

func TestSanitizeEventDescription(t *testing.T) {
	// Allowed Trix-output tags pass through unchanged.
	allowed := "<p>Hello <strong>world</strong> <em>now</em></p><ul><li>one</li></ul>"
	if got := sanitizeEventDescription(allowed); got != allowed {
		t.Errorf("allowed HTML was modified: got %q, want %q", got, allowed)
	}

	// Disallowed elements and attributes are stripped, but inner text is kept.
	dangerous := `<p>Hi<script>alert(1)</script> <a href="javascript:bad()">x</a></p>`
	got := sanitizeEventDescription(dangerous)
	if strings.Contains(got, "<script") {
		t.Errorf("script tag was not stripped: %q", got)
	}
	if strings.Contains(got, "javascript:") {
		t.Errorf("javascript: URL was not stripped: %q", got)
	}
	// The visible text "Hi" should still be there.
	if !strings.Contains(got, "Hi") {
		t.Errorf("expected 'Hi' to survive sanitization, got %q", got)
	}

	// Style attributes from a Word/Trix paste are stripped (no colors, no
	// alignment) — keeps the output clean and re-editable in Trix.
	styled := `<p style="color:red;text-align:center">Red text</p>`
	if strings.Contains(sanitizeEventDescription(styled), "style=") {
		t.Errorf("style attribute should be stripped: %q", sanitizeEventDescription(styled))
	}
}
