#!/usr/bin/env python3
"""Prove lint guards reject bad code in a disposable module, without editing the repo."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import tempfile


BAD_CODE = '''package clubreminder
import (
 "context"
 "errors"
 "time"
 _ "github.com/vaporon4a/movie-helper/internal/storage"
)
type Oversized interface { A(); B(); C(); D(); E(); F(); G(); H() }
var ErrFixture = errors.New("fixture")
func Clock() time.Time { return time.Now() }
func Consume(ctx context.Context) { _ = ctx.Done() }
func Detached(ctx context.Context) { Consume(context.Background()) }
func Durations(a, b time.Duration) time.Duration { return a*b }
func Forced(v any) string { return v.(string) }
func Wrapped(err error) bool { return err == ErrFixture }
func Generate(n int) int { if n>0 { if n>1 { if n>2 { if n>3 { if n>4 { if n>5 { return n } } } } } }; return 0 }
'''
GOOD_CODE = '''package clubreminder
import "context"
type Port interface { Send(context.Context) error }
func Consume(ctx context.Context) { _ = ctx.Done() }
func Inherited(ctx context.Context) { Consume(ctx) }
'''
EXPECTED = {'depguard', 'forbidigo', 'interfacebloat', 'contextcheck',
            'durationcheck', 'forcetypeassert', 'errorlint', 'gocognit'}


def run(linter, root, directory, code):
    source = directory / 'internal/clubreminder/model.go'
    source.parent.mkdir(parents=True, exist_ok=True)
    source.write_text(code)
    adapter = directory / 'internal/storage/store.go'
    adapter.parent.mkdir(parents=True, exist_ok=True)
    adapter.write_text('package storage\n')
    (directory / 'go.mod').write_text((root / 'go.mod').read_text().split('require')[0])
    shutil.copyfile(root / '.golangci.yml', directory / '.golangci.yml')
    report = directory / 'report.json'
    result = subprocess.run([linter, 'run', '--output.json.path', str(report),
                             '--output.text.path', str(directory / 'report.txt'), './...'],
                            cwd=directory, capture_output=True, text=True)
    if result.returncode not in (0, 1) or not report.is_file():
        raise RuntimeError('Lint fixture failed to execute: ' + result.stderr[-2000:])
    data = json.loads(report.read_text())
    issues = data.get('Issues') or []
    if any(issue['FromLinter'] == 'typecheck' for issue in issues):
        raise RuntimeError('Fixture did not typecheck')
    return result.returncode, {issue['FromLinter'] for issue in issues}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--linter', default='golangci-lint')
    args = parser.parse_args()
    binary = shutil.which(args.linter)
    if not binary:
        raise SystemExit('golangci-lint not found: ' + args.linter)
    root = Path(__file__).resolve().parent.parent
    with tempfile.TemporaryDirectory(prefix='movie-helper-lint-guards-') as tmp:
        directory = Path(tmp)
        code, detected = run(binary, root, directory, BAD_CODE)
        if code != 1 or not EXPECTED <= detected:
            raise SystemExit('Missing lint guards: ' + ', '.join(sorted(EXPECTED - detected)))
        code, detected = run(binary, root, directory, GOOD_CODE)
        if code != 0 or detected:
            raise SystemExit('Valid fixture rejected: ' + ', '.join(sorted(detected)))
    print('Lint guards verified: ' + ', '.join(sorted(EXPECTED)))


if __name__ == '__main__':
    main()
