package markdown

import (
	"fmt"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// show renders the annotations inline as [kind:text] for readable cases.
func show(r *otogi.RichText) string {
	var lines []string
	for _, a := range r.Annotations() {
		target := ""
		if a.Target != "" {
			target = "=" + a.Target
		}
		lines = append(lines, fmt.Sprintf("%s%s:%q", a.Kind, target, r.String()[a.Offset:a.Offset+a.Length]))
	}
	return r.String() + "\n--\n" + strings.Join(lines, "\n")
}

func TestRender(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"inline", "Some *it* and **bold**, ~~gone~~, `x := 1` and \\*literal\\* &amp; [docs](https://d.example/a).",
			"Some it and bold, gone, x := 1 and *literal* & docs.\n--\n" +
				`italic:"it"` + "\n" + `bold:"bold"` + "\n" + `strike:"gone"` + "\n" + `code:"x := 1"` + "\n" + `link=https://d.example/a:"docs"`},
		{"lines and blocks", "first line\nsecond line\n\n# Title\n\n> quoted **b**\n\n```go\nfmt.Println()\n```",
			"first line\nsecond line\n\nTitle\n\nquoted b\n\nfmt.Println()\n--\n" +
				`bold:"Title"` + "\n" + `bold:"b"` + "\n" + `block_quote:"quoted b"` + "\n" + `preformatted=go:"fmt.Println()"`},
		{"lists", "- a\n- b\n  1. one\n  2. two\n\n3. three",
			"• a\n• b\n  1. one\n  2. two\n\n3. three\n--\n"},
		{"links", "see https://x.example/p and <mail@x.example>, ![logo](https://x.example/l.png)",
			"see https://x.example/p and mail@x.example, logo\n--\n" +
				`link=https://x.example/p:"https://x.example/p"` + "\n" + `link=mailto:mail@x.example:"mail@x.example"` + "\n" + `link=https://x.example/l.png:"logo"`},
		{"raw html", "a <b>b</b>\n\n<div>\nblock\n</div>", "a <b>b</b>\n\n<div>\nblock\n</div>\n--\n"},
		{"unicode offsets", "日本 **語**", "日本 語\n--\n" + `bold:"語"`},
		{"table", "Before\n\n| Name | Score | Note |\n| :-- | --: | :-: |\n| **Alice** | 3 | [docs](https://d.example) |\n| 日本 | 12 | x |\n\nAfter",
			"Before\n\nName  | Score |           Note\n------+-------+-------------------------\nAlice |     3 | docs (https://d.example)\n日本  |    12 |            x\n\nAfter\n--\n" +
				`preformatted:"Name  | Score |           Note\n------+-------+-------------------------\nAlice |     3 | docs (https://d.example)\n日本  |    12 |            x"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := show(Render(c.in)); got != c.want {
				t.Errorf("Render(%q):\n%s\nwant:\n%s", c.in, got, c.want)
			}
		})
	}
}
