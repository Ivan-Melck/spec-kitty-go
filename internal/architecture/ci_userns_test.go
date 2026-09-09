package architecture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func usernsHelper(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "prepare-userns.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCIUsernsHostedGuard(t *testing.T) {
	usernsHelper(t)
	cmd := exec.Command("/bin/bash", "../../scripts/ci/prepare-userns.sh")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GITHUB_ACTIONS=false"}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "github-hosted ordinary user required") {
		t.Fatalf("guard: %v %s", err, out)
	}
}

func TestCIUsernsWorkflowWiring(t *testing.T) {
	for _, name := range []string{"quality.yml", "full-campaign.yml", "hubnot-qualification.yml"} {
		b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Count(s, "run: bash scripts/ci/prepare-userns.sh") != 1 {
			t.Fatalf("%s missing single prerequisite", name)
		}
		if !strings.Contains(s, "if: always()") {
			t.Fatalf("%s loses diagnostics upload", name)
		}
	}
}

func TestCIUsernsDiagnosticOnlyOutcomes(t *testing.T) {
	source := usernsHelper(t)
	start := strings.Index(source, "import hashlib\n")
	end := strings.LastIndex(source, "\nPY\n")
	if start < 0 || end < start {
		t.Fatal("embedded helper unavailable")
	}
	harness := `
import tempfile
from unittest.mock import patch
code = sys.stdin.read()
ns = {'__name__':'test_fixture'}
exec(compile(code, 'prepare-userns.sh', 'exec'), ns)
# Fixture kernel data; never a kernel admission or policy administration test.
for outcome in ('pass','unknown-denial','missing-attachments','overlapping-attachments','bad-topology','bounded-probe'):
    with tempfile.TemporaryDirectory() as temp:
        artifact = pathlib.Path(temp) / 'evidence'; artifact.mkdir()
        env = {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'12','GITHUB_RUN_ATTEMPT':'1','GITHUB_JOB':'quality','RUNNER_TEMP':temp,'ARTIFACT_DIR':str(artifact)}
        calls = []
        def capture(argv):
            calls.append(argv)
            r = {'argv':argv,'stdout':'','stderr':'','exit':0,'bounded_stop':False}
            if '--map-root-user' in argv:
                assert argv[1:8] == ns['FLAGS']
                r['stdout'] = json.dumps({'pid':1,'uid':0,'gid':0,'namespaces':{'net':'net:[fixture]','pid':'pid:[fixture]','user':'user:[fixture]'},'uid_map':'0 %d 1'%os.getuid(),'gid_map':'0 %d 1'%os.getgid(),'setgroups':'deny','interfaces':' lo: 0\n' if outcome != 'bad-topology' else ' eth0: 0\n'})
                if outcome in ('unknown-denial','missing-attachments','overlapping-attachments'): r.update(exit=1,stderr='uid_map: Operation not permitted')
                if outcome == 'bounded-probe': r['bounded_stop']=True
            return r
        evidence = {'complete_nonconflict_proof':False, 'entries':[] if outcome != 'overlapping-attachments' else [{'attach':'/**'}]}
        with patch.dict(os.environ,env,clear=True), patch.dict(ns,{'capture':capture,'attachments':lambda:evidence}):
            status = ns['main']()
        root = artifact/'userns-prerequisite'
        result=json.loads((root/'result.json').read_text())
        assert (status == 0) == (outcome == 'pass'), result
        assert result['policy_changed'] is False and result['mode']=='diagnostic-only'
        assert (root/'probe.json').is_file() and (root/'audit.json').is_file()
        failed_probe = outcome in ('unknown-denial','missing-attachments','overlapping-attachments','bounded-probe')
        assert len(calls)==(5 if failed_probe else 3)
        assert all('apparmor_parser' not in str(c) for c in calls)
        if failed_probe:
            assert calls[3] == ['/usr/bin/sudo','-n','/usr/bin/python3','-I','-c',ns['PRIVILEGED_INVENTORY']]
            assert calls[4][:3] == ['/usr/bin/sudo','-n','/usr/bin/journalctl']
            assert (root/'privileged-inventory.json').is_file() and (root/'privileged-audit.json').is_file()
        else: assert all('sudo' not in str(c) for c in calls)
        before=(root/'result.json').read_bytes()
        with patch.dict(os.environ,env,clear=True): assert ns['main']()==1
        assert (root/'result.json').read_bytes()==before
# A caller-controlled artifact symlink is rejected before a probe or write.
with tempfile.TemporaryDirectory() as temp:
    root=pathlib.Path(temp); (root/'real').mkdir(); (root/'link').symlink_to(root/'real', target_is_directory=True)
    try: ns['safe_directory'](str(root/'link'))
    except ValueError: pass
    else: raise AssertionError('symlink admitted')
# Capture retains finite output and cancels overflow; no namespace or privilege needed.
r=ns['capture'](['/usr/bin/python3','-I','-c','import sys; sys.stdout.write("x"*1000000); sys.stdout.flush()'])
assert r['bounded_stop'] and len(r['stdout'])==ns['CAP'], r
# EOF before process exit must not escape the deadline or orphan the child.
child="import os,sys,time; print(os.getpid(),flush=True); os.close(1); os.close(2); time.sleep(30)"
r=ns['capture'](['/usr/bin/python3','-I','-c',child])
pid=int(r['stdout'].strip())
assert r['bounded_stop'] and r['exit']==-9 and 10<=r['duration_seconds']<13, r
assert not pathlib.Path('/proc',str(pid)).exists(), r
# Audit records without namespace context are outside this helper's scope.
assert 'capname=' not in code

`
	cmd := exec.Command("/usr/bin/python3", "-I", "-c", "import sys,os,json,pathlib\n"+harness)
	cmd.Stdin = strings.NewReader(source[start:end])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture outcomes: %v\n%s", err, out)
	}
}
