"""Build an offline review harness from frozen manual labels and actual API results."""
import json,hashlib,html,shutil
from pathlib import Path
ROOT=Path(__file__).resolve().parent/'results/pilot-2026-10-10'
ALT=ROOT.parent/'pilot-groq-transport-v2'
LITE=ROOT.parent/'pilot-flash-lite-v1'
HOLD=ROOT.parent/'holdout-editorial-v1'
HOLD2=ROOT.parent/'holdout-editorial-v2'
CAL=ROOT.parent/'calibration-v4-2026-10-10'
TUNED=ROOT.parent/'calibration-v4-tuned-2026-10-10'
FINAL=ROOT.parent/'calibration-v4-final-2026-10-10'
def records(path):return [json.loads(x) for x in path.read_text().splitlines()] if path.exists() else []
def summarize(rows,labels):
 groups={}
 for row in rows:
  key=(row['model'],row['variant'])
  group=groups.setdefault(key,{'model':key[0],'variant':key[1],'calls':0,'failures':0,'tp':0,'fp':0,'fn':0,'tn':0,'evaluated':0})
  group['calls']+=1
  if row['status']!='ok':group['failures']+=1;continue
  for n,ident in enumerate(row['ids']):
   actual=row['result']['reviews'][str(n)]['reason']=='accepted';wanted=labels[ident]['publish']
   group['evaluated']+=1
   group['tp' if actual and wanted else 'fp' if actual else 'fn' if wanted else 'tn']+=1
 for group in groups.values():
  accepted=group['tp']+group['fp'];positive=group['tp']+group['fn']
  group.update(precision=group['tp']/accepted if accepted else None,recall=group['tp']/positive if positive else None)
 return list(groups.values())
def calibrations():
 ratings=json.loads((CAL/'user-ratings.json').read_text())['ratings'] if (CAL/'user-ratings.json').exists() else {}
 rows=[];summaries=[]
 for variant,path in [('structured',CAL),('tuned',TUNED),('final',FINAL)]:
  results=records(path/('replayed.jsonl' if variant=='final' else 'responses.jsonl'));groups={}
  for row in results:
   row.update(phase='calibration',variant=variant);rows.append(row)
   group=groups.setdefault(row['model'],dict(model=row['model'],variant=variant,calls=0,failures=0,evaluated=0,exact=0,false_publish=0,missed_publish=0))
   group['calls']+=1
   if row['status']!='ok':group['failures']+=1;continue
   for n,ident in enumerate(row['ids']):
    got=row['decisions'][str(n)];want=ratings[ident]
    group['evaluated']+=1;group['exact']+=got==want;group['false_publish']+=got=='publish' and want!='publish';group['missed_publish']+=got!='publish' and want=='publish'
  summaries.extend(groups.values())
 return rows,summaries,ratings

def main():
 dataset=json.loads((ROOT/'dataset.json').read_text());manual=json.loads((ROOT/'editorial-labels.json').read_text());labels={r['id']:r for r in manual['labels']}
 responses=records(ROOT/'responses.jsonl')+records(ALT/'responses.jsonl')+records(LITE/'responses.jsonl')
 holdout=records(HOLD/'responses.jsonl')+records(HOLD2/'responses.jsonl')
 for r in responses:r['phase']='pilot'
 for r in holdout:r['phase']='holdout'
 summary=summarize(responses,labels)
 calibration,calibration_summary,ratings=calibrations()
 data={'rows':dataset['rows'],'labels':labels,'responses':responses+holdout+calibration,'calibration_summary':calibration_summary,'user_ratings':ratings,'summary':summary,'holdout_summary':summarize(holdout,labels),'protocol':json.loads((ROOT/'protocol.json').read_text())}
 (ROOT/'calibration-summary.json').write_text(json.dumps(calibration_summary,ensure_ascii=False,indent=2))
 (ROOT/'summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2))
 (ROOT/'holdout-summary.json').write_text(json.dumps(data['holdout_summary'],ensure_ascii=False,indent=2))
 payload=json.dumps(data,ensure_ascii=False).replace('<','\\u003c')
 template=Path(__file__).with_name('review-template.html').read_text()
 (ROOT/'compare.html').write_text(template.replace('/*DATA*/',payload))
 print(json.dumps(summary,ensure_ascii=False))
if __name__=='__main__':main()
