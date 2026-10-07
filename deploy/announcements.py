"""Validated, immutable user-facing announcements; no Telegram markup."""
import hashlib
from pathlib import Path
import re


class AnnouncementError(Exception):
    pass


def validate_entries(entries):
    if not isinstance(entries, list) or len(entries) > 500:
        raise AnnouncementError('Invalid announcement catalog')
    if sum(len(str(entry).encode('utf-8')) for entry in entries) > 48000:
        raise AnnouncementError('Announcement catalog exceeds deployment payload budget')
    seen = set()
    for entry in entries:
        if not isinstance(entry, dict):
            raise AnnouncementError('Invalid announcement entry')
        name, text = entry.get('id'), entry.get('text')
        if not isinstance(name, str) or not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,79}', name) or name in seen:
            raise AnnouncementError('Invalid or duplicate announcement ID')
        if not isinstance(text, str) or not text.strip() or len(text) > 1500 or any(ord(c) < 32 and c != '\n' for c in text):
            raise AnnouncementError('Announcement must contain 1..1500 printable characters')
        seen.add(name)
    return entries


def catalog(directory):
    return validate_entries([{'id': path.stem, 'text': path.read_text(encoding='utf-8').strip()}
                             for path in sorted(Path(directory).glob('*.txt'))])


def fingerprint(entry):
    return hashlib.sha256(entry['text'].encode('utf-8')).hexdigest()


def pending(entries, ledger):
    result = []
    for entry in validate_entries(entries):
        old = ledger.get(entry['id'])
        if old:
            if old.get('hash') != fingerprint(entry):
                raise AnnouncementError('Published announcement changed; use a new ID')
            if old.get('state') == 'unknown':
                raise AnnouncementError('Delivery is uncertain; inspect channel and resolve server ledger before retry')
            if old.get('state') != 'sent':
                raise AnnouncementError('Invalid announcement delivery state')
        else:
            result.append(entry)
    return result


def render(entries):
    text = '🎬 Бот обновился!\n\n' + '\n\n'.join(entry['text'] for entry in entries)
    if len(text.encode('utf-16-le')) // 2 > 3500:
        raise AnnouncementError('Summary exceeds 3500 Telegram characters; reduce pending announcements')
    return text
