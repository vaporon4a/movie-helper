import json
from pathlib import Path
import tempfile
import unittest
import urllib.error
from unittest.mock import patch

import announcements
import announce_remote
import remote

SHA = 'a' * 40
IMAGE = 'ghcr.io/vaporon4a/movie-helper@sha256:' + 'b' * 64
ENTRIES = [{'id': 'feature-ideas', 'text': '• Предлагайте идеи через /idea.'},
           {'id': 'readable-facts', 'text': '• Факты о кино стали понятнее.'}]


class CatalogTests(unittest.TestCase):
    def test_catalog_checks_ids_empty_text_and_payload_budget(self):
        for entries in ([{'id': '../evil', 'text': 'text'}], [{'id': 'empty', 'text': ' '}],
                        ENTRIES + [ENTRIES[0]], [{'id': 'too-long', 'text': 'x' * 1501}],
                        [{'id': 'a', 'text': '\x00'}],
                        [{'id': f'entry-{i}', 'text': 'ф' * 1400} for i in range(30)]):
            with self.subTest(entries=len(entries)), self.assertRaises(announcements.AnnouncementError):
                announcements.validate_entries(entries)

    def test_catalog_reads_sorted_utf8_files_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            (path / 'b.txt').write_text('Русский текст\n')
            (path / 'a.txt').write_text('Первый')
            (path / 'README.md').write_text('documentation')
            self.assertEqual([entry['id'] for entry in announcements.catalog(path)], ['a', 'b'])

    def test_telegram_limit_counts_utf16(self):
        with self.assertRaises(announcements.AnnouncementError):
            announcements.render([{'text': '🎬' * 1800}])


class ReadinessTests(unittest.TestCase):
    def test_requires_exact_image_running_and_no_restarts(self):
        valid = {'running': True, 'restarts': 0, 'image': IMAGE}
        with patch.object(remote, 'run', return_value=json.dumps(valid)):
            announce_remote.verify_running('bot', IMAGE)
        for change in ({'running': False}, {'restarts': 1}, {'image': 'different'}):
            with patch.object(remote, 'run', return_value=json.dumps(dict(valid, **change))), self.assertRaises(
                    announcements.AnnouncementError):
                announce_remote.verify_running('bot', IMAGE)


class TelegramTests(unittest.TestCase):
    def test_explicit_rate_limit_rejection_is_safe_to_retry(self):
        error = urllib.error.HTTPError('https://example.invalid', 429, 'rate limit', {}, None)
        with patch('urllib.request.urlopen', side_effect=error), self.assertRaises(announce_remote.Rejected):
            announce_remote.send_telegram('123:test', '-1', 'Hello')

    def test_server_errors_and_timeouts_are_uncertain_without_token_leak(self):
        for error in (TimeoutError('token-secret'),
                      urllib.error.HTTPError('https://example.invalid', 503, 'token-secret', {}, None)):
            with patch('urllib.request.urlopen', side_effect=error), self.assertRaisesRegex(
                    announcements.AnnouncementError, '^Telegram delivery uncertain; inspect channel before retry$'):
                announce_remote.send_telegram('123:token-secret', '-1', 'Hello')

    def test_success_payload_disables_link_previews_and_has_no_parse_mode(self):
        with patch('urllib.request.urlopen') as opener:
            opener.return_value.__enter__.return_value.read.return_value = b'{"ok":true,"result":{"message_id":7}}'
            self.assertEqual(announce_remote.send_telegram('123:test', '-1', 'Hello'), 7)
            data = json.loads(opener.call_args.args[0].data)
            self.assertNotIn('parse_mode', data)
            self.assertTrue(data['link_preview_options']['is_disabled'])


class PublicationTests(unittest.TestCase):
    def setUp(self):
        tmp = self.enterContext(tempfile.TemporaryDirectory())
        self.base = Path(tmp)
        self.release = self.base / 'releases' / ('123-1-' + SHA)
        self.release.mkdir(parents=True)
        (self.base / 'current').symlink_to(self.release)
        (self.release / 'ready.json').write_text(json.dumps({'image': IMAGE}))
        (self.release / 'announcements.json').write_text(json.dumps(ENTRIES))
        (self.release / 'runtime.env').write_text('BOT_TOKEN=123:test\nALLOWED_CHAT_IDS=-100123,-200\n')
        self.enterContext(patch.object(remote, 'BASE', self.base))
        self.enterContext(patch.object(remote, 'container', return_value='bot'))
        self.ready = self.enterContext(patch.object(announce_remote, 'verify_running'))
        self.sender = self.enterContext(patch.object(announce_remote, 'send_telegram', return_value=42))
        self.data = {'sha': SHA, 'chat_id': '-100123', 'publish': False}
        self.ledger = self.base / 'announcement-ledger.json'

    def publish(self, **change):
        return announce_remote.publish(dict(self.data, **change))

    def test_preview_does_not_send_or_mark_entries(self):
        result = self.publish()
        self.assertEqual(result['status'], 'preview')
        self.assertIn('/idea', result['text'])
        self.sender.assert_not_called()
        self.assertFalse(self.ledger.exists())
        self.ready.assert_called_once_with('bot', IMAGE)

    def test_success_deduplicates_and_keeps_new_entries_pending(self):
        self.assertEqual(self.publish(publish=True)['message_id'], 42)
        self.assertEqual(self.publish(publish=True)['status'], 'nothing_pending')
        self.sender.assert_called_once()
        ledger = json.loads(self.ledger.read_text())
        self.assertEqual(ledger['-100123']['feature-ideas']['state'], 'sent')
        self.assertEqual(self.ledger.stat().st_mode & 0o777, 0o600)
        extra = {'id': 'next-feature', 'text': 'Следующая фича'}
        (self.release / 'announcements.json').write_text(json.dumps(ENTRIES + [extra]))
        self.assertEqual(self.publish()['ids'], ['next-feature'])

    def test_auto_uses_deployed_allowlist_and_deduplicates_each_chat(self):
        self.publish(chat_id='', publish=True)
        self.assertEqual(self.sender.call_count, 2)
        self.publish(chat_id='', publish=True)
        self.assertEqual(self.sender.call_count, 2)
        ledger = json.loads(self.ledger.read_text())
        self.assertEqual(set(ledger), {'-100123', '-200'})

    def test_partial_failure_retries_only_unsent_chats(self):
        self.sender.side_effect = [42, announce_remote.Rejected('403')]
        with self.assertRaises(announcements.AnnouncementError):
            self.publish(chat_id='', publish=True)
        self.sender.side_effect = None
        self.publish(chat_id='', publish=True)
        self.assertEqual(self.sender.call_count, 3)

    def test_first_chat_failure_does_not_block_other_chats(self):
        self.sender.side_effect = [announce_remote.Rejected('403'), 42]
        with self.assertRaises(announcements.AnnouncementError):
            self.publish(chat_id='', publish=True)
        self.assertEqual(self.sender.call_count, 2)
        ledger = json.loads(self.ledger.read_text())
        self.assertEqual(ledger['-200']['feature-ideas']['state'], 'sent')
        self.assertEqual(ledger['-100123'], {})

    def test_different_channel_has_separate_history(self):
        self.publish(publish=True)
        self.assertEqual(self.publish(chat_id='-200')['ids'], [entry['id'] for entry in ENTRIES])

    def test_unknown_delivery_blocks_retry(self):
        self.sender.side_effect = TimeoutError('transport failed')
        with self.assertRaises(TimeoutError):
            self.publish(publish=True)
        self.sender.side_effect = None
        with self.assertRaisesRegex(announcements.AnnouncementError, 'uncertain'):
            self.publish(publish=True)
        self.sender.assert_called_once()

    def test_explicit_rejection_allows_manual_retry(self):
        self.sender.side_effect = announce_remote.Rejected('429')
        with self.assertRaises(announce_remote.Rejected):
            self.publish(publish=True)
        self.assertEqual(self.publish()['status'], 'preview')
        self.sender.side_effect = None
        self.assertEqual(self.publish(publish=True)['status'], 'sent')

    def test_mark_before_send_handles_interruption(self):
        def inspect_before_send(*args):
            ledger = json.loads(self.ledger.read_text())
            self.assertEqual(ledger['-100123']['feature-ideas']['state'], 'unknown')
            return 42
        self.sender.side_effect = inspect_before_send
        self.publish(publish=True)

    def test_wrong_commit_failed_readiness_and_missing_marker_never_send(self):
        with self.assertRaises(announcements.AnnouncementError):
            self.publish(sha='b' * 40, publish=True)
        self.ready.side_effect = remote.DeployError('not ready')
        with self.assertRaises(remote.DeployError):
            self.publish(publish=True)
        self.ready.side_effect = None
        (self.release / 'ready.json').unlink()
        with self.assertRaises(announcements.AnnouncementError):
            self.publish(publish=True)
        self.sender.assert_not_called()

    def test_published_text_cannot_be_changed(self):
        self.publish(publish=True)
        (self.release / 'announcements.json').write_text(json.dumps([dict(ENTRIES[0], text='Changed')]))
        with self.assertRaisesRegex(announcements.AnnouncementError, 'changed'):
            self.publish(publish=True)
        self.sender.assert_called_once()

    def test_overflow_never_reserves_or_sends(self):
        entries = [{'id': f'entry-{i}', 'text': 'x' * 1200} for i in range(3)]
        (self.release / 'announcements.json').write_text(json.dumps(entries))
        with self.assertRaises(announcements.AnnouncementError):
            self.publish(publish=True)
        self.assertFalse(self.ledger.exists())
        self.sender.assert_not_called()


if __name__ == '__main__':
    unittest.main()
