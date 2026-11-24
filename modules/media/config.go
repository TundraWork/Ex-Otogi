package media

import (
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings.
type Config struct {
	Steps    []Step `json:"steps" minItems:"1" ordered:"true" doc:"The models that describe media, tried in order: when one fails, is blocked, answers out of format, or did not get the media, the next is tried. Configure at least two: Gemini's free tier often answers 503 when it is busy."`
	Language string `json:"language,omitempty" default:"English" minLength:"1" doc:"The language descriptions are written in, for example Chinese. Text on images is copied as it is."`
}

// Step is one model that describes media.
type Step struct {
	Provider string `json:"provider" minLength:"1" format:"llm-provider" doc:"A provider set in the panel under Language models, such as gemini. Use only models that see images and video: one that drops them silently, as deepseek-v4-pro does images, makes descriptions up."`
	Model    string `json:"model" minLength:"1" format:"llm-model" doc:"The provider's model ID, for example gemini-3.5-flash-lite."`
	Thinking string `json:"thinking,omitempty" enum:"minimal,low,medium,high" format:"llm-thinking" doc:"The thinking level; empty is the model's default. minimal is enough to describe media."`
}

// defaultLanguage is the language of descriptions when none is set.
const defaultLanguage = "English"

// step is a Step with its provider resolved.
type step struct {
	Step
	provider llm.Provider
}

// steps resolves each step's provider; an unknown provider fails, so the
// settings are refused.
func (c Config) steps(providers func(string) (llm.Provider, error)) ([]step, error) {
	if len(c.Steps) == 0 {
		return nil, &otogi.SettingError{Path: "steps", Err: errors.New("none configured")}
	}
	out := make([]step, len(c.Steps))
	var errs []error
	for i, s := range c.Steps {
		p, err := providers(s.Provider)
		if err != nil {
			errs = append(errs, &otogi.SettingError{Path: fmt.Sprintf("steps[%d].provider", i), Err: err})
		}
		out[i] = step{Step: s, provider: p}
	}
	return out, errors.Join(errs...)
}
