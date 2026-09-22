#!/usr/bin/env python3
"""GitHub runner side: temporary SSH credentials and stdin-only secret transfer."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    host = os.environ.get('DEPLOY_HOST', '')
    user = os.environ.get('DEPLOY_USER', '')
    port = os.environ.get('DEPLOY_PORT', '') or '22'
    release = os.environ.get('DEPLOY_RELEASE', '')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]*', host) or not re.fullmatch(r'[a-z_][a-z0-9_-]*', user):
        raise RuntimeError('Set DEPLOY_HOST and DEPLOY_USER variables')
    if not port.isdigit() or not 1 <= int(port) <= 65535 or not re.fullmatch(r'[0-9]+-[0-9]+-[a-f0-9]{40}', release):
        raise RuntimeError('Invalid deployment port or release ID')
    for key in ('DEPLOY_SSH_KEY', 'DEPLOY_KNOWN_HOSTS', 'BOT_TOKEN', 'BOT_IMAGE'):
        if not os.environ.get(key):
            raise RuntimeError('Missing deployment setting: ' + key)
    payload = json.dumps({
        'image': os.environ['BOT_IMAGE'], 'bot_token': os.environ['BOT_TOKEN'],
        'gemini_api_key': os.environ.get('GEMINI_API_KEY', ''),
        'allowed_chat_ids': os.environ.get('ALLOWED_CHAT_IDS', ''),
        'registry_user': os.environ['GITHUB_ACTOR'],
        'registry_token': os.environ['REGISTRY_TOKEN'],
    })
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
        destination = '/opt/movie-helper/releases/' + release
        subprocess.run([*ssh, 'umask 077; mkdir -m 700 ' + destination], check=True)
        subprocess.run(['scp', *options, '-P', port, 'deploy/remote.py', 'deploy/compose.yaml',
                        f'{user}@{host}:{destination}/'], check=True)
        try:
            subprocess.run([*ssh, 'python3 -u ' + destination + '/remote.py'], input=payload, text=True, check=True)
        finally:
            subprocess.run(['ssh', *options, '-p', port, '-O', 'exit', f'{user}@{host}'],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError:
        raise SystemExit('Deployment transport or remote execution failed') from None
    except RuntimeError as err:
        raise SystemExit(str(err)) from None
