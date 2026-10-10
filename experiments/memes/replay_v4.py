"""Replay frozen API responses through the production Go validator and policy."""
import argparse,hashlib,json,subprocess
from pathlib import Path
def replay(root,binary):
 raw=root/'responses.jsonl';output=[]
 for line in raw.read_text().splitlines():
  row=json.loads(line)
  if 'result' in row:
   result=subprocess.run([str(binary),'evaluate'],input=json.dumps({'result':row['result'],'count':len(row['ids'])}),text=True,capture_output=True,check=False)
   row['original_status']=row['status']
   if result.returncode:
    row.update(status='contract_error',contract_error='Go structural validation failed',decisions={})
   else:
    row.update(status='ok',decisions=json.loads(result.stdout))
    row['invalid_explanations']=[row['ids'][int(n)] for n,r in row['result']['reviews'].items() if not r['assessment']['observation'].strip() or len(r['assessment']['observation'])>400 or len(r['assessment']['punchline'])>350 or (r['reason']=='eligible' and not r['assessment']['punchline'].strip())]
  output.append(row)
 (root/'replayed.jsonl').write_text(''.join(json.dumps(r,ensure_ascii=False)+'\n' for r in output))
 repo=Path(__file__).resolve().parents[2]
 manifest={'source_responses_sha':hashlib.sha256(raw.read_bytes()).hexdigest(),'binary_sha':hashlib.sha256(binary.read_bytes()).hexdigest(),'go_sources':{name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in ['internal/ai/meme.go','internal/ai/meme_policy.go','internal/ai/selection.go']}}
 (root/'replay-manifest.json').write_text(json.dumps(manifest,indent=2))
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--root',type=Path,required=True);p.add_argument('--binary',type=Path,required=True);a=p.parse_args();replay(a.root,a.binary)
