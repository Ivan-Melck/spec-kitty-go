#!/bin/bash
# Diagnostic-only hosted prerequisite. Never changes policy or executes as root.
set -eu
exec /usr/bin/python3 -I - <<'PY'
import hashlib
import json
import os
import pathlib
import re
import selectors
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
        child = "import os,json; print(json.dumps({'pid':os.getpid(),'uid':os.getuid(),'gid':os.getgid(),'namespaces':{n:os.readlink('/proc/self/ns/'+n) for n in ['net','pid','user']},'uid_map':open('/proc/self/uid_map').read(),'gid_map':open('/proc/self/gid_map').read(),'setgroups':open('/proc/self/setgroups').read(),'interfaces':open('/proc/net/dev').read()}))"
        probe = capture([str(launcher), *FLAGS, '/usr/bin/python3', '-I', '-c', child])
        probe['parent_namespaces'] = parent
        save('probe.json', probe)
        # Read only relevant recent kernel denials; lack of audit access is evidence.
        audit = capture(['/usr/bin/journalctl', '-k', '--since', '-2 minutes', '-n', '80', '--no-pager', '-o', 'cat', '--grep', 'apparmor=.*DENIED.*(userns|unshare)'])
        save('audit.json', audit)
        if probe['exit'] != 0 or probe['bounded_stop']:
            save('privileged-inventory.json', capture(['/usr/bin/sudo', '-n', '/usr/bin/python3', '-I', '-c', PRIVILEGED_INVENTORY]))
            save('privileged-audit.json', capture(['/usr/bin/sudo', '-n', '/usr/bin/journalctl', '-k', '--since', '-2 minutes', '-n', '80', '--no-pager', '-o', 'cat', '--grep', 'apparmor=.*DENIED.*(userns|unshare)']))
            raise ValueError('ordinary-user namespace probe failed; cause/attachment nonconflict not established; no policy change attempted')
        c = json.loads(probe['stdout'])
        mappings = [list(map(int, c[key].split())) for key in ['uid_map', 'gid_map']]
        interfaces = [line.split(':')[0].strip() for line in c['interfaces'].splitlines() if ':' in line]
        if (c['pid'] != 1 or c['uid'] != 0 or c['gid'] != 0 or c['setgroups'].strip() != 'deny' or
            mappings != [[0, os.getuid(), 1], [0, os.getgid(), 1]] or interfaces != ['lo'] or
            any(c['namespaces'][n] == parent[n] for n in parent)):
            raise ValueError('namespace probe identity/mapping/topology did not establish isolation')
        result['verdict'] = 'pass'
        result['reason'] = 'ordinary-user exact-flags probe passed; no policy change'
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
