package architecture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real kernel/strace execution using the canonical prerequisite main. The
// disposable hosted-state fixture is not proof this machine is a hosted runner;
// all privileged operations are refused by the test's capture boundary.
func TestCIUsernsRealProbeAndStracePreserveIsolation(t *testing.T) {
	source := usernsHelper(t)
	start, end := strings.Index(source, "import hashlib\n"), strings.LastIndex(source, "\nPY\n")
	if start < 0 || end < start {
		t.Fatal("embedded helper unavailable")
	}
	code := `import os,sys,json,pathlib,tempfile
from unittest.mock import patch
ns={'__name__':'fixture'};exec(sys.stdin.read(),ns)
realcapture=ns['capture']; calls=[]
def capture(argv):
 calls.append(argv)
 if argv[0] in ('/usr/bin/sudo','/usr/bin/aa-exec','/usr/sbin/apparmor_parser'):
  raise ValueError('live test cannot administer or select policy')
 return realcapture(argv)
with tempfile.TemporaryDirectory() as temp:
 root=pathlib.Path(temp); art=root/'artifact';art.mkdir(); envfile=root/'env';envfile.touch()
 env={'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'12','GITHUB_RUN_ATTEMPT':'1','GITHUB_JOB':'quality','RUNNER_TEMP':temp,'ARTIFACT_DIR':str(art),'GITHUB_ENV':str(envfile),'GO_KITTY_USERNS_SOURCE':sys.argv[1]}
 with patch.dict(os.environ,env,clear=True),patch.dict(ns,{'capture':capture}): status=ns['main']()
 result=json.loads((art/'userns-prerequisite/result.json').read_text())
 assert status==0 and result['verdict']=='pass' and result['mode']=='direct' and result['policy_changed'] is False,result
 probe=json.loads((art/'userns-prerequisite/probe.json').read_text()); child=json.loads(probe['stdout'])
 assert child['pid']==1 and child['uid']==child['gid']==0,child
 assert all(child['namespaces'][n]!=probe['parent_namespaces'][n] for n in ['net','pid','user']),probe
 assert child['setgroups'].strip()=='deny' and [line.split(':')[0].strip() for line in child['interfaces'].splitlines() if ':' in line]==['lo'],child
 assert child['uid_map'].split()==['0',str(os.getuid()),'1'] and child['gid_map'].split()==['0',str(os.getgid()),'1'],child
 assert 'GO_KITTY_USERNS_UID='+str(os.getuid()) in envfile.read_text()
 # Independently exercise the installed tracer under the same isolation flags.
 trace=root/'strace.log'; target=['/usr/bin/unshare',*ns['FLAGS'],'/bin/true']
 traced=realcapture(['/usr/bin/strace','-f','-o',str(trace),*target])
 assert traced['exit']==0 and not traced['bounded_stop'],traced
 text=trace.read_text(); assert 'CLONE_NEWUSER' in text and 'CLONE_NEWNET' in text and 'CLONE_NEWPID' in text and 'execve(' in text,text
 print(json.dumps({'verdict':result['verdict'],'policy_changed':result['policy_changed'],'namespace_identity':child,'strace_exit':traced['exit']}))
`
	command := exec.Command("/usr/bin/python3", "-I", "-c", code, "../../scripts/ci/prepare-userns.sh")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	command.Stdin = strings.NewReader(source[start:end])
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real namespace/trace proof: %v\n%s", err, output)
	}
	t.Logf("real namespace/trace proof: %s", output)
}

func TestCIUsernsDirectLabelObservationIsExplicitAndFailClosed(t *testing.T) {
	wrapper, err := filepath.Abs("../../scripts/ci/with-userns.py")
	if err != nil {
		t.Fatal(err)
	}
	code := `import builtins,errno,io,json,os,pathlib,sys,tempfile
from unittest.mock import patch
path=pathlib.Path(sys.argv[1]); ns={'__name__':'fixture','__file__':str(path)}
exec(path.read_text(),ns)
base={'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'12','GITHUB_RUN_ATTEMPT':'1','GITHUB_JOB':'quality','GO_KITTY_USERNS_UID':str(os.getuid()),'GO_KITTY_USERNS_GID':str(os.getgid()),'GO_KITTY_USERNS_MODE':'direct','GO_KITTY_USERNS_PROFILE':''}
real_open=builtins.open
cases=[
 ('observed',None,'unconfined\n',None,True),
 ('no-label-lsm',errno.EINVAL,None,'capability,landlock,lockdown,yama,bpf\n',True),
 ('apparmor',errno.EINVAL,None,'capability,apparmor\n',False),
 ('selinux',errno.EINVAL,None,'capability,selinux\n',False),
 ('unknown-lsm',errno.EINVAL,None,'capability,unknown\n',False),
 ('empty-lsm',errno.EINVAL,None,'',False),
 ('unreadable-lsm',errno.EINVAL,None,None,False),
 ('truncated-lsm',errno.EINVAL,None,'capability,'*100,False),
 ('denied',errno.EACCES,None,'capability,landlock\n',False),
 ('io-failure',errno.EIO,None,'capability,landlock\n',False),
 ('empty-label',None,'',None,False),
 ('truncated-label',None,'x'*513,None,False),
]
for name,label_errno,label,lsms,accepted in cases:
 with tempfile.TemporaryDirectory() as directory:
  root=pathlib.Path(directory); (root/'userns-prerequisite').mkdir()
  env=dict(base,ARTIFACT_DIR=directory)
  def read_file(p,*args,**kwargs):
   if str(p)=='/proc/self/attr/current':
    if label_errno: raise OSError(label_errno,'fixture label refusal')
    return io.StringIO(label)
   if str(p)=='/sys/kernel/security/lsm':
    if lsms is None: raise OSError(errno.EACCES,'fixture LSM inventory refusal')
    return io.StringIO(lsms)
   return real_open(p,*args,**kwargs)
  with patch.dict(os.environ,env,clear=True),patch.object(sys,'argv',[str(path),'--','/bin/true']),patch('builtins.open',side_effect=read_file),patch('importlib.machinery.SourceFileLoader.get_code',side_effect=AssertionError('cached code must not participate')),patch.object(os,'execvpe') as launch:
   try: ns['main'](); failure=None
   except (OSError,ValueError) as e: failure=e
  if accepted:
   assert failure is None and launch.call_count==1,(name,failure)
   evidence=json.loads((root/'userns-prerequisite'/'command-selection.json').read_text())
   assert evidence['mode']=='direct' and evidence['profile']=='' and evidence['argv']==['/bin/true'],name
   if label_errno:
    assert evidence['label'] is None and evidence['label_observation']=={'status':'not_applicable','reason':'no_label_lsm','active_lsms':['capability','landlock','lockdown','yama','bpf']},(name,evidence)
   else: assert evidence['label']==label,name
  else: assert failure is not None and launch.call_count==0,(name,failure)
# A selected profile must never use the direct no-label observation path.
name='go-kitty-ci-12-1-quality-'+'a'*32
env=dict(base,GO_KITTY_USERNS_MODE='selected',GO_KITTY_USERNS_PROFILE=name)
with patch('builtins.open',side_effect=OSError(errno.EINVAL,'fixture label refusal')):
 try: ns['command'](['__selected','--','/bin/true'],env)
 except OSError: pass
 else: raise AssertionError('selected profile accepted an unavailable label')
`
	command := exec.Command("/usr/bin/python3", "-I", "-c", code, wrapper)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("direct label evidence/refusal: %v\n%s", err, output)
	}
}
