"""Freeze bounded public meme candidates for human/vision evaluation; no secrets or Telegram."""
import hashlib,json,urllib.request,urllib.parse,shutil
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
from PIL import Image,ImageOps,ImageDraw
ROOT=Path(__file__).resolve().parent/'results/pilot-2026-10-10'
ROOT.mkdir(parents=True,exist_ok=True)
def fetch_image(row):
 target=ROOT/'images'/f"{row['id']}.png"
 try:
  url=urllib.parse.urlparse(row['url'])
  if url.scheme!='https' or url.hostname!='i.redd.it':raise ValueError('non-public-image-host')
  with urllib.request.urlopen(urllib.request.Request(row['url'],headers={'User-Agent':'MovieHelperMemeExperiment/1.0'}),timeout=18) as r:data=r.read((8<<20)+1)
  if len(data)>8<<20:raise ValueError('oversize')
  target.write_bytes(data)
  im=Image.open(target);im.verify()
  row.update(image='images/'+target.name,sha256=hashlib.sha256(data).hexdigest(),status='ok')
 except Exception as e:row.update(status=type(e).__name__)
 return row

def main():
 (ROOT/'images').mkdir(exist_ok=True)
 production=json.loads((ROOT/'production.json').read_text())
 fresh=json.loads((ROOT/'fresh-russian.json').read_text()).get('memes',[])
 rows=[{'id':'M00','origin':'user-example','title':'','image':'images/M00.png','status':'ok'}]
 shutil.copy('/tmp/codex-clipboard-224506e0-42c1-49e0-9798-60fe4bfdab7b.png',ROOT/'images/M00.png')
 rows[0]['sha256']=hashlib.sha256((ROOT/'images/M00.png').read_bytes()).hexdigest()
 used=set()
 proposed=[]
 for r in production[:8]:
  if r['url'] not in used:proposed.append({**r,'origin':'production-'+r['state']});used.add(r['url'])
 for r in fresh:
  if r.get('nsfw') or r.get('spoiler') or r.get('url') in used:continue
  proposed.append({'origin':'fresh','url':r['url'],'post':r['postLink'],'title':r['title']});used.add(r['url'])
 for n,r in enumerate(proposed[:17],1):r['id']=f'M{n:02}'
 with ThreadPoolExecutor(max_workers=4) as pool:rows+=list(pool.map(fetch_image,proposed[:17]))
 dataset={'version':'meme-pilot-v1','created_date':'2026-10-10','rows':rows,'selection':'user control + latest 8 production + first 9 public fresh non-nsfw non-spoiler; no manual humour selection'}
 (ROOT/'dataset.json').write_text(json.dumps(dataset,ensure_ascii=False,indent=2))
 print([(r['id'],r['origin'],r['status']) for r in rows])
 for start in range(0,len(rows),6):
  subset=rows[start:start+6];sheet=Image.new('RGB',(1440,1440),'#e5e5e5');d=ImageDraw.Draw(sheet)
  for n,r in enumerate(subset):
   x=(n%3)*480;y=(n//3)*720;d.text((x+12,y+5),r['id']+' '+r['origin'],fill='black')
   if r['status']=='ok':
    im=Image.open(ROOT/r['image']).convert('RGB');im.thumbnail((456,660));sheet.paste(im,(x+(480-im.width)//2,y+40))
  sheet.save(ROOT/f'sheet-{start//6+1}.jpg',quality=90)
if __name__=='__main__':main()
