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
