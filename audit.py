import os,re
cjk=re.compile(r'[\u4e00-\u9fff\u3000-\u303f\uff00-\uffef]')
for r,d,fs in os.walk('web/src'):
    if 'mock' in r or 'locales' in r: continue
    for f in fs:
        if f.endswith(('.ts','.tsx')):
            p=os.path.join(r,f)
            n=sum(1 for l in open(p,encoding='utf-8')
                  if cjk.search(l) and not l.strip().startswith(('//','*','/*','{/*')))
            if n: print(n,p)
