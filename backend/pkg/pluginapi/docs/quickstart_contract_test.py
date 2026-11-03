#!/usr/bin/env python3
"""Offline contracts for the Chinese plugin quickstart. --root accepts a snapshot."""
import argparse, importlib.util, json, re, sys, tempfile, unittest, zipfile
from pathlib import Path
parser=argparse.ArgumentParser(add_help=False)
parser.add_argument('--root',type=Path,default=Path(__file__).resolve().parents[4])
args,rest=parser.parse_known_args()
ROOT=args.root.resolve()
DOC=ROOT/'docs/PLUGIN_DEVELOPMENT.md'
def block(name,language):
    text=DOC.read_text(encoding='utf-8')
    match=re.search(r'<!-- '+name+r':start -->\s*```'+language+r'\n(.*?)\n```\s*<!-- '+name+r':end -->',text,re.S)
    if not match: raise AssertionError('missing complete quickstart block: '+name)
    return match.group(1)+'\n'
class QuickstartContracts(unittest.TestCase):
    def test_general_first_and_navigation(self):
        text=DOC.read_text(encoding='utf-8')
        self.assertIn('schema2 / general.v1',text[:800])
        for heading in ['选择插件类型','最小输入检查插件','打包','签名','安装与发布','独立任务','排错','附录']:
            self.assertIn(heading,text)
    def test_manifest_matches_go_identity(self):
        manifest=json.loads(block('quickstart-manifest','json'))
        go=block('quickstart-go','go')
        self.assertEqual(manifest['schema_version'],2)
        self.assertEqual(manifest['requires']['general_api'],1)
        self.assertEqual(manifest['requires']['transport_api'],0)
        self.assertEqual(manifest['requires']['ui_bridge'],0)
        for value in [manifest['id'],manifest['version'],manifest['general']['hooks'][0]['name'],manifest['general']['hooks'][0]['protocols'][0]]:
            self.assertIn('"'+value+'"',go)
        self.assertEqual(manifest['runtimes'],{})
        self.assertEqual(manifest['files'],{})
    def test_config_and_schema_match(self):
        config=json.loads(block('quickstart-config','json'))
        schema=json.loads(block('quickstart-schema','json'))
        self.assertEqual(config,{'config_version':1,'keyword':'PLUGIN_TEST_BLOCK'})
        self.assertFalse(schema['additionalProperties'])
        self.assertEqual(schema['properties']['config_version']['const'],config['config_version'])
        self.assertIn('DisallowUnknownFields',block('quickstart-go','go'))
    def test_complete_code_and_test(self):
        go=block('quickstart-go','go'); test=block('quickstart-go-test','go')
        for method in ['GetInfo','Configure','Check','Serve','UnimplementedGeneralPluginServer']:
            self.assertIn(method,go)
        for case in ['pass','block','immutable','binding','cancel','invalid_config']:
            self.assertIn(case,test)
    def test_signer_is_real_and_private_excluded(self):
        signer=ROOT/'backend/scripts/plugin-sign/main.go'
        self.assertTrue(signer.is_file())
        source=signer.read_text(encoding='utf-8')
        for token in ['ed25519.Sign','ed25519.Verify','zw.Copy','case "keygen"','case "sign"','case "verify"']:
            self.assertIn(token,source)
        text=DOC.read_text(encoding='utf-8')
        self.assertIn('scripts/plugin-sign',text)
        self.assertIn('PLUGINS_ALLOW_UNSIGNED=false',text)
        self.assertIn('trusted_publishers',text)
    def test_packager_roundtrip_and_paths(self):
        file=ROOT/'backend/scripts/plugin-package.py'
        self.assertTrue(file.is_file())
        spec=importlib.util.spec_from_file_location('doc_packager',file)
        mod=importlib.util.module_from_spec(spec);spec.loader.exec_module(mod)
        with tempfile.TemporaryDirectory() as d:
            d=Path(d);src=d/'source';src.mkdir()
            (src/'manifest.source.json').write_text(block('quickstart-manifest','json'),encoding='utf-8')
            (src/'config.schema.json').write_text(block('quickstart-schema','json'),encoding='utf-8')
            (src/'config.example.json').write_text(block('quickstart-config','json'),encoding='utf-8')
            (src/'README.md').write_text('Fixture only',encoding='utf-8')
            binary=d/'plugin.exe';binary.write_bytes(b'MZ-fixture-not-a-real-runtime')
            out=d/'hello.s2plugin'
            record=mod.build(src,{'windows-amd64':binary},out)
            self.assertEqual(record['result'],'PASS')
            self.assertEqual(mod.verify(out)['result'],'PASS')
            with zipfile.ZipFile(out) as z:
                manifest=json.loads(z.read('manifest.json'))
                self.assertEqual(manifest['runtimes']['windows-amd64']['path'],'runtime/windows_amd64/plugin.exe')
                self.assertEqual(set(z.namelist()),{'manifest.json',*manifest['files']})
            with self.assertRaises((ValueError,FileExistsError)):mod.build(src,{'windows-amd64':binary},out)
            with self.assertRaises(ValueError):mod.build(src,{'../evil':binary},d/'bad.s2plugin')
    def test_install_flow_and_cas(self):
        text=DOC.read_text(encoding='utf-8')
        for token in ['expected_generation','/plugin-instances','/plugins/upload','.data.id','.data.instance.id','保存新版本','发布 / 回退','headless']:
            self.assertIn(token,text)
    def test_local_links_resolve(self):
        text=DOC.read_text(encoding='utf-8')
        for link in re.findall(r'\]\(([^)]+)\)',text):
            if '://' in link or link.startswith('#'): continue
            self.assertTrue((DOC.parent/link.split('#')[0]).exists(),link)
if __name__=='__main__':
    unittest.main(argv=[sys.argv[0],*rest])
