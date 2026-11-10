#!/usr/bin/env python3
"""Build a local immutable download inventory. No remote release discovery."""
import hashlib,json,pathlib,re,sys
out=pathlib.Path(sys.argv[1]);version=sys.argv[2]
if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}',version):raise SystemExit('invalid package version')
rows=[]
for os in ('windows','linux'):
 for arch in ('amd64','arm64'):
  formats=('exe','zip') if os=='windows' else ('run','tar.gz','deb')
  for fmt in formats:
   name=f'hexmon-edge_{version.removeprefix("v")}_{arch}.deb' if fmt=='deb' else f'hexmon-edge-{version}-{os}-{arch}{"-setup" if fmt=="exe" else ""}.{fmt}'
   file=out/name
   if not file.is_file():raise SystemExit('missing package: '+name)
   data=file.read_bytes()
   rows.append(dict(id=f'{os}-{arch}-{fmt.replace(".","-")}',filename=name,os=os,arch=arch,format=fmt,size=len(data),sha256=hashlib.sha256(data).hexdigest()))
(out/'manifest.json').write_text(json.dumps(dict(version=version,packages=rows),indent=2)+'\n')
