package architecture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCIUsernsWrapperRefusesMissingState(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "ci", "with-userns.py")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/python3", "-I", path, "--", "/bin/true")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err == nil {
		t.Fatal("missing prerequisite admitted")
	}
}

func TestCIUsernsWrapperDispatch(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ci/with-userns.py")
	if err != nil {
		t.Fatal(err)
	}
	code := `import os,sys,json,io,pathlib,types
from unittest.mock import patch
ns={'__name__':'fixture','__file__':str(pathlib.Path('scripts/ci/with-userns.py').resolve())};exec(sys.stdin.read(),ns)
env={'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'12','GITHUB_RUN_ATTEMPT':'1','GITHUB_JOB':'quality','GO_KITTY_USERNS_UID':str(os.getuid()),'GO_KITTY_USERNS_GID':str(os.getgid()),'GO_KITTY_USERNS_MODE':'direct','GO_KITTY_USERNS_PROFILE':''}
argv=['--','/usr/bin/make','quality-bounded','ARTIFACT_DIR=directory with spaces']
assert ns['command'](argv,env)==(argv[1:],False)
for key,value in [('GO_KITTY_USERNS_MODE',''),('GO_KITTY_USERNS_MODE','maybe'),('GO_KITTY_USERNS_PROFILE','other'),('GO_KITTY_USERNS_UID','0'),('RUNNER_ENVIRONMENT','self-hosted')]:
 bad=dict(env);bad[key]=value
 try:ns['command'](argv,bad)
 except ValueError:pass
 else:raise AssertionError('bad state admitted '+key)
name='go-kitty-ci-12-1-quality-'+'a'*32
env.update(GO_KITTY_USERNS_MODE='selected',GO_KITTY_USERNS_PROFILE=name)
with patch.object(pathlib.Path,'stat',return_value=types.SimpleNamespace(st_mode=0o100755,st_uid=0)),patch.object(os,'access',return_value=True):
 actual,selecting=ns['command'](argv,env)
assert selecting and actual[:6]==['/usr/bin/aa-exec','-i','-p',name,'--','/usr/bin/python3'] and actual[-len(argv):]==argv
ns['open']=lambda _:io.StringIO(name+' (unconfined)\n')
assert ns['command'](['__selected',*argv],env)==(argv[1:],False)
ns['open']=lambda _:io.StringIO('unconfined\n')
try:ns['command'](['__selected',*argv],env)
except ValueError:pass
else:raise AssertionError('label mismatch admitted')
for bad in [name+'/bad',name+'\n',name.replace('-12-','-13-')]:
 env['GO_KITTY_USERNS_PROFILE']=bad
 try:ns['command'](argv,env)
 except ValueError:pass
 else:raise AssertionError('profile injection admitted')
`
	cmd := exec.Command("/usr/bin/python3", "-I", "-c", code)
	cmd.Stdin = strings.NewReader(string(source))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper: %v\n%s", err, out)
	}
}

// This executes only the direct dispatch branch with a disposable successful
// state fixture; it is not kernel/profile admission evidence.
func TestCIUsernsWrapperPreservesOrdinaryExec(t *testing.T) {
	root := t.TempDir()
	evidence := filepath.Join(root, "evidence")
	if err := os.MkdirAll(filepath.Join(evidence, "userns-prerequisite"), 0700); err != nil {
		t.Fatal(err)
	}
	wrapper, err := filepath.Abs("../../scripts/ci/with-userns.py")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/python3", "-I", wrapper, "--", "/usr/bin/python3", "-I", "-c", "import os,sys; print(os.getcwd()); print(sys.argv[1]); print(os.environ['SENTINEL']); sys.exit(17)", "space ; $ literal")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GITHUB_ACTIONS=true", "RUNNER_ENVIRONMENT=github-hosted", "GITHUB_RUN_ID=12", "GITHUB_RUN_ATTEMPT=1", "GITHUB_JOB=quality", "GO_KITTY_USERNS_MODE=direct", "GO_KITTY_USERNS_PROFILE=", "GO_KITTY_USERNS_UID="+strconv.Itoa(os.Getuid()), "GO_KITTY_USERNS_GID="+strconv.Itoa(os.Getgid()), "ARTIFACT_DIR="+evidence, "SENTINEL=retained")
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 17 || string(out) != root+"\nspace ; $ literal\nretained\n" {
		t.Fatalf("exec preservation: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(evidence, "userns-prerequisite", "command-selection.json")); err != nil {
		t.Fatal(err)
	}
}
