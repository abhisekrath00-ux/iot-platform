// Package docsrag is the assistant's local documentation search. It splits the platform's own
// markdown docs (embedded at build time, so it works air-gapped) into heading-sized passages and
// ranks them with BM25. There are no embeddings and no external calls; the index is built in
// memory at start-up in milliseconds. Results always carry the document and heading they came
// from, so an answer can cite its source. The corpus is product documentation, not tenant data.
package docsrag

import (
	"embed"
	"math"
	"sort"
	"strings"
	"unicode"
)

//go:embed corpus/*.md
var corpusFS embed.FS

type Passage struct {
	Doc     string `json:"doc"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
	tf      map[string]int
	n       int
}

type Index struct {
	passages []Passage
	df       map[string]int
	avgLen   float64
}

type Hit struct {
	Doc     string  `json:"doc"`
	Heading string  `json:"heading"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

const maxPassage = 1400

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an and are as at be by for from how i in is it of on or that the this to was what when where which with you your do does can should my me we our not no") {
		stop[w] = true
	}
}

// synonyms maps how people ask to the words the docs use. Query side only.
var synonyms = map[string]string{
	"gpu": "cpu model", "recovery time": "rto", "recovery point": "rpo", "service level objective": "slo",
	"service level objectives": "slo", "offline": "air gapped", "no internet": "air gapped",
	"password": "credentials login", "restore": "restore backup", "sign in": "login sso",
}

func expand(q string) string {
	l := strings.ToLower(q)
	for k, v := range synonyms {
		if strings.Contains(l, k) {
			q += " " + v
		}
	}
	return q
}

func tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
		if len(w) < 2 || stop[w] {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

// stem is a light suffix stripper: enough to match "alerts"/"alert" and "acknowledging"/"acknowledge".
func stem(w string) string {
	for _, suf := range []string{"ings", "ing", "ies", "es", "ed", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			if suf == "ies" {
				return w[:len(w)-3] + "y"
			}
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

// Split cuts one markdown document into passages at headings, and again at paragraph breaks when a
// section is long. Code fences are kept intact.
func Split(doc, md string) []Passage {
	var out []Passage
	heading := strings.TrimSuffix(doc, ".md")
	var buf []string
	inFence := false
	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = nil
		for len(text) > maxPassage {
			cut := strings.LastIndex(text[:maxPassage], "\n\n")
			if cut < maxPassage/3 {
				cut = maxPassage
			}
			out = append(out, Passage{Doc: doc, Heading: heading, Text: strings.TrimSpace(text[:cut])})
			text = strings.TrimSpace(text[cut:])
		}
		if len(text) > 40 {
			out = append(out, Passage{Doc: doc, Heading: heading, Text: text})
		}
	}
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(l, "#") {
			flush()
			heading = strings.TrimSpace(strings.TrimLeft(l, "#"))
			continue
		}
		buf = append(buf, l)
	}
	flush()
	return out
}

func New(passages []Passage) *Index {
	ix := &Index{df: map[string]int{}}
	total := 0
	for _, p := range passages {
		p.tf = map[string]int{}
		docWords := strings.NewReplacer("-", " ", "_", " ").Replace(strings.TrimSuffix(p.Doc, ".md"))
		for _, t := range tokens(p.Heading + " " + p.Heading + " " + docWords + " " + p.Text) { // heading and document-name words count double
			p.tf[t]++
			p.n++
		}
		for t := range p.tf {
			ix.df[t]++
		}
		total += p.n
		ix.passages = append(ix.passages, p)
	}
	if len(ix.passages) > 0 {
		ix.avgLen = float64(total) / float64(len(ix.passages))
	}
	return ix
}

// Embedded builds the index from the docs compiled into the binary.
func Embedded() (*Index, error) {
	ents, err := corpusFS.ReadDir("corpus")
	if err != nil {
		return nil, err
	}
	var ps []Passage
	for _, e := range ents {
		b, err := corpusFS.ReadFile("corpus/" + e.Name())
		if err != nil {
			return nil, err
		}
		ps = append(ps, Split(e.Name(), string(b))...)
	}
	return New(ps), nil
}

func (ix *Index) Size() int { return len(ix.passages) }

// Search returns the best passages for a question (BM25, k1=1.4, b=0.75).
func (ix *Index) Search(q string, k int) []Hit {
	qt := tokens(expand(q))
	if k <= 0 || len(qt) == 0 || len(ix.passages) == 0 {
		return nil
	}
	n := float64(len(ix.passages))
	type sc struct {
		i int
		s float64
	}
	var scored []sc
	for i, p := range ix.passages {
		s := 0.0
		for _, t := range qt {
			f := float64(p.tf[t])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(ix.df[t])+0.5)/(float64(ix.df[t])+0.5))
			s += idf * f * 2.4 / (f + 1.4*(0.25+0.75*float64(p.n)/ix.avgLen))
		}
		if s > 0 {
			scored = append(scored, sc{i, s})
		}
	}
	sort.Slice(scored, func(a, b int) bool { return scored[a].s > scored[b].s })
	if k > len(scored) {
		k = len(scored)
	}
	var out []Hit
	for _, x := range scored[:k] {
		p := ix.passages[x.i]
		sn := p.Text
		if len(sn) > 700 {
			sn = sn[:700] + "..."
		}
		out = append(out, Hit{Doc: p.Doc, Heading: p.Heading, Snippet: sn, Score: math.Round(x.s*100) / 100})
	}
	return out
}
