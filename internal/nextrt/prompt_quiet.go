package nextrt

import (
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// decision._build_prompt_or_error runs build_prompt under
// contextlib.redirect_stdout/redirect_stderr: the log records the prompt
// build emits (charter bundle freshness / auto-sync warnings, slug-resolver
// warnings) reach the redirected stream, not the terminal.
func quietPromptBuild(fn func() (string, error)) (p string, err error) {
	wl, pwl := charter.WarnLog, charter.PyWarnLog
	packLog := assets.PackWarnLog
	assets.PackWarnLog = nil
	charter.WarnLog, charter.PyWarnLog = nil, nil
	defer func() {
		charter.WarnLog, charter.PyWarnLog = wl, pwl
		assets.PackWarnLog = packLog
	}()
	status.Quietly(func() { p, err = fn() })
	return p, err
}

// buildTemplatePrompt is _build_template_prompt under the redirect.
func buildTemplatePrompt(m *missionCtx, featureDir, action, agent string) (string, error) {
	return quietPromptBuild(func() (string, error) { return buildTemplatePromptLoud(m, featureDir, action, agent) })
}

// buildWPPrompt is _build_wp_prompt under the redirect.
func buildWPPrompt(m *missionCtx, action, wpID, agent string) (string, error) {
	return quietPromptBuild(func() (string, error) { return buildWPPromptLoud(m, action, wpID, agent) })
}
