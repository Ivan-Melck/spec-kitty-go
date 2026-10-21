package missionstate

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	secretFlagNames = map[string]bool{
		"--token": true, "--auth": true, "--password": true, "--secret": true, "--api-key": true, "--bearer": true,
		"--access-token": true, "--refresh-token": true, "--id-token": true, "--client-secret": true,
		"--private-key": true, "--ssh-key": true, "--aws-secret-access-key": true, "--gh-token": true,
	}
	githubTokenRe  = regexp.MustCompile(`^gh[pousr]_[A-Za-z0-9_]{34,}$`)
	jwtRe          = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}$`)
	bearerRe       = regexp.MustCompile(`(?i)^Bearer\s+\S{16,}$`)
	envVarSecretRe = regexp.MustCompile(`^([A-Z][A-Z0-9_]*(?:TOKEN|SECRET|KEY|PASSWORD|PASSPHRASE))=(.+)$`)
)

const redacted = "<redacted>"

func looksLikeSlackToken(v string) bool {
	ok := false
	for _, p := range []string{"xoxb-", "xoxp-", "xoxa-", "xoxr-", "xoxs-"} {
		if strings.HasPrefix(v, p) {
			ok = true
		}
	}
	if !ok {
		return false
	}
	parts := strings.Split(v, "-")
	if len(parts) < 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}

func looksLikeAuthorizationHeader(v string) bool {
	name, rest, ok := strings.Cut(v, ":")
	return ok && strings.ToLower(strings.TrimSpace(name)) == "authorization" && strings.TrimSpace(rest) != ""
}

func standaloneSecret(v string) bool {
	return githubTokenRe.MatchString(v) || looksLikeSlackToken(v) || jwtRe.MatchString(v) ||
		looksLikeAuthorizationHeader(v) || bearerRe.MatchString(v)
}

// ScrubSecretArgs is _scrub_secret_args: argv with secret-bearing values
// replaced by "<redacted>".
func ScrubSecretArgs(argv []string) []string {
	out := []string{}
	for i := 0; i < len(argv); i++ {
		item := argv[i]
		if strings.Contains(item, "=") && strings.HasPrefix(item, "--") {
			flag, _, _ := strings.Cut(item, "=")
			if secretFlagNames[strings.ToLower(flag)] {
				out = append(out, flag+"="+redacted)
				continue
			}
		}
		if strings.HasPrefix(item, "--") && secretFlagNames[strings.ToLower(item)] && i+1 < len(argv) {
			out = append(out, item, redacted)
			i++
			continue
		}
		if m := envVarSecretRe.FindStringSubmatch(item); m != nil {
			out = append(out, m[1]+"="+redacted)
			continue
		}
		if standaloneSecret(item) {
			out = append(out, redacted)
			continue
		}
		out = append(out, item)
	}
	return out
}
