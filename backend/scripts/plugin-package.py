#!/usr/bin/env python3
"""Build or integrity-check schema2/general.v1 headless packages; standard library only.
Does not compile binaries or verify publisher signatures. Never overwrites an archive.
"""
import argparse,hashlib,json
from pathlib import Path,PurePosixPath
import zipfile
REPO=Path(__file__).resolve().parents[2]
TARGETS={'windows-amd64':'runtime/windows_amd64/plugin.exe','linux-amd64':'runtime/linux_amd64/plugin','linux-arm64':'runtime/linux_arm64/plugin'}
MAX_FILE=128*1024*1024
MAX_TOTAL=256*1024*1024
def digest(data):return hashlib.sha256(data).hexdigest()
def safe_name(name):
    parts=name.split('/')
    return bool(name) and not name.startswith('/') and '\\' not in name and ':' not in name and all(p not in ('','..','.') for p in parts)
def binary_check(target,data):
    magic=b'MZ' if target.startswith('windows-') else b'\x7fELF'
    if not data.startswith(magic):raise ValueError('runtime format mismatch: '+target)
def verify(path):
    with zipfile.ZipFile(path) as z:
        names=z.namelist()
        if len(names)>512 or len(names)!=len(set(names)) or any(not safe_name(n) for n in names):raise ValueError('invalid archive entries')
        if any(i.file_size>MAX_FILE or (i.external_attr>>16)&0o170000==0o120000 for i in z.infolist()) or sum(i.file_size for i in z.infolist())>MAX_TOTAL:raise ValueError('size limit or symlink')
        if z.testzip() is not None:raise ValueError('ZIP CRC error')
        m=json.loads(z.read('manifest.json'))
        if m.get('schema_version')!=2 or m.get('requires',{}).get('general_api')!=1:raise ValueError('requires schema2/general.v1')
        files=m.get('files',{})
        if not files or any(n in files for n in ['manifest.json','signature.json']):raise ValueError('invalid manifest.files')
        if set(names)-{'manifest.json','signature.json'}!=set(files):raise ValueError('undeclared/missing file')
        for name,want in files.items():
            if digest(z.read(name))!=want:raise ValueError('SHA256 mismatch: '+name)
        if not m.get('runtimes'):raise ValueError('runtime required')
        for target,runtime in m['runtimes'].items():
            if target not in TARGETS or runtime.get('path')!=TARGETS[target] or runtime['path'] not in files:raise ValueError('invalid runtime declaration')
            binary_check(target,z.read(runtime['path']))
        return {'path':str(Path(path).resolve()),'id':m['id'],'version':m['version'],'sha256':digest(Path(path).read_bytes()),'files_verified':len(files),'runtimes':sorted(m['runtimes']),'result':'PASS'}
def build(source,runtimes,out):
    source=Path(source).resolve();out=Path(out).resolve()
    if out.is_relative_to(REPO):raise ValueError('package output must be outside repository')
    if not runtimes or any(t not in TARGETS for t in runtimes):raise ValueError('unsupported/missing runtime target')
    if out.exists():raise FileExistsError(out)
    m=json.loads((source/'manifest.source.json').read_text(encoding='utf-8-sig'))
    req=m.get('requires',{})
    if m.get('schema_version')!=2 or req.get('general_api')!=1 or req.get('transport_api')!=0 or req.get('ui_bridge')!=0 or m.get('ui')!={}:raise ValueError('builder supports schema2/general.v1 headless only')
    payloads={}
    for name,entry in [('README.md','README.md'),('config.schema.json','sdk/config.schema.json'),('config.example.json','sdk/config.example.json')]:
        p=source/name
        if p.is_symlink():raise ValueError('source symlink rejected')
        payloads[entry]=p.read_bytes()
    m['runtimes']={}
    for target,binary in runtimes.items():
        binary=Path(binary)
        if binary.is_symlink():raise ValueError('runtime symlink rejected')
        data=binary.read_bytes();binary_check(target,data)
        payloads[TARGETS[target]]=data;m['runtimes'][target]={'path':TARGETS[target]}
    if any(len(b)>MAX_FILE for b in payloads.values()) or sum(map(len,payloads.values()))>MAX_TOTAL:raise ValueError('size limit')
    m['files']={name:digest(data) for name,data in sorted(payloads.items())}
    data={'manifest.json':(json.dumps(m,ensure_ascii=False,indent=2)+'\n').encode('utf-8'),**payloads}
    out.parent.mkdir(parents=True,exist_ok=True)
    with out.open('xb') as f:
        with zipfile.ZipFile(f,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
            for name,raw in sorted(data.items()):
                info=zipfile.ZipInfo(name,(2020,1,1,0,0,0));info.create_system=3
                info.external_attr=(0o100755 if name.startswith('runtime/') else 0o100644)<<16;info.compress_type=zipfile.ZIP_DEFLATED
                z.writestr(info,raw)
    return verify(out)
def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--source',type=Path);p.add_argument('--runtime',action='append',default=[],metavar='TARGET=FILE');p.add_argument('--out',type=Path);p.add_argument('--verify',type=Path)
    a=p.parse_args()
    if a.verify:
        if a.source or a.runtime or a.out:p.error('--verify cannot be combined with build options')
        record=verify(a.verify)
    else:
        if not a.source or not a.runtime or not a.out:p.error('--source --runtime --out required')
        runtimes={}
        for item in a.runtime:
            target,separator,file=item.partition('=')
            if not separator or not file or target in runtimes:p.error('invalid or duplicate --runtime')
            runtimes[target]=Path(file)
        record=build(a.source,runtimes,a.out)
    print(json.dumps(record,ensure_ascii=False))
if __name__=='__main__':main()
