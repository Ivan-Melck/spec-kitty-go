package prereview

import (
	"regexp"
	"sort"
	"strings"
)

// ShlexValid reports whether Python shlex.split(s) (posix mode) succeeds:
// no unbalanced quotes and no trailing escape.
func ShlexValid(s string) bool {
	inSingle, inDouble, escaped := false, false, false
	for _, c := range s {
		switch {
		case escaped:
			escaped = false
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case c == '\\':
			escaped = true
		case inDouble:
			if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		}
	}
	return !inSingle && !inDouble && !escaped
}

var nonIdent = regexp.MustCompile(`[^A-Za-z0-9_]`)

// substitutionEnvName is configured_command._substitution_env_name.
func substitutionEnvName(key string) string {
	return "SPEC_KITTY_CMD_" + strings.ToUpper(nonIdent.ReplaceAllString(key, "_"))
}

// formatShellTemplateWithEnv is _format_shell_template_with_env: every
// {key} placeholder becomes a quote-aware reference to an exported env var.
func formatShellTemplateWithEnv(template string, subs map[string]string) (string, map[string]string) {
	keys := make([]string, 0, len(subs))
	for k := range subs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := map[string]string{}
	for _, k := range keys {
		env[substitutionEnvName(k)] = subs[k]
	}
	var b strings.Builder
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(template); {
		matched := ""
		for _, k := range keys {
			if strings.HasPrefix(template[i:], "{"+k+"}") {
				matched = k
				break
			}
		}
		if matched != "" {
			name := substitutionEnvName(matched)
			switch {
			case inSingle:
				b.WriteString(`'"${` + name + `}"'`)
			case inDouble:
				b.WriteString("${" + name + "}")
			default:
				b.WriteString(`"${` + name + `}"`)
			}
			i += len(matched) + 2
			escaped = false
			continue
		}
		c := template[i]
		b.WriteByte(c)
		switch {
		case escaped:
			escaped = false
		case c == '\\' && !inSingle:
			escaped = true
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		}
		i++
	}
	return b.String(), env
}

// ShellQuote is Python shlex.quote.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("@%+=:,./-_", c)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// BuildShellCommand is build_shell_command_with_substitutions: the
// template wrapped in `sh -c`, with substitutions exported first.
func BuildShellCommand(template string, subs map[string]string) []string {
	raw := map[string]string{}
	for k, v := range subs {
		if strings.Contains(template, "{"+k+"}") {
			raw[k] = v
		}
	}
	if len(raw) == 0 {
		return []string{"sh", "-c", template}
	}
	cmd, env := formatShellTemplateWithEnv(template, raw)
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	var exports []string
	for _, n := range names {
		exports = append(exports, "export "+n+"="+ShellQuote(env[n]))
	}
	return []string{"sh", "-c", strings.Join(exports, "; ") + "; " + cmd}
}
