import importlib.util
from pathlib import Path
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location("packager", Path(__file__).with_name("package.py"))
packager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packager)

class PackageTests(unittest.TestCase):
    def fixture(self, directory):
        path = Path(directory) / "fixture.s2plugin"
        files = {"runtime/windows_amd64/plugin.exe": b"MZtest-fixture"}
        manifest = {"schema_version": 2, "id": "examples.test.fixture", "version": "1.0.0",
                    "requires": {"general_api": 1}, "runtimes": {"windows-amd64": {"path": next(iter(files))}},
                    "files": {name: packager.sha(data) for name, data in files.items()}}
        return path, files, manifest

    def test_roundtrip_and_sha(self):
        with tempfile.TemporaryDirectory() as tmp:
            path, files, manifest = self.fixture(tmp)
            packager.write_archive(path, manifest, files)
            self.assertEqual(packager.verify(path)["result"], "PASS")
            with zipfile.ZipFile(path) as z:
                self.assertEqual(z.getinfo(next(iter(files))).external_attr >> 16, 0o100755)

    def test_tampered_sha_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path, files, manifest = self.fixture(tmp)
            files[next(iter(files))] += b"modified"
            packager.write_archive(path, manifest, files)
            with self.assertRaisesRegex(ValueError, "SHA mismatch"):
                packager.verify(path)

    def test_undeclared_and_traversal_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path, files, manifest = self.fixture(tmp)
            for name in ("rogue.txt", "../rogue.txt"):
                packager.write_archive(path, manifest, {**files, name: b"x"})
                with self.assertRaises(ValueError):
                    packager.verify(path)

    def test_repo_binary_output_rejected(self):
        with self.assertRaisesRegex(ValueError, "outside"):
            packager.build(packager.ROOT / "dist", [])

if __name__ == "__main__":
    unittest.main()
