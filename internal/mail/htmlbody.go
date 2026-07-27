package mail

import (
	"html"
	"strings"
)

// TextToHTML renders a plain-text mail body as HTML that keeps the author's
// layout: a blank line starts a new paragraph, a single newline becomes a line
// break. Everything else is escaped, so text that happens to contain angle
// brackets stays literal.
//
// Why this exists: Graph's reply/replyAll `comment` is injected into the HTML
// body of the reply, and a reply draft handed back by createReply is an HTML
// message too. Plain text placed there renders as one run-on paragraph — the
// newlines are simply insignificant in HTML.
func TextToHTML(text string) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")

	var paragraphs []string
	for _, block := range strings.Split(normalized, "\n\n") {
		var lines []string
		for _, line := range strings.Split(block, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				lines = append(lines, html.EscapeString(trimmed))
			}
		}
		if len(lines) > 0 {
			paragraphs = append(paragraphs, "<p>"+strings.Join(lines, "<br>")+"</p>")
		}
	}
	return strings.Join(paragraphs, "")
}

// htmlOpeners are the tags a mail body realistically starts with when its
// author wrote HTML — full documents and bare fragments alike.
var htmlOpeners = []string{
	"<!doctype", "<html", "<body", "<div", "<p>", "<p ", "<table", "<span",
	"<font", "<ul", "<ol", "<br", "<h1", "<h2", "<h3",
}

// LooksLikeHTML reports whether a body was written as HTML. Only the opening
// of the body decides: prose never *starts* with a tag, so this cannot misfire
// on a mail that merely mentions one ("der Platzhalter <name> …").
//
// It is a safety net, not the interface — `--html` states the intent
// explicitly. But a body that opens with `<html><body>` has exactly one
// plausible reading, and sending it as text/plain shows the recipient the tags.
func LooksLikeHTML(body string) bool {
	head := strings.ToLower(strings.TrimSpace(body))
	for _, opener := range htmlOpeners {
		if strings.HasPrefix(head, opener) {
			return true
		}
	}
	return false
}

// Body is an outgoing mail body plus how its author wrote it. It travels
// through the backend seam unchanged, because whether plain text needs
// converting depends on the sink: Graph's reply path is HTML-only, EWS types
// its bodies explicitly and keeps plain text as plain text.
type Body struct {
	Content string
	HTML    bool
}

// IsHTML reports whether the content must be treated as HTML — either the
// author said so (--html) or the body visibly opens with a tag.
func (b Body) IsHTML() bool { return b.HTML || LooksLikeHTML(b.Content) }

// AsHTML returns the content ready for an HTML sink: verbatim when it already
// is HTML, converted from plain text otherwise.
func (b Body) AsHTML() string {
	if b.IsHTML() {
		return b.Content
	}
	return TextToHTML(b.Content)
}
