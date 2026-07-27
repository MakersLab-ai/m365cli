package mail

import "testing"

func TestTextToHTMLKeepsParagraphsAndLineBreaks(t *testing.T) {
	got := TextToHTML("Hallo Sandra,\n\nbitte um 12:30 zwei Menüs.\n\nMit freundlichen Grüßen\nAmadeus Falk")
	want := "<p>Hallo Sandra,</p><p>bitte um 12:30 zwei Menüs.</p><p>Mit freundlichen Grüßen<br>Amadeus Falk</p>"
	if got != want {
		t.Errorf("TextToHTML =\n%s\nwant\n%s", got, want)
	}
}

func TestTextToHTMLEscapesMarkup(t *testing.T) {
	got := TextToHTML(`Preis < 5 & "fair" <b>nicht fett</b>`)
	want := `<p>Preis &lt; 5 &amp; &#34;fair&#34; &lt;b&gt;nicht fett&lt;/b&gt;</p>`
	if got != want {
		t.Errorf("TextToHTML = %s, want %s", got, want)
	}
}

func TestTextToHTMLNormalisesCRLFAndBlankRuns(t *testing.T) {
	got := TextToHTML("eins\r\n\r\n\r\nzwei\r\n")
	want := "<p>eins</p><p>zwei</p>"
	if got != want {
		t.Errorf("TextToHTML = %s, want %s", got, want)
	}
}

func TestTextToHTMLEmptyStaysEmpty(t *testing.T) {
	if got := TextToHTML("   \n\n "); got != "" {
		t.Errorf("TextToHTML = %q, want empty", got)
	}
}

func TestLooksLikeHTMLDetectsWhatAgentsActuallyWrite(t *testing.T) {
	// All four mails that reached recipients as raw tags started this way.
	htmlBodies := []string{
		"<html><body>\n<p>Hallo Frau Pucher,</p>\n",
		"<!DOCTYPE html><html><body><p>Hi</p></body></html>",
		"<body><p>Hi</p></body>",
		"<p>Hallo Sandra,</p><p>danke dir.</p>",
		"  \n<div style=\"font-family: Arial\">Hallo</div>",
	}
	for _, b := range htmlBodies {
		if !LooksLikeHTML(b) {
			t.Errorf("LooksLikeHTML(%.30q) = false, want true", b)
		}
	}
}

func TestLooksLikeHTMLLeavesProseAlone(t *testing.T) {
	proseBodies := []string{
		"Hallo Sandra,\n\nbitte um 12:30 zwei Menüs.",
		"Preis < 5 Euro",
		"<3 Grüße",
		"",
		"Siehe Anhang -> Angebot",
		"Der Platzhalter <name> wird ersetzt.", // starts with prose, not a tag
	}
	for _, b := range proseBodies {
		if LooksLikeHTML(b) {
			t.Errorf("LooksLikeHTML(%.30q) = true, want false", b)
		}
	}
}

func TestTextToHTMLNotAppliedToBodiesThatAreAlreadyHTML(t *testing.T) {
	// asHTML is the single decision point: an HTML body must never be escaped,
	// with or without the --html flag.
	body := "<html><body><p>Hallo</p></body></html>"
	if got := asHTML(body, false); got != body {
		t.Errorf("asHTML auto-detect = %q, want verbatim", got)
	}
	if got := asHTML(body, true); got != body {
		t.Errorf("asHTML explicit = %q, want verbatim", got)
	}
}
