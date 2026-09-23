#!/usr/bin/env python3
"""Receive deployment settings on stdin, never in shell arguments or logs."""
import fcntl
import gzip
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

BASE = Path('/opt/movie-helper')
PROJECT = 'movie-helper'
VOLUME = 'movie-helper-data'


class DeployError(Exception):
    pass


def run(args, *, data=None, env=None):
    # External tools can echo configuration, so do not forward their output on error.
    result = subprocess.run(args, input=data, capture_output=True, text=True, env=env)
    if result.returncode:
        raise DeployError('Command failed: ' + ' '.join(args[:2]))
    return result.stdout.strip()


def secure_write(path, text):
    with open(path, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as f:
        f.write(text)
    path.chmod(0o600)


def runtime_settings(data, previous=None):
    token = data.get('bot_token', '')
    if not isinstance(token, str) or not re.fullmatch(r'[0-9]+:[A-Za-z0-9_-]+', token):
        raise DeployError('BOT_TOKEN is missing or invalid')
    chats = data.get('allowed_chat_ids', '').strip()
    if not chats:
        chats = 'bootstrap'
        if previous and (previous / 'runtime.env').is_file():
            for line in (previous / 'runtime.env').read_text().splitlines():
                if line.startswith('ALLOWED_CHAT_IDS='):
                    chats = line.split('=', 1)[1]
    if chats != 'bootstrap':
        parts = [v.strip() for v in chats.split(',')]
        if not parts or any(not re.fullmatch(r'-[1-9][0-9]*', v) or int(v) < -(2**63) for v in parts):
            raise DeployError('Invalid ALLOWED_CHAT_IDS')
        chats = ','.join(parts)
    settings = {'BOT_TOKEN': token, 'GEMINI_API_KEY': data.get('gemini_api_key', ''),
                'GROQ_API_KEY': data.get('groq_api_key', ''),
                'TMDB_API_TOKEN': data.get('tmdb_api_token', ''), 'ALLOWED_CHAT_IDS': chats}
    # raw env_file preserves dollar signs and quotes. Reject line injection.
    for value in settings.values():
        if not isinstance(value, str) or any(c in value for c in '\r\n\x00'):
            raise DeployError('Invalid multiline runtime setting')
    return ''.join(f'{k}={v}\n' for k, v in settings.items())


def image_reference(data):
    image = data.get('image', '')
    if not isinstance(image, str) or not re.fullmatch(r'ghcr\.io/vaporon4a/movie-helper@sha256:[a-f0-9]{64}', image):
        raise DeployError('Expected movie-helper image digest')
    return image


def compose(release, *args):
    return run(['docker', 'compose', '--project-name', PROJECT,
                '--env-file', str(release / 'image.env'),
                '-f', str(release / 'compose.yaml'), *args])


def container():
    ids = run(['docker', 'ps', '-aq', '--filter', f'label=com.docker.compose.project={PROJECT}',
               '--filter', 'label=com.docker.compose.service=bot']).splitlines()
    if len(ids) > 1:
        raise DeployError('Multiple bot containers found; inspect before deployment')
    return ids[0] if ids else None


def backup(cid, destination):
    with destination.open('xb') as out, gzip.GzipFile(fileobj=out, mode='wb') as gz:
        proc = subprocess.Popen(['docker', 'cp', cid + ':/data/.', '-'], stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, cwd=destination.parent)
        try:
            while block := proc.stdout.read(1024 * 1024):
                gz.write(block)
        finally:
            proc.stdout.close()
        if proc.wait():
            raise DeployError('Database backup failed')


def wait_ready(cid, expected):
    for _ in range(18):
        state = json.loads(run(['docker', 'inspect', '--format',
                              '{"state":{{json .State}},"restarts":{{.RestartCount}},"image":{{json .Config.Image}}}', cid]))
        if state['image'] != expected or state['restarts'] or state['state']['Status'] != 'running':
            raise DeployError('Bot exited or restarted; inspect server logs')
        logs = run(['docker', 'logs', '--tail', '100', cid])
        if '"msg":"bot started"' in logs:
            time.sleep(3)
            check = run(['docker', 'inspect', '--format', '{{.State.Running}} {{.RestartCount}}', cid])
            if check != 'true 0':
                raise DeployError('Bot did not remain running')
            return
        time.sleep(5)
    raise DeployError('Bot initialization timed out')


def deploy(data, release):
    previous = (BASE / 'current').resolve() if (BASE / 'current').is_symlink() else None
    runtime = runtime_settings(data, previous)
    image = image_reference(data)
    secure_write(release / 'runtime.env', runtime)
    secure_write(release / 'image.env', 'BOT_IMAGE=' + image + '\n')
    compose(release, 'config', '--quiet')
    # A job-scoped registry token exists only during pull, never in a release or image.
    with tempfile.TemporaryDirectory(prefix='registry-', dir=BASE) as tmp:
        env = dict(os.environ, DOCKER_CONFIG=tmp)
        if data.get('registry_token'):
            user = data.get('registry_user', '')
            if not re.fullmatch(r'[A-Za-z0-9_-]+', user):
                raise DeployError('Invalid registry user')
            run(['docker', 'login', 'ghcr.io', '--username', user, '--password-stdin'],
                data=data['registry_token'] + '\n', env=env)
        run(['docker', 'pull', image], env=env)
    cid = container()
    volumes = run(['docker', 'volume', 'ls', '--format', '{{.Name}}']).splitlines()
    if cid and previous is None:
        raise DeployError('Existing bot has no managed release; refusing to replace it')
    if not cid and VOLUME in volumes:
        raise DeployError('Unattached database volume exists; inspect before deployment')
    if cid:
        # Never archive a different service's data, even if labels were copied.
        mounts = json.loads(run(['docker', 'inspect', '--format', '{{json .Mounts}}', cid]))
        if not any(m.get('Name') == VOLUME and m.get('Destination') == '/data' for m in mounts):
            raise DeployError('Unexpected database volume')
        running = run(['docker', 'inspect', '--format', '{{.State.Running}}', cid]) == 'true'
        run(['docker', 'stop', '--time', '45', cid])
        try:
            backup_dir = BASE / 'backups'
            backup_dir.mkdir(mode=0o700, exist_ok=True)
            backup(cid, backup_dir / (release.name + '.tar.gz'))
        except Exception:
            # No new image has run, so the previous schema is still safe.
            if running:
                run(['docker', 'start', cid])
            raise
    link = BASE / 'current.next'
    link.unlink(missing_ok=True)
    link.symlink_to(release)
    link.replace(BASE / 'current')
    if previous:
        secure_write(release / 'previous.txt', str(previous) + '\n')
    try:
        compose(release, 'up', '-d', '--no-build', '--pull', 'never', '--force-recreate', 'bot')
        cid = container()
        if not cid:
            raise DeployError('Bot container missing after deployment')
        wait_ready(cid, image)
    except Exception:
        # Startup may have migrated SQLite. Stop the new bot; do not silently
        # restart an old binary against a newer schema.
        try:
            compose(release, 'stop', 'bot')
        except DeployError:
            pass
        raise DeployError('New bot failed readiness; stopped. Previous release and backup retained') from None
    print('Deployment ready: ' + image, flush=True)
    print('Chat mode: ' + ('bootstrap (/id only)' if 'ALLOWED_CHAT_IDS=bootstrap\n' in runtime else 'allowlist configured'), flush=True)


def main():
    os.umask(0o077)
    release = Path(__file__).resolve().parent
    if release.parent != BASE / 'releases':
        raise DeployError('Unexpected release directory')
    with (BASE / 'deploy.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise DeployError('Another deployment is already running') from None
        raw = sys.stdin.read(65537)
        if len(raw) > 65536:
            raise DeployError('Deployment input too large')
        try:
            data = json.loads(raw)
        except ValueError:
            raise DeployError('Invalid deployment input') from None
        if not isinstance(data, dict):
            raise DeployError('Invalid deployment input')
        deploy(data, release)


if __name__ == '__main__':
    try:
        main()
    except Exception as err:
        # Only deliberate, sanitised errors are safe to print.
        print(str(err) if isinstance(err, DeployError) else 'Deployment failed; inspect server state', file=sys.stderr)
        sys.exit(1)
