#!/usr/bin/env python3
"""GitHub runner side: temporary SSH credentials and stdin-only secret transfer."""
import json
import os
import re
import subprocess
from announcements import catalog
from ssh_transport import session


def main():
    release = os.environ.get('DEPLOY_RELEASE', '')
    if not re.fullmatch(r'[0-9]+-[0-9]+-[a-f0-9]{40}', release):
        raise RuntimeError('Invalid deployment release ID')
    for key in ('BOT_TOKEN', 'BOT_IMAGE'):
        if not os.environ.get(key):
            raise RuntimeError('Missing deployment setting: ' + key)
    payload = json.dumps({
        'announcements': catalog('announcements'),
        'image': os.environ['BOT_IMAGE'], 'bot_token': os.environ['BOT_TOKEN'],
        'gemini_api_key': os.environ.get('GEMINI_API_KEY', ''),
        'groq_api_key': os.environ.get('GROQ_API_KEY', ''),
        'tmdb_api_token': os.environ.get('TMDB_API_TOKEN', ''),
        'film_research_chat_ids': os.environ.get('FILM_RESEARCH_CHAT_IDS', ''),
        'film_research_gemini_model': os.environ.get('FILM_RESEARCH_GEMINI_MODEL', 'gemini-3.5-flash-lite'),
        'film_research_mode': os.environ.get('FILM_RESEARCH_MODE', 'off'),
        'film_research_groq_model': os.environ.get('FILM_RESEARCH_GROQ_MODEL', 'openai/gpt-oss-120b'),
        'ai_result_mode': os.environ.get('AI_RESULT_MODE', 'warmup'),
        'allowed_chat_ids': os.environ.get('ALLOWED_CHAT_IDS', ''),
        'registry_user': os.environ['GITHUB_ACTOR'],
        'registry_token': os.environ['REGISTRY_TOKEN'],
    }, ensure_ascii=False)
    if len(payload) > 65536:
        raise RuntimeError('Deployment input too large')
    with session() as (ssh, scp, target):
        destination = '/opt/movie-helper/releases/' + release
        subprocess.run([*ssh, 'umask 077; mkdir -m 700 ' + destination], check=True)
        subprocess.run([*scp, 'deploy/remote.py', 'deploy/announcements.py', 'deploy/compose.yaml',
                        target + ':' + destination + '/'], check=True)
        subprocess.run([*ssh, 'python3 -u ' + destination + '/remote.py'], input=payload, text=True, check=True)


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError:
        raise SystemExit('Deployment transport or remote execution failed') from None
    except RuntimeError as err:
        raise SystemExit(str(err)) from None
