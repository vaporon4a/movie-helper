#!/usr/bin/env python3
"""Stop, snapshot, restart, encrypt/upload, verify and retain daily backups."""
import configparser
from datetime import datetime, timezone
import fcntl
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import sqlite3
import stat
import subprocess
import sys
import tarfile
import tempfile
import uuid

from configure_backup import BUCKET, CONFIG
import remote

NAME = re.compile(r'daily-\d{8}T\d{6}Z-[a-f0-9]{8}\.tar\.gz')
KEEP = 30


def log(message):
    print(datetime.now(timezone.utc).isoformat(timespec='seconds') + ' ' + message, flush=True)


def check_config():
    if CONFIG.is_symlink() or not CONFIG.is_file():
        raise remote.DeployError('Missing regular rclone config; run configure_backup.py')
    info = CONFIG.stat()
    if stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.getuid():
        raise remote.DeployError('rclone config must be owned by the backup user with mode 0600')
    cfg = configparser.ConfigParser(interpolation=None)
    cfg.read(CONFIG)
    if (cfg.get('b2', 'type') != 'b2' or cfg.get('backup', 'type') != 'crypt'
            or cfg.get('backup', 'remote') != f'b2:{BUCKET}/archives'
            or not cfg.get('backup', 'password')
            or not cfg.get('b2', 'account') or not cfg.get('b2', 'key')):
        raise remote.DeployError('Unexpected backup remote configuration')


def rclone(*args):
    result = subprocess.run(['rclone', '--config', str(CONFIG), '--retries', '2',
                             '--contimeout', '20s', '--timeout', '60s', *args],
                            capture_output=True, text=True, timeout=600, check=False)
    if result.returncode:
        # Provider responses can include secrets or private object paths.
        raise remote.DeployError('rclone ' + args[0] + ' failed; check credentials, quota and connectivity')
    return result.stdout


def snapshot(destination):
    partial = destination.with_suffix(destination.suffix + '.partial')
    try:
        with (remote.BASE / 'deploy.lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            cid = remote.container()
            if not cid:
                raise remote.DeployError('Bot container is missing')
            mounts = json.loads(remote.run(['docker', 'inspect', '--format', '{{json .Mounts}}', cid]))
            if not any(m.get('Name') == remote.VOLUME and m.get('Destination') == '/data' for m in mounts):
                raise remote.DeployError('Unexpected database volume; bot not stopped')
            if remote.run(['docker', 'inspect', '--format', '{{.State.Running}}', cid]) != 'true':
                raise remote.DeployError('Bot was already stopped; refusing to change its state')
            try:
                remote.run(['docker', 'stop', '--time', '45', cid])
                remote.backup(cid, partial)
            finally:
                # Even if stop partially succeeds or archiving fails, restart
                # the same container, while still holding the deployment lock.
                remote.run(['docker', 'start', cid])
            if remote.run(['docker', 'inspect', '--format', '{{.State.Running}}', cid]) != 'true':
                raise remote.DeployError('Bot failed to restart after snapshot')
            partial.replace(destination)
    finally:
        partial.unlink(missing_ok=True)


def verify_archive(path):
    # Consume the entire stream to verify gzip CRC, including its final trailer.
    with gzip.open(path, 'rb') as stream:
        while stream.read(1024 * 1024):
            pass
    with tempfile.TemporaryDirectory(prefix='verify-', dir=path.parent) as folder:
        folder = Path(folder)
        found = set()
        with tarfile.open(path, 'r:gz') as archive:
            for member in archive:
                name = member.name.removeprefix('./')
                if name not in ('movie-helper.db', 'movie-helper.db-wal'):
                    continue
                if not member.isfile() or name in found:
                    raise remote.DeployError('Invalid database archive member')
                found.add(name)
                with archive.extractfile(member) as source, (folder / name).open('xb') as target:
                    while chunk := source.read(1024 * 1024):
                        target.write(chunk)
        if 'movie-helper.db' not in found:
            raise remote.DeployError('Archive does not contain movie-helper.db')
        db = sqlite3.connect((folder / 'movie-helper.db').as_uri() + '?mode=rw', uri=True)
        try:
            if db.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
                raise remote.DeployError('SQLite integrity check failed')
            if db.execute('PRAGMA foreign_key_check').fetchone() is not None:
                raise remote.DeployError('SQLite foreign key check failed')
        finally:
            db.close()


def digest(path):
    checksum = hashlib.sha256()
    with path.open('rb') as source:
        while chunk := source.read(1024 * 1024):
            checksum.update(chunk)
    return checksum.digest()


def expired_names(items):
    names = sorted({item['Name'] for item in items
                    if not item.get('IsDir') and NAME.fullmatch(item.get('Name', ''))})
    return names[:-KEEP]


def perform():
    check_config()
    # A failure here must leave the bot running, before any snapshot is taken.
    rclone('lsjson', 'backup:', '--files-only', '--max-depth', '1')
    directory = remote.BASE / 'backups' / 'daily'
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    name = datetime.now(timezone.utc).strftime('daily-%Y%m%dT%H%M%SZ-') + uuid.uuid4().hex[:8] + '.tar.gz'
    archive = directory / name
    log('Creating database snapshot')
    snapshot(archive)
    log('Bot restarted; checking SQLite snapshot')
    verify_archive(archive)
    rclone('copyto', str(archive), 'backup:' + name, '--immutable')
    # A download through crypt verifies that both credentials and decryption work.
    with tempfile.TemporaryDirectory(prefix='download-', dir=directory) as tmp:
        restored = Path(tmp) / 'restored.tar.gz'
        rclone('copyto', 'backup:' + name, str(restored))
        if digest(restored) != digest(archive):
            raise remote.DeployError('Downloaded backup differs; old backups retained')
    log('Encrypted upload and restore verified: ' + name)
    listing = json.loads(rclone('lsjson', 'backup:', '--files-only', '--max-depth', '1'))
    if not any(item.get('Name') == name for item in listing):
        raise remote.DeployError('New backup not listed; old backups retained')
    # Delete exact, owned names only; no sync, purge, wildcard or bucket-wide cleanup.
    for old in expired_names(listing):
        rclone('deletefile', 'backup:' + old, '--b2-hard-delete')
    # Keep failed/unuploaded snapshots too, unless their exact name exists in B2.
    uploaded = {item.get('Name') for item in listing}
    local = sorted(p for p in directory.iterdir() if p.is_file() and NAME.fullmatch(p.name))
    for old in local[:-KEEP]:
        if old.name in uploaded:
            old.unlink()
    remote.secure_write(remote.BASE / 'backup-last-success.json', json.dumps({
        'archive': name, 'verified_at': datetime.now(timezone.utc).isoformat(),
        'size_bytes': archive.stat().st_size,
    }) + '\n')
    log('Backup complete; retention applied')


def main():
    os.umask(0o077)
    def interrupted(_signum, _frame):
        raise InterruptedError('Backup interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    with (remote.BASE / 'backup.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise remote.DeployError('Another backup is already running') from None
        perform()


if __name__ == '__main__':
    try:
        main()
    except remote.DeployError as err:
        log('ERROR: ' + str(err))
        sys.exit(1)
    except BaseException:
        log('ERROR: Backup failed or interrupted; check bot state. Success marker not updated.')
        sys.exit(1)
