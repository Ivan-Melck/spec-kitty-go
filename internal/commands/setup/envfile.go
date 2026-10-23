package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

// Upstream seeds the two-tier operator env file (.kitty.env) into the
// process environment as the first thing `import specify_cli` does
// (bootstrap.env_file.load_operator_env_file): home tier (config.yaml
// env_file pointer, default ${SPEC_KITTY_HOME}/.kitty.env) merged under the
// repo tier (<repo>/.kittify/.kitty.env), then setdefault'ed so real
// environment values always win. A SPEC_KITTY_HOME line is never honoured.
func init() { LoadOperatorEnvFile("") }

// envWarningSite stands in for the wheel's specify_cli/__init__.py, where
// the import-time loader runs (the <spec-kitty> display root).
const envWarningSite = "<spec-kitty>/specify_cli/__init__.py"

// LoadOperatorEnvFile is bootstrap.env_file.load_operator_env_file.
func LoadOperatorEnvFile(start string) {
	if start == "" {
		start, _ = os.Getwd()
	}
	repo := ""
	for cur := start; cur != ""; {
		if st, err := os.Stat(filepath.Join(cur, ".kittify")); err == nil && st.IsDir() {
			repo = cur
			break
		}
		if _, err := os.Lstat(filepath.Join(cur, ".git")); err == nil {
			repo = cur
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	readTier := func(p string) map[string]string {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		vals := ParseEnvFile(string(b))
		if _, ok := vals["SPEC_KITTY_HOME"]; ok {
			delete(vals, "SPEC_KITTY_HOME")
			// warnings.warn(..., UserWarning, stacklevel=3): reported at the
			// `import specify_cli` call site with its source line.
			fmt.Fprintf(os.Stderr, "%s:33: UserWarning: %s defines SPEC_KITTY_HOME; ignoring that line (the locator that finds this file cannot be redefined by it).\n  load_operator_env_file()\n",
				envWarningSite, p)
		}
		return vals
	}
	merged := map[string]string{}
	homePath, _ := homeEnvPath(repo)
	for k, v := range readTier(homePath) {
		merged[k] = v
	}
	if repo != "" {
		for k, v := range readTier(filepath.Join(repo, ".kittify", ".kitty.env")) {
			merged[k] = v
		}
	}
	for k, v := range merged {
		if _, ok := os.LookupEnv(k); !ok {
			os.Setenv(k, v)
		}
	}
}
