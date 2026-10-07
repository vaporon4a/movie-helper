#!/usr/bin/env python3
"""Publish only announcements belonging to the verified current deployment."""
import fcntl
import json
import os
from pathlib import Path
import re
import sys
import urllib.error
import urllib.request

import remote
from announcements import AnnouncementError, fingerprint, pending, render


def write_ledger(path, ledger):
    temporary = path.with_suffix('.next')
    remote.secure_write(temporary, json.dumps(ledger, ensure_ascii=False))
    temporary.replace(path)


def send_telegram(token, chat_id, text):
    request = urllib.request.Request('https://api.telegram.org/bot' + token + '/sendMessage',
        data=json.dumps({'chat_id': chat_id, 'text': text, 'link_preview_options': {'is_disabled': True}}).encode(),
        headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            result = json.load(response)
    except urllib.error.HTTPError as error:
        # Only explicit client rejection proves the message was not delivered.
        if error.code in (400, 401, 403, 404, 429):
            raise Rejected('Telegram rejected announcement: HTTP ' + str(error.code)) from None
        raise AnnouncementError('Telegram delivery uncertain; inspect channel before retry') from None
    except Exception:
        raise AnnouncementError('Telegram delivery uncertain; inspect channel before retry') from None
    if result.get('ok') is not True or not isinstance(result.get('result', {}).get('message_id'), int):
        raise AnnouncementError('Telegram delivery uncertain; inspect channel before retry')
    return result['result']['message_id']


class Rejected(AnnouncementError):
    pass


def verify_running(cid, image):
    # Startup logs can rotate away; ready.json proves deployment initialization.
    state = json.loads(remote.run(['docker', 'inspect', '--format',
        '{"running":{{.State.Running}},"restarts":{{.RestartCount}},"image":{{json .Config.Image}}}', cid]))
    if state.get('running') is not True or state.get('restarts') != 0 or state.get('image') != image:
        raise AnnouncementError('Current bot does not match a healthy verified deployment')


def configured_chats(release):
    value = ''
    for line in (release / 'runtime.env').read_text().splitlines():
        if line.startswith('ALLOWED_CHAT_IDS='):
            value = line.split('=', 1)[1]
    if value == 'bootstrap':
        return []
    chats = list(dict.fromkeys(part.strip() for part in value.split(',')))
    if not chats or any(not re.fullmatch(r'-[1-9][0-9]*', chat) for chat in chats):
        raise AnnouncementError('Deployed ALLOWED_CHAT_IDS missing or invalid')
    return chats


def publish(data):
    sha, chat_id = data.get('sha', ''), data.get('chat_id', '')
    if not re.fullmatch(r'[a-f0-9]{40}', sha):
        raise AnnouncementError('Expected commit SHA')
    if type(data.get('publish')) is not bool:
        raise AnnouncementError('Invalid publish flag')
    release = (remote.BASE / 'current').resolve()
    if release.parent != remote.BASE / 'releases' or not release.name.endswith('-' + sha):
        raise AnnouncementError('Selected commit is not the current deployed version')
    if not chat_id:
        chats = configured_chats(release)
        results, failures = [], []
        for chat in chats:
            try:
                results.append(publish(dict(data, chat_id=chat)))
            except AnnouncementError as error:
                results.append({'status': 'failed'})
                failures.append('Chat ' + chat + ': ' + str(error))
        if failures:
            raise AnnouncementError('; '.join(failures))
        return {'status': 'sent' if data['publish'] else 'preview', 'sha': sha,
                'text': '\n\n'.join('Chat ' + chat + ': ' + result['status'] + '\n' + result.get('text', '')
                                    for chat, result in zip(chats, results))}
    if not re.fullmatch(r'-[1-9][0-9]*', chat_id):
        raise AnnouncementError('Expected negative chat ID')
    marker = release / 'ready.json'
    if not marker.is_file():
        raise AnnouncementError('Deploy this version with announcement support first')
    ready = json.loads(marker.read_text())
    image = remote.image_reference(ready)
    cid = remote.container()
    if not cid:
        raise AnnouncementError('Bot container missing')
    verify_running(cid, image)
    entries = json.loads((release / 'announcements.json').read_text())
    path = remote.BASE / 'announcement-ledger.json'
    ledger = json.loads(path.read_text()) if path.exists() else {}
    channel = ledger.setdefault(chat_id, {})
    selected = pending(entries, channel)
    if not selected:
        return {'status': 'nothing_pending', 'sha': sha}
    text = render(selected)
    result = {'status': 'preview', 'sha': sha, 'ids': [entry['id'] for entry in selected], 'text': text}
    if not data['publish']:
        return result
    token = ''
    for line in (release / 'runtime.env').read_text().splitlines():
        if line.startswith('BOT_TOKEN='):
            token = line.split('=', 1)[1]
    if not re.fullmatch(r'[0-9]+:[A-Za-z0-9_-]+', token):
        raise AnnouncementError('Deployed BOT_TOKEN is missing or invalid')
    # Persist before the network call. A crash or timeout cannot cause an automatic duplicate.
    for entry in selected:
        channel[entry['id']] = {'hash': fingerprint(entry), 'state': 'unknown', 'sha': sha}
    write_ledger(path, ledger)
    try:
        message_id = send_telegram(token, chat_id, text)
    except Rejected:
        for entry in selected:
            del channel[entry['id']]
        write_ledger(path, ledger)
        raise
    for entry in selected:
        channel[entry['id']].update(state='sent', message_id=message_id)
    write_ledger(path, ledger)
    result.update(status='sent', message_id=message_id)
    return result


def main():
    os.umask(0o077)
    with (remote.BASE / 'deploy.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        data = json.loads(sys.stdin.read(4096))
        print(json.dumps(publish(data), ensure_ascii=False))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(str(error) if isinstance(error, (AnnouncementError, remote.DeployError)) else
              'Announcement failed; inspect server state', file=sys.stderr)
        sys.exit(1)
