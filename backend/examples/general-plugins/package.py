#!/usr/bin/env python3
"""Build real general.v1 runtimes, write .s2plugin ZIPs and verify every SHA.
Uses only Python's standard library plus Go; never writes binaries in the repo.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent
BACKEND = ROOT.parents[1]
NAMES = ("credential-input", "business-output", "pricing-sync")
TARGETS = {"windows-amd64": ("windows", "amd64", "runtime/windows_amd64/plugin.exe"),
           "linux-amd64": ("linux", "amd64", "runtime/linux_amd64/plugin")}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def verify(path):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)) or len(names) > 512:
            raise ValueError("duplicate or excessive archive files")
        for name in names:
            if name.startswith("/") or "\\" in name or any(part in ("", ".", "..") for part in name.split("/")):
                raise ValueError("invalid archive path")
        manifest = json.loads(archive.read("manifest.json"))
        if manifest["schema_version"] != 2 or manifest["requires"]["general_api"] != 1:
            raise ValueError("not schema2/general.v1")
        files = manifest["files"]
        if set(names) != {"manifest.json", *files}:
            raise ValueError("undeclared or missing files")
        for name, digest in files.items():
            if sha(archive.read(name)) != digest:
                raise ValueError("SHA mismatch: " + name)
        for target, runtime in manifest["runtimes"].items():
            if target not in TARGETS or runtime["path"] != TARGETS[target][2] or runtime["path"] not in files:
                raise ValueError("invalid runtime declaration")
            data = archive.read(runtime["path"])
            if (target.startswith("windows") and not data.startswith(b"MZ")) or (target.startswith("linux") and not data.startswith(b"\x7fELF")):
                raise ValueError("not a real executable")
        return {"path": str(Path(path).resolve()), "sha256": sha(Path(path).read_bytes()),
                "id": manifest["id"], "version": manifest["version"],
                "runtimes": sorted(manifest["runtimes"]), "files_verified": len(files), "result": "PASS"}


def write_archive(path, manifest, payloads):
    # Canonical timestamp/modes make archives deterministic for identical binaries.
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        all_files = {"manifest.json": (json.dumps(manifest, indent=2, ensure_ascii=False) + "\n").encode(), **payloads}
        for name, data in sorted(all_files.items()):
            info = zipfile.ZipInfo(name, (2020, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = (0o100755 if name.startswith("runtime/") else 0o100644) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, data)


def build(out, targets):
    out = Path(out).resolve()
    if out.is_relative_to(BACKEND.parent):
        raise ValueError("package output must be outside the repository")
    out.mkdir(parents=True, exist_ok=True)
    records = []
    with tempfile.TemporaryDirectory(prefix="m7-build-", dir=out) as temp:
        for name in NAMES:
            manifest = json.loads((ROOT / name / "manifest.source.json").read_text(encoding="utf-8"))
            payloads = {"sdk/config.schema.json": (ROOT / name / "config.schema.json").read_bytes(),
                        "sdk/config.example.json": (ROOT / name / "config.example.json").read_bytes(),
                        "README.md": (ROOT / name / "README.md").read_bytes()}
            manifest["runtimes"] = {}
            for target in targets:
                goos, goarch, runtime_path = TARGETS[target]
                binary = Path(temp) / (name + "-" + target + (".exe" if goos == "windows" else ""))
                env = os.environ.copy()
                env.update(GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
                command = ["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.plugin=" + name,
                           "-o", str(binary), "./examples/general-plugins/cmd/general-plugin"]
                result = subprocess.run(command, cwd=BACKEND, env=env, capture_output=True, text=True)
                if result.returncode:
                    raise RuntimeError("build failed for " + name + "/" + target + "\n" + result.stdout + result.stderr)
                print(json.dumps({"phase": "BUILD", "plugin": name, "target": target, "command": command,
                                  "input": {"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"},
                                  "output": result.stdout + result.stderr, "exit_status": result.returncode}), flush=True)
                payloads[runtime_path] = binary.read_bytes()
                manifest["runtimes"][target] = {"path": runtime_path}
            manifest["files"] = {name: sha(data) for name, data in sorted(payloads.items())}
            path = out / (name + "-" + manifest["version"] + ".s2plugin")
            write_archive(path, manifest, payloads)
            record = verify(path)
            records.append(record)
            print(json.dumps(record), flush=True)
    (out / "PACKAGES.json").write_text(json.dumps(records, indent=2) + "\n", encoding="utf-8")
    return records


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    parser.add_argument("--verify", type=Path, nargs="+")
    args = parser.parse_args()
    if args.verify:
        for path in args.verify:
            print(json.dumps(verify(path)))
    elif args.out:
        if len(set(args.targets)) != len(args.targets):
            parser.error("duplicate targets")
        build(args.out, args.targets)
    else:
        parser.error("--out or --verify is required")


if __name__ == "__main__":
    main()
