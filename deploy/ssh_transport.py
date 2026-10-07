"""Shared temporary credentials and pinned host verification for runner scripts."""
from contextlib import contextmanager
import os
from pathlib import Path
import re
import subprocess
import tempfile


@contextmanager
def session():
    host, user = os.environ.get('DEPLOY_HOST', ''), os.environ.get('DEPLOY_USER', '')
    port = os.environ.get('DEPLOY_PORT', '') or '22'
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]*', host) or not re.fullmatch(r'[a-z_][a-z0-9_-]*', user):
        raise RuntimeError('Set DEPLOY_HOST and DEPLOY_USER variables')
    if not port.isdigit() or not 1 <= int(port) <= 65535:
        raise RuntimeError('Invalid deployment port')
    for key in ('DEPLOY_SSH_KEY', 'DEPLOY_KNOWN_HOSTS'):
        if not os.environ.get(key):
            raise RuntimeError('Missing deployment setting: ' + key)
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix='movie-helper-ssh-') as tmp:
        keyfile, hosts = Path(tmp) / 'key', Path(tmp) / 'known_hosts'
        keyfile.write_text(os.environ['DEPLOY_SSH_KEY'].rstrip() + '\n')
        hosts.write_text(os.environ['DEPLOY_KNOWN_HOSTS'].rstrip() + '\n')
        options = ['-i', str(keyfile), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
                   '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(hosts),
                   '-o', 'ConnectTimeout=15', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=4',
                   '-o', 'ControlMaster=auto', '-o', 'ControlPersist=60', '-o', 'ControlPath=' + tmp + '/control']
        ssh = ['ssh', *options, '-p', port, f'{user}@{host}']
        scp = ['scp', *options, '-P', port]
        try:
            yield ssh, scp, f'{user}@{host}'
        finally:
            subprocess.run(['ssh', *options, '-p', port, '-O', 'exit', f'{user}@{host}'],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
