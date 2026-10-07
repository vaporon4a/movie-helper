#!/usr/bin/env python3
"""Actions announcement entrypoint; remote stdout contains only reviewed public text."""
import json
import os
from pathlib import Path
import re
import subprocess

from ssh_transport import session


def main():
    sha = os.environ.get('GITHUB_SHA', '')
    run_id = os.environ.get('GITHUB_RUN_ID', '')
    attempt = os.environ.get('GITHUB_RUN_ATTEMPT', '')
    chat_id = os.environ.get('ANNOUNCEMENT_CHAT_ID', '')
    publish = os.environ.get('ANNOUNCEMENT_PUBLISH', 'false')
    if not re.fullmatch(r'[a-f0-9]{40}', sha) or not run_id.isdigit() or not attempt.isdigit():
        raise RuntimeError('Invalid Actions revision or run ID')
    if (chat_id and not re.fullmatch(r'-[1-9][0-9]*', chat_id)) or publish not in ('true', 'false'):
        raise RuntimeError('Invalid announcement settings')
    destination = '/opt/movie-helper/announcement-jobs/' + run_id + '-' + attempt
    payload = json.dumps({'sha': sha, 'chat_id': chat_id, 'publish': publish == 'true'})
    with session() as (ssh, scp, target):
        subprocess.run([*ssh, 'umask 077; mkdir -p ' + destination], check=True)
        try:
            subprocess.run([*scp, 'deploy/announce_remote.py', 'deploy/announcements.py', 'deploy/remote.py',
                            target + ':' + destination + '/'], check=True)
            result = subprocess.run([*ssh, 'python3 ' + destination + '/announce_remote.py'],
                                    input=payload, capture_output=True, text=True)
            if result.returncode:
                raise RuntimeError('Announcement failed: ' + result.stderr.strip())
            data = json.loads(result.stdout)
            summary = '# Bot announcement\n\nStatus: ' + data['status'] + '\n\n'
            summary += 'Deployed commit: `' + data['sha'] + '`\n\n' + data.get('text', '') + '\n'
            if os.environ.get('GITHUB_STEP_SUMMARY'):
                with Path(os.environ['GITHUB_STEP_SUMMARY']).open('a') as output:
                    output.write(summary)
            print('Announcement status: ' + data['status'])
        finally:
            subprocess.run([*ssh, 'rm -rf ' + destination], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError) as error:
        raise SystemExit(str(error) if isinstance(error, RuntimeError) else 'Announcement SSH transport failed') from None
