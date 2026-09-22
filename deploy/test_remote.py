import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import remote

IMAGE = 'ghcr.io/vaporon4a/movie-helper@sha256:' + 'a' * 64
PAYLOAD = {'bot_token': '123:test', 'image': IMAGE, 'gemini_api_key': 'quoted"$value', 'groq_api_key': 'groq"$value'}


class ConfigurationTests(unittest.TestCase):
    def test_bootstrap_and_existing_allowlist(self):
        self.assertIn('ALLOWED_CHAT_IDS=bootstrap\n', remote.runtime_settings(PAYLOAD))
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp)
            (p / 'runtime.env').write_text('ALLOWED_CHAT_IDS=-123,-456\n')
            self.assertIn('ALLOWED_CHAT_IDS=-123,-456\n', remote.runtime_settings(PAYLOAD, p))
        value = remote.runtime_settings(dict(PAYLOAD, allowed_chat_ids=' -5, -6 '))
        self.assertIn('ALLOWED_CHAT_IDS=-5,-6\n', value)
        self.assertIn('GEMINI_API_KEY=quoted"$value\n', value)
        self.assertIn('GROQ_API_KEY=groq"$value\n', value)

    def test_reject_line_injection_bad_ids_and_image(self):
        for change in ({'bot_token': ''}, {'gemini_api_key': 'x\nINJECT=y'}, {'groq_api_key': 'x\nINJECT=y'},
                       {'allowed_chat_ids': '123'}, {'allowed_chat_ids': '-1\nX=y'},
                       {'allowed_chat_ids': str(-(2**64))}):
            with self.subTest(change=change), self.assertRaises(remote.DeployError):
                remote.runtime_settings(dict(PAYLOAD, **change))
        for image in ('nginx:latest', 'ghcr.io/vaporon4a/movie-helper:main', IMAGE + '; ls'):
            with self.assertRaises(remote.DeployError):
                remote.image_reference({'image': image})


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.release = self.base / 'releases' / 'new'
        self.release.mkdir(parents=True)
        self.previous = self.base / 'releases' / 'old'
        self.previous.mkdir()
        (self.previous / 'runtime.env').write_text('ALLOWED_CHAT_IDS=-123\n')
        (self.base / 'current').symlink_to(self.previous)
        self.calls = []
        self.puller_fails = False

    def fake_run(self, args, **kwargs):
        self.calls.append(args)
        if args[:2] == ['docker', 'pull'] and self.puller_fails:
            raise remote.DeployError('pull failed')
        if args[:2] == ['docker', 'ps']:
            return 'old-container'
        if args[:3] == ['docker', 'volume', 'ls']:
            return remote.VOLUME
        if args[:2] == ['docker', 'inspect']:
            if 'Mounts' in args[3]:
                return json.dumps([{'Name': remote.VOLUME, 'Destination': '/data'}])
            return 'true'
        return ''

    def patches(self):
        self.enterContext(patch.object(remote, 'BASE', self.base))
        self.enterContext(patch.object(remote, 'run', self.fake_run))
        compose = self.enterContext(patch.object(remote, 'compose'))
        backup = self.enterContext(patch.object(remote, 'backup'))
        ready = self.enterContext(patch.object(remote, 'wait_ready'))
        return compose, backup, ready

    def test_first_release_starts_in_bootstrap(self):
        compose, backup, ready = self.patches()
        (self.base / 'current').unlink()
        with patch.object(remote, 'container', side_effect=[None, 'new-container']), patch.object(remote, 'run', return_value=''):
            remote.deploy(PAYLOAD, self.release)
        backup.assert_not_called()
        ready.assert_called_once_with('new-container', IMAGE)
        self.assertIn('ALLOWED_CHAT_IDS=bootstrap\n', (self.release / 'runtime.env').read_text())

    def test_pull_failure_keeps_running_bot(self):
        self.patches()
        self.puller_fails = True
        with self.assertRaises(remote.DeployError):
            remote.deploy(PAYLOAD, self.release)
        self.assertFalse(any(c[:2] == ['docker', 'stop'] for c in self.calls))
        self.assertEqual((self.base / 'current').resolve(), self.previous)

    def test_backup_failure_restarts_old_bot_before_any_migration(self):
        compose, backup, _ = self.patches()
        backup.side_effect = remote.DeployError('backup failed')
        with self.assertRaises(remote.DeployError):
            remote.deploy(PAYLOAD, self.release)
        self.assertIn(['docker', 'start', 'old-container'], self.calls)
        self.assertFalse(any(c.args[1] == 'up' for c in compose.call_args_list))
        self.assertEqual((self.base / 'current').resolve(), self.previous)

    def test_success_uses_backup_and_preserves_secret_permissions(self):
        compose, backup, ready = self.patches()
        remote.deploy(PAYLOAD, self.release)
        self.assertIn(['docker', 'stop', '--time', '45', 'old-container'], self.calls)
        backup.assert_called_once()
        ready.assert_called_once_with('old-container', IMAGE)
        self.assertEqual((self.base / 'current').resolve(), self.release)
        self.assertEqual((self.release / 'runtime.env').stat().st_mode & 0o777, 0o600)
        self.assertEqual((self.release / 'previous.txt').read_text().strip(), str(self.previous))
        self.assertTrue(any(c.args[1:4] == ('up', '-d', '--no-build') for c in compose.call_args_list))

    def test_failed_new_bot_is_stopped_without_unsafe_old_schema_restart(self):
        compose, _, ready = self.patches()
        ready.side_effect = remote.DeployError('startup failed')
        with self.assertRaises(remote.DeployError):
            remote.deploy(PAYLOAD, self.release)
        compose.assert_called_with(self.release, 'stop', 'bot')
        self.assertNotIn(['docker', 'start', 'old-container'], self.calls)
        self.assertTrue((self.release / 'previous.txt').exists())


if __name__ == '__main__':
    unittest.main()
