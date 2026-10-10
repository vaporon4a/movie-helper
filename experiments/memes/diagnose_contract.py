"""One explicit diagnostic model call; captures only the parsed decision on validation failure."""
import json,os
from pathlib import Path
from run_transport_v2 import request
from prompts import VARIANTS
root=Path('/tmp/movie-helper-meme-pilot-2026-10-10/pilot-groq-transport-v2')
out=root/'contract-diagnostic.json'
if out.exists():raise SystemExit('diagnostic already reserved; no retry')
out.write_text(json.dumps({'status':'reserved','max_requests':1}))
e=dict(line.partition('=')[::2] for line in Path('/opt/movie-helper/current/runtime.env').read_text().splitlines() if '=' in line)
data=json.loads((root/'dataset.json').read_text());rows={r['id']:r for r in data['rows']}
try:
 result=request('groq','qwen/qwen3.8-27b',e['GROQ_API_KEY'],VARIANTS['editorial'],[rows['M00'],rows['M01']],root)
 out.write_text(json.dumps({'status':'ok',**result},ensure_ascii=False,indent=2));print('diagnostic ok')
except ValueError as error:
 parsed=None;tb=error.__traceback__
 while tb:
  if tb.tb_frame.f_code.co_name=='validate':parsed=tb.tb_frame.f_locals.get('result')
  tb=tb.tb_next
 out.write_text(json.dumps({'status':'contract_error','code':str(error),'parsed_decision':parsed},ensure_ascii=False,indent=2))
 print('diagnostic contract error',str(error))
