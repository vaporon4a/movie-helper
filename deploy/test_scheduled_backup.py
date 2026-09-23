import json
from pathlib import Path
import sqlite3
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import remote
import scheduled_backup as backup


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.enterContext(patch.object(remote, 'BASE', self.base))
        self.calls = []
        self.volume = remote.VOLUME
        self.running = 'true'

    def docker(self, args):
        self.calls.append(args)
        if args[:2] == ['docker', 'inspect']:
            if 'Mounts' in args[3]:
                return json.dumps([{'Name': self.volume, 'Destination': '/data'}])
            return self.running
        return ''

    def snapshot_patches(self):
        self.enterContext(patch.object(remote, 'container', return_value='bot'))
        self.enterContext(patch.object(remote, 'run', side_effect=self.docker))

    def test_archiving_failure_still_restarts_and_removes_partial(self):
        self.snapshot_patches()
        def fail(_cid, path):
            path.write_bytes(b'incomplete')
            raise OSError('disk full')
        with patch.object(remote, 'backup', side_effect=fail), self.assertRaises(OSError):
            backup.snapshot(self.base / 'test.tar.gz')
        self.assertIn(['docker', 'start', 'bot'], self.calls)
        self.assertFalse((self.base / 'test.tar.gz').exists())
        self.assertFalse((self.base / 'test.tar.gz.partial').exists())

    def test_wrong_volume_or_stopped_bot_is_not_touched(self):
        self.snapshot_patches()
        for volume, running in [('other-volume', 'true'), (remote.VOLUME, 'false')]:
            self.volume, self.running = volume, running
            with self.subTest(volume=volume), self.assertRaises(remote.DeployError):
                backup.snapshot(self.base / 'test.tar.gz')
        self.assertFalse(any(call[1] in ('start', 'stop') for call in self.calls))

    def test_snapshot_restarts_before_publishing_completed_archive(self):
        self.snapshot_patches()
        target = self.base / 'test.tar.gz'
        def archive(_cid, path):
            self.assertEqual(self.calls[-1], ['docker', 'stop', '--time', '45', 'bot'])
            path.write_bytes(b'archive')
            self.assertFalse(target.exists())
        with patch.object(remote, 'backup', side_effect=archive):
            backup.snapshot(target)
        self.assertEqual(target.read_bytes(), b'archive')
        self.assertIn(['docker', 'start', 'bot'], self.calls)

    def test_archive_checks_real_sqlite_including_uncheckpointed_wal(self):
        dbpath = self.base / 'movie-helper.db'
        db = sqlite3.connect(dbpath)
        try:
            db.execute('PRAGMA journal_mode=WAL')
            db.execute('PRAGMA wal_autocheckpoint=0')
            db.execute('CREATE TABLE items (id INTEGER PRIMARY KEY)')
            db.execute('INSERT INTO items VALUES (1)')
            db.commit()
            archive = self.base / 'test.tar.gz'
            with tarfile.open(archive, 'w:gz') as tar:
                tar.add(dbpath, arcname='./movie-helper.db')
                tar.add(str(dbpath) + '-wal', arcname='./movie-helper.db-wal')
            backup.verify_archive(archive)
        finally:
            db.close()
        with archive.open('r+b') as out:
            out.seek(-8, 2)
            out.write(b'\x00' * 8)
        with self.assertRaises((OSError, EOFError)):
            backup.verify_archive(archive)

    def test_invalid_database_is_rejected(self):
        dbpath = self.base / 'movie-helper.db'
        dbpath.write_bytes(b'not a sqlite database')
        archive = self.base / 'test.tar.gz'
        with tarfile.open(archive, 'w:gz') as tar:
            tar.add(dbpath, arcname='movie-helper.db')
        with self.assertRaises(sqlite3.DatabaseError):
            backup.verify_archive(archive)

    def test_retention_ignores_unknown_files_and_keeps_latest_thirty(self):
        names = [f'daily-20260923T0100{i:02d}Z-1234abcd.tar.gz' for i in range(35)]
        listing = [{'Name': name} for name in names]
        listing += [{'Name': '../important.tar.gz'}, {'Name': 'manual.tar.gz'},
                    {'Name': 'daily-20260922T010000Z-1234abcd.tar.gz', 'IsDir': True}]
        self.assertEqual(backup.expired_names(listing), names[:5])
        self.assertEqual(backup.expired_names(listing[:3]), [])

    def test_preflight_failure_does_not_stop_bot(self):
        with patch.object(backup, 'check_config'), patch.object(backup, 'snapshot') as snapshot, \
                patch.object(backup, 'rclone', side_effect=remote.DeployError('no access')):
            with self.assertRaises(remote.DeployError):
                backup.perform()
        snapshot.assert_not_called()

    def test_upload_or_download_failure_never_prunes(self):
        for stage in ('upload', 'download', 'mismatch'):
            calls = []
            def rclone(*args):
                calls.append(args)
                if args[0] == 'copyto':
                    if args[1].startswith('backup:'):
                        if stage == 'download':
                            raise remote.DeployError('download failed')
                        Path(args[2]).write_bytes(b'wrong content')
                    elif stage == 'upload':
                        raise remote.DeployError('upload failed')
                return '[]'
            with self.subTest(stage=stage), patch.object(backup, 'check_config'), \
                    patch.object(backup, 'snapshot', side_effect=lambda p: p.write_bytes(b'archive')), \
                    patch.object(backup, 'verify_archive'), patch.object(backup, 'rclone', side_effect=rclone):
                with self.assertRaises(remote.DeployError):
                    backup.perform()
            self.assertFalse(any(args[0] == 'deletefile' for args in calls))
            self.assertFalse((self.base / 'backup-last-success.json').exists())

    def test_config_permissions_and_destination_are_checked(self):
        config = self.base / 'rclone.conf'
        config.write_text('[b2]\ntype=b2\naccount=test\nkey=test\n'
                          '[backup]\ntype=crypt\npassword=test\nremote=b2:wrong-bucket\n')
        with patch.object(backup, 'CONFIG', config):
            config.chmod(0o644)
            with self.assertRaises(remote.DeployError):
                backup.check_config()
            config.chmod(0o600)
            with self.assertRaises(remote.DeployError):
                backup.check_config()


if __name__ == '__main__':
    unittest.main()
