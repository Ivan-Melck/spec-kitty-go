"""Ordinary-user dispatch following a successful hosted namespace prerequisite."""
import importlib.util
import json
import os
import pathlib
import re
import stat
import sys


def command(argv, env):
    if sys.platform != 'linux' or os.getuid() == 0 or os.geteuid() == 0 or env.get('GITHUB_ACTIONS') != 'true' or env.get('RUNNER_ENVIRONMENT') != 'github-hosted':
        raise ValueError('github-hosted ordinary user required')
    run, attempt, job = (env.get(k, '') for k in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT', 'GITHUB_JOB'))
    if not re.fullmatch(r'[1-9][0-9]{0,19}', run) or not re.fullmatch(r'[1-9][0-9]{0,9}', attempt) or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_-]{0,63}', job):
        raise ValueError('invalid job identity')
    if env.get('GO_KITTY_USERNS_UID') != str(os.getuid()) or env.get('GO_KITTY_USERNS_GID') != str(os.getgid()):
        raise ValueError('prerequisite ordinary identity missing or changed')
    selected = bool(argv and argv[0] == '__selected')
    if selected:
        argv = argv[1:]
    if len(argv) < 2 or argv[0] != '--':
        raise ValueError('expected -- command [args]')
    argv = argv[1:]
    mode, profile = env.get('GO_KITTY_USERNS_MODE'), env.get('GO_KITTY_USERNS_PROFILE', '')
    if mode not in ('direct', 'selected'):
        raise ValueError('successful prerequisite mode required')
    if mode == 'direct':
        if profile or selected:
            raise ValueError('direct prerequisite state mismatch')
        return argv, False
    prefix = 'go-kitty-ci-' + '-'.join((run, attempt, job)) + '-'
    if not profile.startswith(prefix) or not re.fullmatch(r'[0-9a-f]{32}', profile[len(prefix):]):
        raise ValueError('profile does not belong to current job')
    if selected:
        with open('/proc/self/attr/current') as f:
            label = f.read(512)
        if label != profile + ' (unconfined)\n':
            raise ValueError('selected ordinary label mismatch')
        return argv, False
    launcher = pathlib.Path('/usr/bin/aa-exec')
    info = launcher.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022 or not os.access(launcher, os.X_OK):
        raise ValueError('aa-exec identity refused')
    return [str(launcher), '-i', '-p', profile, '--', '/usr/bin/python3', '-I', str(pathlib.Path(__file__).resolve()), '__selected', '--', *argv], True


def main():
    argv, selecting = command(sys.argv[1:], os.environ)
    if not selecting:
        root = pathlib.Path(os.environ['ARTIFACT_DIR']) / 'userns-prerequisite'
        # This ordinary evidence write cannot administer policy. Exclusive output
        # also prevents a second dispatch from silently replacing the first.
        # Compile the canonical source directly: neither read nor write an
        # adjacent import cache outside the source candidate's binding.
        spec = importlib.util.spec_from_file_location('userns_label', pathlib.Path(__file__).with_name('userns_label.py'))
        labels = importlib.util.module_from_spec(spec)
        exec(compile(spec.loader.get_source('userns_label'), spec.origin, 'exec'), labels.__dict__)
        observation = labels.label_evidence()
        # A selected profile can never turn an unavailable label into admission,
        # including a change between command selection and evidence publication.
        if os.environ['GO_KITTY_USERNS_MODE'] == 'selected' and observation['label'] != os.environ['GO_KITTY_USERNS_PROFILE'] + ' (unconfined)\n':
            raise ValueError('selected ordinary label changed before dispatch')
        with (root / 'command-selection.json').open('x') as f:
            json.dump({'uid': os.getuid(), 'gid': os.getgid(), **observation,
                       'argv': argv, 'cwd': os.getcwd(), 'mode': os.environ['GO_KITTY_USERNS_MODE'],
                       'profile': os.environ.get('GO_KITTY_USERNS_PROFILE', '')}, f)
            f.write('\n')
    os.execvpe(argv[0], argv, os.environ)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError) as error:
        print('namespace dispatch refused: ' + str(error), file=sys.stderr)
        sys.exit(1)
