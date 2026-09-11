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
		if strings.Count(s, "/usr/bin/python3 -I scripts/ci/with-userns.py -- make ") != 1 {
			t.Fatalf("%s missing selected campaign boundary", name)
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
        env = {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'12','GITHUB_RUN_ATTEMPT':'1','GITHUB_JOB':'quality','RUNNER_TEMP':temp,'ARTIFACT_DIR':str(artifact),'GITHUB_ENV':str(pathlib.Path(temp)/'env')}
        pathlib.Path(env['GITHUB_ENV']).touch()
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
        assert result['policy_changed'] is False and result['mode']==('direct' if outcome=='pass' else 'diagnostic-only')
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
assert '(userns|unshare|capname=)' not in code
# Event-like readers produce a short record but do not close. Both literal
# readers must return after a single nonblocking read, without any sudo call.
priv={'__name__':'fixture'}
exec(ns['PRIVILEGED_INVENTORY'].split("root='/sys/kernel/security/apparmor/policy'")[0],priv)
with tempfile.TemporaryDirectory() as temp:
    fifo=pathlib.Path(temp)/'revision'; os.mkfifo(fifo)
    fd=os.open(fifo,os.O_RDWR|os.O_NONBLOCK)
    try:
        for reader in [ns['revision'],priv['revision']]:
            os.write(fd,b'7\n')
            assert reader(str(fifo))['text']=='7\n'
            os.write(fd,b'8')
            assert 'unavailable' in reader(str(fifo))
            assert 'unavailable' in reader(str(fifo))
    finally: os.close(fd)
# The total deadline bypasses generic read/capture handlers, but capture still
# kills/reaps and attaches the already captured output before propagating.
import signal
signal.signal(signal.SIGALRM,ns['deadline_expired'])
signal.setitimer(signal.ITIMER_REAL,.15)
try:
    ns['capture'](['/usr/bin/python3','-I','-c',child])
    raise AssertionError('deadline swallowed')
except ns['HelperDeadline'] as e:
    r=e.capture_result
    assert r['bounded_stop'] and r['exit']==-9 and r['stdout'].strip(),r
    assert not pathlib.Path('/proc',r['stdout'].strip()).exists()
finally: signal.setitimer(signal.ITIMER_REAL,0)


`
	cmd := exec.Command("/usr/bin/python3", "-I", "-c", "import sys,os,json,pathlib\n"+harness)
	cmd.Stdin = strings.NewReader(source[start:end])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture outcomes: %v\n%s", err, out)
	}
}

func TestCIUsernsConditionalProfile(t *testing.T) {
	source := usernsHelper(t)
	source = source[strings.Index(source, "import hashlib\n"):strings.LastIndex(source, "\nPY\n")]
	code := `import sys,os,json,io,pathlib,tempfile,types,subprocess,signal
from unittest.mock import patch
ns={'__name__':'fixture'};exec(sys.stdin.read(),ns)
parts=['12','1','quality','a'*32];name,policy=ns['profile_identity'](parts)
loader={'__name__':'fixture'}
with patch.object(signal,'signal'),patch.object(signal,'alarm'):exec(ns['PROFILE_LOADER'],loader)
assert loader['profile_parts'](parts)==(name,policy)
assert policy=='abi <abi/4.0>,\nprofile '+name+' flags=(unconfined) {\n  userns,\n}\n'
for bad in [['12','1','bad/job','a'*32],['12','1','bad\n','a'*32],['0','1','quality','a'*32],['12','1','quality','*']]:
 for fn in [ns['profile_identity'],loader['profile_parts']]:
  try:fn(bad)
  except ValueError:pass
  else:raise AssertionError('profile identity injection')
# Execute fixed loader logic with mocked root/parser; no privileged subprocess.
for outcome in ['success','collision','timeout']:
 calls=[]
 class Child:
  pid=999999
  def __init__(self,argv,**kw):
   assert argv==['/usr/sbin/apparmor_parser','--add','--skip-cache'];calls.append(argv);self.kw=kw;self.returncode=None
  def communicate(self,data,timeout):
   assert data==policy.encode() and timeout==3
   if outcome=='timeout':raise subprocess.TimeoutExpired('parser',3)
   self.returncode=0 if outcome=='success' else 1
  def wait(self,timeout):return 0 if outcome=='success' else (1 if outcome=='collision' else -9)
 fakeos=types.SimpleNamespace(geteuid=lambda:0,stat=lambda p:types.SimpleNamespace(st_mode=0o100755,st_uid=0),access=lambda p,m:True,X_OK=os.X_OK,killpg=lambda p,s:calls.append(['kill',p,s]))
 with patch.dict(loader,{'os':fakeos,'open':lambda *a:io.BytesIO(b'parser'),'subprocess':types.SimpleNamespace(Popen=Child,TimeoutExpired=subprocess.TimeoutExpired,PIPE=subprocess.PIPE)}):
  record=loader['load'](parts)
 assert record['policy']==policy and record['timed_out']==(outcome=='timeout')
 assert record['exit']==(0 if outcome=='success' else (1 if outcome=='collision' else -9))
 assert len(calls)==(2 if outcome=='timeout' else 1)
# Actual observed diagnostic fixture; all privileged/selection commands mocked.
realread=ns['read_file']
controls={'/etc/os-release':'ID=ubuntu\nVERSION_ID="24.04"\n','/proc/self/attr/current':'unconfined\n','/sys/module/apparmor/parameters/enabled':'Y\n','/proc/sys/kernel/apparmor_restrict_unprivileged_userns':'1\n','/proc/sys/kernel/apparmor_restrict_unprivileged_unconfined':'0\n','/proc/sys/kernel/unprivileged_userns_clone':'1\n','/proc/sys/user/max_user_namespaces':'31633\n'}
context={k:{'text':v+'\n'} for k,v in {'.ns_name':'root','.ns_level':'0','.stacked':'no','.ns_stacked':'no'}.items()}
for outcome in ['success','alternate-kernel','different-pid','missing-denial','unrelated-denial','unsupported-context','collision','failed-retry','changed-controls']:
 with tempfile.TemporaryDirectory() as temp:
  art=pathlib.Path(temp)/'artifact';art.mkdir();envfile=pathlib.Path(temp)/'env';envfile.touch()
  env={'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'12','GITHUB_RUN_ATTEMPT':'1','GITHUB_JOB':'quality','RUNNER_TEMP':temp,'ARTIFACT_DIR':str(art),'GITHUB_ENV':str(envfile)}
  calls=[];loaded=False
  def read(path):
   if outcome=='unsupported-context' and path.endswith('unprivileged_userns_clone'):return {'text':'0\n','truncated':False}
   if outcome=='changed-controls' and loaded and path.endswith('max_user_namespaces'):return {'text':'1\n','truncated':False}
   return {'text':controls.get(path,''),'truncated':False}
  def capture(argv):
   nonlocal_dummy=None
   global loaded
   calls.append(argv);r={'argv':argv,'exit':0,'bounded_stop':False,'stdout':'','stderr':'','pid':1234}
   if argv[0]=='/usr/bin/unshare' and '--map-root-user' in argv:r.update(exit=1,stderr='unshare: write failed /proc/self/uid_map: Operation not permitted\n')
   elif '/usr/bin/journalctl' in argv:
    r['stdout']='apparmor="DENIED" profile="unprivileged_userns" comm="unshare" capability=21 capname="sys_admin" pid='+('999' if outcome=='different-pid' else '1234')
    if outcome=='missing-denial':r['stdout']=''
    if outcome=='unrelated-denial':r['stdout']=r['stdout'].replace('comm="unshare"','comm="unrelated"')
   elif ns['PROFILE_LOADER'] in argv:
    loaded=True
    assert argv[-4:]==parts
    r.update(exit=1 if outcome=='collision' else 0,stdout=json.dumps({'name':name,'policy':policy,'policy_sha256':ns['hashlib'].sha256(policy.encode()).hexdigest(),'exit':1 if outcome=='collision' else 0,'timed_out':False}))
   elif argv[0]=='/usr/bin/aa-exec':
    assert argv[:6]==['/usr/bin/aa-exec','-i','-p',name,'--','/usr/bin/python3']
    selected={'selected_uid':os.getuid(),'selected_gid':os.getgid(),'selected_label':name+' (unconfined)\n'}
    child={'pid':1,'uid':0,'gid':0,'label':name+' (unconfined)\n','namespaces':{'net':'fixture','pid':'fixture','user':'fixture'},'uid_map':'0 %d 1'%os.getuid(),'gid_map':'0 %d 1'%os.getgid(),'setgroups':'deny','interfaces':'lo: 0\n'}
    r.update(exit=1 if outcome=='failed-retry' else 0,stdout=json.dumps(selected)+'\n'+json.dumps(child)+'\n')
   return r
  actualstat=pathlib.Path.stat;actualbytes=pathlib.Path.read_bytes
  def fake_stat(path,*a,**kw):return types.SimpleNamespace(st_mode=0o100755,st_uid=0) if str(path)=='/usr/bin/aa-exec' else actualstat(path,*a,**kw)
  def fake_bytes(path):return b'aa-exec' if str(path)=='/usr/bin/aa-exec' else actualbytes(path)
  realaccess=os.access
  with patch.dict(os.environ,env,clear=True),patch.dict(ns,{'read_file':read,'attachments':lambda:{'namespace_context':context},'capture':capture}),patch.object(ns['secrets'],'token_hex',return_value='a'*32),patch.object(os,'uname',return_value=('Linux','host','6.18.1-fixture' if outcome=='alternate-kernel' else '6.17.0-1022-azure','version','x86_64')),patch.object(pathlib.Path,'stat',fake_stat),patch.object(pathlib.Path,'read_bytes',fake_bytes),patch.object(os,'access',lambda p,m:True if str(p)=='/usr/bin/aa-exec' else realaccess(p,m)):
   status=ns['main']()
  result=json.loads((art/'userns-prerequisite/result.json').read_text())
  assert (status==0)==(outcome in ('success','alternate-kernel','different-pid')),result
  if outcome in ('success','alternate-kernel','different-pid'):assert 'GO_KITTY_USERNS_MODE=selected' in envfile.read_text() and result['policy_changed'] is True
  else:assert envfile.read_text()==''
  if outcome in ('missing-denial','unrelated-denial','unsupported-context'):assert not any(ns['PROFILE_LOADER'] in a for a in calls)
  if outcome=='different-pid':
   assert result['audit_correlation']=='unestablished' and result['observed_probe_pid']==1234 and result['observed_audit_pids']==[999]
   assert any(ns['PROFILE_LOADER'] in a for a in calls)
  if outcome=='collision':assert not any(a[0]=='/usr/bin/aa-exec' for a in calls) and result['policy_changed'] is None
`
	cmd := exec.Command("/usr/bin/python3", "-I", "-c", code)
	cmd.Stdin = strings.NewReader(source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("conditional profile fixtures: %v\n%s", err, out)
	}
}
