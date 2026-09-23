#!/usr/bin/env python3
"""Prompt locally on the server; never print credentials or pass them in argv."""
import getpass
import os
from pathlib import Path
import shutil
import subprocess
import sys

CONFIG = Path('/opt/movie-helper/secrets/rclone.conf')
BUCKET = 'movie-helper-backups-20260923'


def configuration(account, key, password):
    for value in (account, key):
        if not value or any(c.isspace() or ord(c) < 32 for c in value):
            raise ValueError('Invalid B2 credential; no whitespace allowed')
    if len(password) < 24 or any(c in password for c in '\r\n\x00'):
        raise ValueError('Use a saved encryption password of at least 24 characters')
    # obscure is reversible protection for the config, not encryption at rest.
    # Actual archive encryption is provided by the crypt backend.
    result = subprocess.run(['rclone', 'obscure', '-'], input=password + '\n',
                            text=True, capture_output=True, check=False)
    if result.returncode or not result.stdout.strip():
        raise RuntimeError('rclone could not prepare the encryption password')
    return (f'[b2]\ntype = b2\naccount = {account}\nkey = {key}\n'
            'hard_delete = true\n\n'
            f'[backup]\ntype = crypt\nremote = b2:{BUCKET}/archives\n'
            'filename_encryption = standard\ndirectory_name_encryption = true\n'
            f'password = {result.stdout.strip()}\n')


def main():
    os.umask(0o077)
    if not sys.stdin.isatty():
        raise RuntimeError('Run interactively in your SSH terminal')
    if not shutil.which('rclone'):
        raise RuntimeError('Install rclone first')
    if CONFIG.exists() or CONFIG.is_symlink():
        raise RuntimeError('Config already exists; refusing to overwrite credentials')
    if CONFIG.parent.is_symlink():
        raise RuntimeError('Secret directory must not be a symlink')
    print('Save a unique random encryption password (at least 24 characters) in your password manager first.')
    print('Without that password, the archives cannot be restored after loss of the server.')
    account = getpass.getpass('B2 keyID (hidden): ').strip()
    key = getpass.getpass('B2 applicationKey (hidden): ').strip()
    password = getpass.getpass('Saved archive encryption password (hidden): ')
    if getpass.getpass('Repeat encryption password (hidden): ') != password:
        raise ValueError('Encryption passwords do not match; nothing saved')
    content = configuration(account, key, password)
    CONFIG.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    CONFIG.parent.chmod(0o700)
    with CONFIG.open('x') as target:
        target.write(content)
        target.flush()
        os.fsync(target.fileno())
    CONFIG.chmod(0o600)
    print(f'Saved {CONFIG} with permissions 0600. No backup scheduled yet.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError) as err:
        raise SystemExit(str(err)) from None
    except (KeyboardInterrupt, EOFError):
        raise SystemExit('Setup cancelled') from None
    except Exception:
        raise SystemExit('Setup failed; check directory permissions and available disk space') from None
