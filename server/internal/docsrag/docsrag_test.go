package docsrag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitKeepsHeadingsAndFences(t *testing.T) {
	md := "# Title\nIntro text that is long enough to keep as a passage on its own here.\n## Setup\nRun this:\n```\n# not a heading\nmake up\n```\nAfter the fence there is more text to make it long enough.\n"
	ps := Split("x.md", md)
	if len(ps) != 2 || ps[0].Heading != "Title" || ps[1].Heading != "Setup" || !strings.Contains(ps[1].Text, "# not a heading") {
		t.Fatalf("%+v", ps)
	}
}

func TestSplitLongSectionsStayBounded(t *testing.T) {
	md := "# Big\n" + strings.Repeat("A sentence of filler words about devices and alerts.\n\n", 200)
	for _, p := range Split("big.md", md) {
		if len(p.Text) > maxPassage {
			t.Fatalf("passage of %d bytes", len(p.Text))
		}
	}
}

func TestSearchRanksTheRightPassage(t *testing.T) {
	ix := New([]Passage{
		{Doc: "a.md", Heading: "Backups", Text: "Run scripts/backup.sh nightly. Restore with restore.sh and rehearse it."},
		{Doc: "b.md", Heading: "Alerts", Text: "An alert is acknowledged by an operator and resolved when the condition clears."},
		{Doc: "c.md", Heading: "MQTT", Text: "Devices publish telemetry over MQTT with TLS and per-device credentials."},
	})
	for q, want := range map[string]string{"how do I restore a backup": "a.md", "acknowledging alerts": "b.md", "mqtt credentials": "c.md"} {
		h := ix.Search(q, 3)
		if len(h) == 0 || h[0].Doc != want {
			t.Errorf("%q -> %+v, want %s first", q, h, want)
		}
	}
	if len(ix.Search("the and of", 3)) != 0 || len(ix.Search("zebra unicorn", 3)) != 0 {
		t.Error("stop words and unknown words must not match")
	}
}

func TestEmbeddedCorpusAnswersRealQuestions(t *testing.T) {
	ix, err := Embedded()
	if err != nil || ix.Size() < 100 {
		t.Fatalf("size %d err %v", ix.Size(), err)
	}
	for q, doc := range map[string]string{
		"how do I back up and restore the database":            "backup-restore.md",
		"how do I deploy the platform without internet access": "deployment.md",
		"what does the assistant refuse to do":                 "assistant.md",
		"configure single sign-on with SAML":                   "sso-saml.md",
		"what is the AI runtime memory use":                    "ai-runtime.md",
		"how do I create a site":                               "ui-guide.md",
		"can I set a colour for a site":                        "ui-guide.md",
		"how do I add a user or invite someone":                "ui-guide.md",
		"how do I create a customer and scope a user":          "ui-guide.md",
		"is an asset the same as a customer":                   "ui-guide.md",
	} {
		h := ix.Search(q, 5)
		found := false
		for _, x := range h {
			if x.Doc == doc {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: %s not in top 5: %+v", q, doc, h)
		}
	}
}

// The embedded corpus is a copy of docs/. A stale copy would make the assistant quote old docs.
func TestCorpusMatchesDocs(t *testing.T) {
	docs, err := filepath.Glob("../../../docs/*.md")
	if err != nil || len(docs) == 0 {
		t.Skip("docs/ not available from this directory")
	}
	for _, d := range docs {
		want, _ := os.ReadFile(d)
		got, err := os.ReadFile(filepath.Join("corpus", filepath.Base(d)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s is stale or missing in the corpus: run scripts/sync-ai-docs.sh", filepath.Base(d))
		}
	}
}
