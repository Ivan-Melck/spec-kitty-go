#!/bin/bash
# Hosted ordinary-user prerequisite; only a fixed named profile may be added conditionally.
set -eu
exec /usr/bin/python3 -I - <<'PY'
import hashlib
import json
import os
import pathlib
import re
import selectors
import secrets
import signal
import stat
import subprocess
import sys
import time

CAP = 65536
FLAGS = ['--user', '--map-root-user', '--net', '--pid', '--fork', '--kill-child=SIGKILL', '--']

# Fixed read-only privileged reader, used only after the guarded ordinary probe
# fails. No caller-provided paths, commands or policy content cross this boundary.
PRIVILEGED_INVENTORY = r"""
import os,json,re
budget=8192
def read(path):
    global budget
    try:
        fd=os.open(path,os.O_RDONLY|os.O_NONBLOCK)
        try: data=os.read(fd,min(1024,budget)+1)
        finally: os.close(fd)
        take=min(len(data),1024,budget); budget-=take
        return {'text':data[:take].decode('utf-8','replace'),'truncated':len(data)>take}
    except OSError as e: return {'unavailable':str(e)}
def revision(path):
    value=read(path)
    if value.get('truncated') or not re.fullmatch(r'[0-9]+\n',value.get('text','')):
        return {'unavailable':'incomplete revision record','observation':value}
    return value
root='/sys/kernel/security/apparmor/policy'
r={'reader_uid':os.getuid(),'reader_label':read('/proc/self/attr/current'),
   'namespace_context':{n:read('/sys/kernel/security/apparmor/'+n) for n in ['.ns_name','.ns_level','.stacked','.ns_stacked']},
   'revision_before':revision(root+'/revision'),'entries':[],'errors':[],
   'complete_nonconflict_proof':False}
count=0
for directory,dirs,files in os.walk(root,followlinks=False,onerror=lambda e:r['errors'].append(str(e))):
    count+=1+len(dirs)+len(files)
    if count>1024 or len(r['entries'])>=128 or budget<=128:
        r['truncated']=True; break
    for name in sorted(files):
        if name in ('attach','name','mode'):
            path=os.path.join(directory,name)
            r['entries'].append({'path':path,**read(path)})
            if len(r['entries'])>=128: break
r['revision_after']=revision(root+'/revision')
r['revision_stable']='text' in r['revision_before'] and r['revision_before']==r['revision_after']
print(json.dumps(r))
"""

# Privileged boundary: no caller policy or path. Reconstruct one fixed profile.
PROFILE_LOADER = r"""
import os,sys,re,json,hashlib,stat,signal,subprocess,tempfile,resource
class Expired(Exception): pass
def expired(*args): raise Expired('privileged loader deadline')
signal.signal(signal.SIGALRM,expired); signal.alarm(7)
def profile_parts(parts):
    if len(parts)!=4: raise ValueError('four identity components required')
    run,attempt,job,nonce=parts
    if not re.fullmatch(r'[1-9][0-9]{0,19}',run) or not re.fullmatch(r'[1-9][0-9]{0,9}',attempt) or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_-]{0,63}',job) or not re.fullmatch(r'[0-9a-f]{32}',nonce):
        raise ValueError('invalid profile identity')
    name='go-kitty-ci-'+run+'-'+attempt+'-'+job+'-'+nonce
    policy='abi <abi/4.0>,\nprofile '+name+' flags=(unconfined) {\n  userns,\n}\n'
    return name,policy

def load(parts):
    name,policy=profile_parts(parts)
    if os.geteuid()!=0: raise ValueError('root loader required')
    parser='/usr/sbin/apparmor_parser'
    info=os.stat(parser)
    if not stat.S_ISREG(info.st_mode) or info.st_uid!=0 or info.st_mode&0o022 or not os.access(parser,os.X_OK): raise ValueError('parser identity refused')
    with open(parser,'rb') as f: digest=hashlib.sha256(f.read()).hexdigest()
    record={'name':name,'policy':policy,'policy_sha256':hashlib.sha256(policy.encode()).hexdigest(),'parser':parser,'parser_sha256':digest,'argv':[parser,'--add','--skip-cache'],'exit':None,'timed_out':False}
    child=None
    def limits(): resource.setrlimit(resource.RLIMIT_FSIZE,(65536,65536))
    with tempfile.TemporaryFile() as out,tempfile.TemporaryFile() as err:
        try:
            child=subprocess.Popen(record['argv'],stdin=subprocess.PIPE,stdout=out,stderr=err,env={'PATH':'/usr/bin:/bin','LANG':'C.UTF-8'},start_new_session=True,preexec_fn=limits)
            child.communicate(policy.encode(),timeout=3)
        except (subprocess.TimeoutExpired,Expired) as e:
            record['timed_out']=True; record['error']=str(e)
        finally:
            if child is not None:
                # communicate reaps successful and failed exits. Signal only a
                # parser still owned and unreaped after timeout/failure.
                if child.returncode is None:
                    try: os.killpg(child.pid,signal.SIGKILL)
                    except ProcessLookupError: pass
                record['exit']=child.wait(timeout=1)
            out.seek(0);err.seek(0)
            record['stdout']=out.read(65536).decode('utf-8','replace');record['stderr']=err.read(65536).decode('utf-8','replace')
    return record
if __name__=='__main__':
    try:
        record=load(sys.argv[1:]);print(json.dumps(record));sys.exit(0 if record['exit']==0 and not record['timed_out'] else 1)
    except Exception as e: print(json.dumps({'error':str(e),'exit':None}));sys.exit(1)
"""

def profile_identity(parts):
    # Same closed identity grammar as the independently checked root loader.
    run, attempt, job, nonce = parts
    if not re.fullmatch(r'[1-9][0-9]{0,19}',run) or not re.fullmatch(r'[1-9][0-9]{0,9}',attempt) or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_-]{0,63}',job) or not re.fullmatch(r'[0-9a-f]{32}',nonce):
        raise ValueError('invalid profile identity')
    name = 'go-kitty-ci-' + '-'.join(parts)
    return name, 'abi <abi/4.0>,\nprofile ' + name + ' flags=(unconfined) {\n  userns,\n}\n'

def supported_denial(diagnostics, probe, audit):
    # Corroborates only the authorized fixed trial; it does not prove lineage
    # or determine the policy, privileges, executable or downstream command.
    def text(path): return diagnostics['files'].get(path, {}).get('text', '').strip()
    context = diagnostics['attachments']['namespace_context']
    if (diagnostics['uid'] == 0 or diagnostics['gid'] == 0 or
        '\nID=ubuntu\n' not in '\n'+text('/etc/os-release')+'\n' or '\nVERSION_ID="24.04"\n' not in '\n'+text('/etc/os-release')+'\n' or
        text('/proc/self/attr/current') != 'unconfined' or text('/sys/module/apparmor/parameters/enabled') != 'Y' or
        text('/proc/sys/kernel/apparmor_restrict_unprivileged_userns') != '1' or
        text('/proc/sys/kernel/apparmor_restrict_unprivileged_unconfined') != '0' or
        text('/proc/sys/kernel/unprivileged_userns_clone') != '1' or
        not text('/proc/sys/user/max_user_namespaces').isdigit() or int(text('/proc/sys/user/max_user_namespaces')) <= 0 or
        any(context.get(k,{}).get('text','').strip()!=v for k,v in {'.ns_name':'root','.ns_level':'0','.stacked':'no','.ns_stacked':'no'}.items()) or
        probe.get('exit') != 1 or probe.get('bounded_stop') or probe.get('stdout') or
        'write failed /proc/self/uid_map: Operation not permitted' not in probe.get('stderr','') or
        audit.get('exit') != 0 or audit.get('bounded_stop') or not isinstance(probe.get('pid'),int)):
        return False
    return any(all(re.search(pattern,line) for pattern in [r'apparmor="DENIED"',r'profile="unprivileged_userns"',r'comm="unshare"',r'capability=21\b',r'capname="sys_admin"']) for line in audit['stdout'].splitlines())

def publish_state(env, temp, mode, profile):
    path=pathlib.Path(env.get('GITHUB_ENV',''))
    safe_directory(str(path.parent)).relative_to(temp)
    fd=os.open(path,os.O_WRONLY|os.O_APPEND|os.O_NOFOLLOW)
    try:
        info=os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid!=os.geteuid(): raise ValueError('GITHUB_ENV ownership/type refused')
        data=('GO_KITTY_USERNS_MODE='+mode+'\nGO_KITTY_USERNS_PROFILE='+profile+'\nGO_KITTY_USERNS_UID='+str(os.getuid())+'\nGO_KITTY_USERNS_GID='+str(os.getgid())+'\n').encode()
        if os.write(fd,data)!=len(data): raise ValueError('incomplete prerequisite publication')
    finally: os.close(fd)

def read_file(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
        try:
            data = os.read(fd, CAP + 1)
        finally:
            os.close(fd)
        return {'text': data[:CAP].decode('utf-8', 'replace'), 'truncated': len(data) > CAP}
    except OSError as e:
        return {'unavailable': str(e)}

def revision(path):
    value = read_file(path)
    if value.get('truncated') or not re.fullmatch(r'[0-9]+\n', value.get('text', '')):
        return {'unavailable': 'incomplete revision record', 'observation': value}
    return value

class HelperDeadline(Exception):
    pass

def deadline_expired(signum, frame):
    raise HelperDeadline('total helper deadline exceeded')

def capture(argv):
    start = time.monotonic()
    result = {'argv': argv, 'stdout': '', 'stderr': '', 'exit': None, 'bounded_stop': False}
    env = {'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8'}
    try:
        p = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, start_new_session=True)
    except OSError as e:
        result['unavailable'] = str(e)
        return result
    result['pid'] = p.pid
    streams = {'stdout': bytearray(), 'stderr': bytearray()}
    sel = selectors.DefaultSelector()
    for name in streams:
        sel.register(getattr(p, name), selectors.EVENT_READ, name)
    deadline = None
    try:
        # EOF is not process completion: retain one deadline across both.
        while sel.get_map() or p.poll() is None:
            if time.monotonic() - start > 10:
                result['bounded_stop'] = True
                break
            if not sel.get_map():
                time.sleep(.05)
                continue
            for key, _ in sel.select(.1):
                data = os.read(key.fd, 8192)
                if not data:
                    sel.unregister(key.fileobj)
                    continue
                buf = streams[key.data]
                remaining = CAP - len(buf)
                buf.extend(data[:remaining])
                if len(data) > remaining:
                    result['bounded_stop'] = True
                    break
            if result['bounded_stop']:
                break
    except HelperDeadline as e:
        deadline = e
        result['bounded_stop'] = True
        result['capture_error'] = str(e)
    except Exception as e:
        result['bounded_stop'] = True
        result['capture_error'] = str(e)
    finally:
        # Also stop descendants of a completed diagnostic parent. This group
        # was created for this command only; no caller process is targeted.
        try:
            os.killpg(p.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except OSError as e:
            result['bounded_stop'] = True
            result['kill_error'] = str(e)
        try:
            result['exit'] = p.wait(timeout=2)
        except subprocess.TimeoutExpired as e:
            result['bounded_stop'] = True
            result['reap_error'] = str(e)
        sel.close()
        for name, buf in streams.items():
            getattr(p, name).close()
            result[name] = buf.decode('utf-8', 'replace')

    result['duration_seconds'] = time.monotonic() - start
    if deadline is not None:
        deadline.capture_result = result
        raise deadline
    return result

def attachments():
    # Exported attachments are diagnostics, not a complete kernel match oracle.
    root = '/sys/kernel/security/apparmor/policy'
    result = {'root': root, 'entries': [], 'complete_nonconflict_proof': False,
              'revision_before': revision(root + '/revision'),
              'reader_context': {p: read_file('/proc/self/attr/' + p) for p in ['current', 'apparmor/current']},
              'namespace_context': {p: read_file('/sys/kernel/security/apparmor/' + p) for p in ['.ns_name', '.ns_level', '.stacked', '.ns_stacked']}}
    def unavailable(e):
        result.setdefault('errors', []).append(str(e))
    count = 0
    for directory, dirs, files in os.walk(root, followlinks=False, onerror=unavailable):
        count += 1 + len(dirs) + len(files)
        if count > 1024 or len(result['entries']) >= 128:
            result['truncated'] = True
            break
        for name in sorted(files):
            if name in ('attach', 'name', 'mode'):
                path = os.path.join(directory, name)
                result['entries'].append({'path': path, **read_file(path)})
                if len(result['entries']) >= 128:
                    break
    result['revision_after'] = revision(root + '/revision')
    result['revision_stable'] = 'text' in result['revision_before'] and result['revision_before'] == result['revision_after']
    result['reader_uid'] = os.getuid()
    return result

def safe_directory(raw):
    path = pathlib.Path(raw)
    if not path.is_absolute() or str(path) != raw or '..' in path.parts:
        raise ValueError('absolute clean artifact directory required')
    for part in [path, *path.parents]:
        if stat.S_ISLNK(part.lstat().st_mode) or not part.is_dir():
            raise ValueError('directory symlink/type refused')
    return path

def main():
    env = os.environ
    if (sys.platform != 'linux' or os.getuid() == 0 or os.geteuid() == 0 or
        env.get('GITHUB_ACTIONS') != 'true' or env.get('RUNNER_ENVIRONMENT') != 'github-hosted' or
        not re.fullmatch(r'[1-9][0-9]*', env.get('GITHUB_RUN_ID', '')) or
        not re.fullmatch(r'[1-9][0-9]*', env.get('GITHUB_RUN_ATTEMPT', '')) or
        not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_-]*', env.get('GITHUB_JOB', ''))):
        print('github-hosted ordinary user required', file=sys.stderr)
        return 1
    try:
        temp = safe_directory(env.get('RUNNER_TEMP', ''))
        artifact = safe_directory(env.get('ARTIFACT_DIR', ''))
        artifact.relative_to(temp)
        if artifact == temp or artifact.stat().st_uid != os.geteuid():
            raise ValueError('owned artifact subdirectory required')
        dest = artifact / 'userns-prerequisite'
        dest.mkdir(mode=0o700)
    except (OSError, ValueError) as e:
        print('artifact directory refused: ' + str(e), file=sys.stderr)
        return 1
    def save(name, value):
        with (dest / name).open('x') as f:
            json.dump(value, f, indent=2)
            f.write('\n')
    result = {'schema': 'go-kitty.ci-userns/1', 'run_id': env['GITHUB_RUN_ID'],
              'attempt': env['GITHUB_RUN_ATTEMPT'], 'job': env['GITHUB_JOB'],
              'verdict': 'refused', 'policy_changed': False, 'mode': 'diagnostic-only'}
    save('started.json', result)
    signal.signal(signal.SIGALRM, deadline_expired)
    signal.setitimer(signal.ITIMER_REAL, 60)
    helper_started = time.monotonic()
    diagnostics = {}
    try:
        diagnostics = {'uid': os.getuid(), 'gid': os.getgid(), 'uname': list(os.uname()),
            'files': {p: read_file(p) for p in ['/etc/os-release', '/proc/self/attr/current',
                '/sys/module/apparmor/parameters/enabled', '/proc/sys/kernel/apparmor_restrict_unprivileged_userns',
                '/proc/sys/kernel/apparmor_restrict_unprivileged_unconfined',
                '/proc/sys/user/max_user_namespaces', '/proc/sys/kernel/unprivileged_userns_clone',
                '/sys/kernel/security/apparmor/profiles']}}
        save('diagnostics-files.json', diagnostics)
        diagnostics['attachments'] = attachments()
        save('diagnostics.json', diagnostics)
        launcher = next((pathlib.Path(p) for p in ['/usr/bin/unshare', '/bin/unshare'] if os.access(p, os.X_OK)), None)
        if launcher is None:
            raise ValueError('fixed system unshare unavailable')
        launcher = launcher.resolve(strict=True)
        info = launcher.stat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('system unshare ownership/type/mode refused')
        save('launcher.json', {'path': str(launcher), 'uid': info.st_uid, 'mode': oct(stat.S_IMODE(info.st_mode)),
            'sha256': hashlib.sha256(launcher.read_bytes()).hexdigest(), 'version': capture([str(launcher), '--version'])})
        parent = {name: os.readlink('/proc/self/ns/' + name) for name in ['net', 'pid', 'user']}
        child = "import os,json; print(json.dumps({'pid':os.getpid(),'uid':os.getuid(),'gid':os.getgid(),'label':open('/proc/self/attr/current').read(),'namespaces':{n:os.readlink('/proc/self/ns/'+n) for n in ['net','pid','user']},'uid_map':open('/proc/self/uid_map').read(),'gid_map':open('/proc/self/gid_map').read(),'setgroups':open('/proc/self/setgroups').read(),'interfaces':open('/proc/net/dev').read()}))"
        probe = capture([str(launcher), *FLAGS, '/usr/bin/python3', '-I', '-c', child])
        probe['parent_namespaces'] = parent
        save('probe.json', probe)
        # Read only relevant recent kernel denials; lack of audit access is evidence.
        audit = capture(['/usr/bin/journalctl', '-k', '--since', '-2 minutes', '-n', '80', '--no-pager', '-o', 'cat', '--grep', 'apparmor=.*DENIED.*(userns|unshare)'])
        save('audit.json', audit)
        mode, profile = 'direct', ''
        if probe['exit'] != 0 or probe['bounded_stop']:
            save('privileged-inventory.json', capture(['/usr/bin/sudo', '-n', '/usr/bin/python3', '-I', '-c', PRIVILEGED_INVENTORY]))
            privileged_audit = capture(['/usr/bin/sudo', '-n', '/usr/bin/journalctl', '-k', '--since', '-2 minutes', '-n', '80', '--no-pager', '-o', 'cat', '--grep', 'apparmor=.*DENIED.*(userns|unshare)'])
            save('privileged-audit.json', privileged_audit)
            result.update(audit_correlation='unestablished', observed_probe_pid=probe.get('pid'),
                          observed_audit_pids=[int(pid) for pid in re.findall(r'\bpid=([0-9]+)',privileged_audit.get('stdout',''))])
            if not supported_denial(diagnostics, probe, privileged_audit):
                raise ValueError('supported context and corroborating unconfined-userns denial not established; no policy change attempted')
            if time.monotonic()-helper_started > 40:
                raise ValueError('insufficient total deadline remaining for bounded privileged loader')
            parts=[env['GITHUB_RUN_ID'],env['GITHUB_RUN_ATTEMPT'],env['GITHUB_JOB'],secrets.token_hex(16)]
            profile,policy=profile_identity(parts)
            save('profile-intent.json',{'name':profile,'policy':policy,'sha256':hashlib.sha256(policy.encode()).hexdigest(),'lifetime':'ephemeral-job','attachment':None})
            parser_version=capture(['/usr/sbin/apparmor_parser','--version'])
            save('parser-version.json',parser_version)
            if parser_version['exit']!=0 or parser_version['bounded_stop']: raise ValueError('parser version unavailable')
            aa=pathlib.Path('/usr/bin/aa-exec'); ai=aa.stat()
            if not stat.S_ISREG(ai.st_mode) or ai.st_uid!=0 or ai.st_mode&0o022 or not os.access(aa,os.X_OK): raise ValueError('aa-exec identity refused')
            save('aa-exec.json',{'path':str(aa),'uid':ai.st_uid,'mode':oct(stat.S_IMODE(ai.st_mode)),'sha256':hashlib.sha256(aa.read_bytes()).hexdigest()})
            result.update(policy_outcome='possibly-added',policy_changed=None,profile=profile)
            loaded=capture(['/usr/bin/sudo','-n','/usr/bin/python3','-I','-c',PROFILE_LOADER,*parts])
            save('profile-load.json',loaded)
            if loaded['exit']!=0 or loaded['bounded_stop']: raise ValueError('add-only profile loader failed; no replacement or retry')
            record=json.loads(loaded['stdout'])
            if record.get('name')!=profile or record.get('policy')!=policy or record.get('policy_sha256')!=hashlib.sha256(policy.encode()).hexdigest() or record.get('exit')!=0 or record.get('timed_out'):
                raise ValueError('profile load receipt mismatch')
            result['policy_changed']=True
            result['policy_outcome']='added-for-ephemeral-job'
            selected="import os,json,sys; print(json.dumps({'selected_uid':os.getuid(),'selected_gid':os.getgid(),'selected_label':open('/proc/self/attr/current').read()}),flush=True); os.execv(sys.argv[1],sys.argv[1:])"
            probe=capture(['/usr/bin/aa-exec','-i','-p',profile,'--','/usr/bin/python3','-I','-c',selected,str(launcher),*FLAGS,'/usr/bin/python3','-I','-c',child])
            save('selected-probe.json',probe)
            if probe['exit']!=0 or probe['bounded_stop']: raise ValueError('selected ordinary-user namespace probe failed')
            lines=probe['stdout'].splitlines()
            if len(lines)!=2: raise ValueError('selected probe framing refused')
            selected_record=json.loads(lines[0])
            if selected_record!={'selected_uid':os.getuid(),'selected_gid':os.getgid(),'selected_label':profile+' (unconfined)\n'}:
                raise ValueError('ordinary selected identity/label refused')
            probe=dict(probe,stdout=lines[1])
            mode='selected'
        c = json.loads(probe['stdout'])
        mappings = [list(map(int, c[key].split())) for key in ['uid_map', 'gid_map']]
        interfaces = [line.split(':')[0].strip() for line in c['interfaces'].splitlines() if ':' in line]
        if (c['pid'] != 1 or c['uid'] != 0 or c['gid'] != 0 or c['setgroups'].strip() != 'deny' or
            mappings != [[0, os.getuid(), 1], [0, os.getgid(), 1]] or interfaces != ['lo'] or
            any(c['namespaces'][n] == parent[n] for n in parent)):
            raise ValueError('namespace probe identity/mapping/topology did not establish isolation')
        if mode=='selected' and c.get('label')!=profile+' (unconfined)\n':
            raise ValueError('selected namespace child label refused')
        controls={path:read_file(path) for path in diagnostics['files'] if path.startswith('/proc/sys/')}
        save('controls-after.json',controls)
        if any(value != diagnostics['files'][path] for path,value in controls.items()):
            raise ValueError('global namespace controls changed')
        publish_state(env,temp,mode,profile)
        result.update(verdict='pass',mode=mode,profile=profile,reason='ordinary-user exact-flags probe passed')
    except Exception as e:
        result['reason'] = str(e)
        if hasattr(e, 'capture_result'):
            save('interrupted-command.json', e.capture_result)
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
    if result['verdict'] != 'pass':
        save('partial-diagnostics.json', diagnostics)
    save('result.json', result)
    print(json.dumps(result))
    return 0 if result['verdict'] == 'pass' else 1

if __name__ == '__main__':
    sys.exit(main())
PY
