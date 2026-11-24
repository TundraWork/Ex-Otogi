package twitter

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Translate configures the translated cards.
type Translate struct {
	Provider string `json:"provider" minLength:"1" format:"llm-provider" doc:"A language model provider set in the panel under Language models, such as gemini."`
	Model    string `json:"model" minLength:"1" format:"llm-model" doc:"The provider's model ID."`
	Thinking string `json:"thinking,omitempty" enum:"minimal,low,medium,high" format:"llm-thinking" doc:"How much the model thinks before answering; unset is the model's default."`
}

// translator asks a language model what language a text is in and, unless
// it is Chinese, for the text in Simplified Chinese.
type translator struct {
	cfg      Translate
	provider llm.Provider
}

// translation is a post in Simplified Chinese.
type translation struct {
	Language    string // the original's, named in Chinese: 英语, 日语
	Post, Quote string
	Parent      string // the post replied to
	ParentQuote string // the post that one quotes
	// Echoed names the parts, comma-separated, the model gave back as
	// they came instead of translating: they have no translation.
	Echoed string
}

const translatePrompt = `You translate a post from X into Simplified Chinese.

The input is the post's text. Reply in exactly this form and nothing else:
<language>the post's language, named in Simplified Chinese, such as 英语 or 日语</language>
<chinese>the post in natural Simplified Chinese</chinese>

When the post is already written in Chinese, Simplified or Traditional, reply with only: <language>中文</language>

Keep URLs, @mentions, #hashtags, emoji, and line breaks exactly as they are. Add no notes or explanations.`

var (
	taggedLanguage = regexp.MustCompile(`(?s)<language>(.*?)</language>`)
	taggedChinese  = regexp.MustCompile(`(?s)<chinese>(.*?)</chinese>`)
)

// translate returns what of the post, its quote, the post it replies to,
// and that post's quote is not in Chinese, in Chinese: false when all of it
// is in Chinese already, or none of them has text; true also when the
// model gave a part back untranslated, to have that recorded. Each part is
// its own request: asked for parts in different languages at once,
// gemini-3.5-flash-lite gave one back as it came about one time in three.
func (tr *translator) translate(ctx context.Context, t tweet) (translation, bool, error) {
	var out translation
	var echoed []string
	for _, p := range []struct {
		name string
		of   *tweet
		into *string
	}{{"post", &t, &out.Post}, {"quote", t.Quote, &out.Quote}, {"parent", t.Parent, &out.Parent}, {"parent_quote", parentQuote(t), &out.ParentQuote}} {
		if p.of == nil || strings.TrimSpace(p.of.Text) == "" {
			continue
		}
		language, text, err := tr.one(ctx, p.of.Text)
		if err != nil {
			return translation{}, false, err
		}
		if p.name == "post" {
			out.Language = language
		}
		if text == strings.TrimSpace(p.of.Text) {
			echoed = append(echoed, p.name)
			continue
		}
		*p.into = text
	}
	out.Echoed = strings.Join(echoed, ",")
	return out, out != translation{Language: out.Language}, nil
}

// one returns the language of text and, unless that is Chinese, text in
// Chinese.
func (tr *translator) one(ctx context.Context, text string) (language, chinese string, err error) {
	resp, err := tr.provider.Generate(ctx, llm.Request{
		Model: tr.cfg.Model, System: translatePrompt, Thinking: tr.cfg.Thinking,
		Turns: []llm.Turn{{Role: llm.RoleUser, Content: otogi.TextMessage(text)}},
	}, nil)
	if err != nil {
		return "", "", fmt.Errorf("twitter: translate: %w", err)
	}
	find := func(re *regexp.Regexp) string {
		if m := re.FindStringSubmatch(resp.Markdown); m != nil {
			return strings.TrimSpace(m[1])
		}
		return ""
	}
	language, chinese = find(taggedLanguage), find(taggedChinese)
	switch {
	case language == "中文":
		return language, "", nil // a post in Chinese has no translation, whatever the model sent
	case language == "" || chinese == "":
		return "", "", fmt.Errorf("twitter: translate: the model replied out of form: %q", resp.Markdown)
	}
	return language, chinese, nil
}

// parentQuote is the post that the post t replies to quotes.
func parentQuote(t tweet) *tweet {
	if t.Parent == nil {
		return nil
	}
	return t.Parent.Quote
}
