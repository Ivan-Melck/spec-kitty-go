"""Bounded kernel label evidence; never namespace or profile admission."""
import errno


# These modules do not implement /proc/PID/attr/current process labels. Unknown
# stacks refuse rather than silently treating a future label provider as absent.
_NON_LABEL_LSMS = frozenset(('capability', 'landlock', 'lockdown', 'yama', 'bpf',
                             'integrity', 'loadpin', 'safesetid'))


def label_evidence():
    try:
        with open('/proc/self/attr/current') as source:
            label = source.read(513)
    except OSError as error:
        if error.errno != errno.EINVAL:
            raise
        with open('/sys/kernel/security/lsm') as source:
            inventory = source.read(513)
        if not inventory or len(inventory) > 512:
            raise ValueError('active LSM inventory empty or truncated')
        names = inventory.removesuffix('\n').split(',')
        if len(set(names)) != len(names) or any(name not in _NON_LABEL_LSMS for name in names):
            raise ValueError('unavailable process label with unproved non-label LSM stack')
        return {'label': None, 'label_observation': {
            'status': 'not_applicable', 'reason': 'no_label_lsm', 'active_lsms': names}}
    if not label or len(label) > 512 or '\x00' in label:
        raise ValueError('process label empty, truncated or malformed')
    return {'label': label}
