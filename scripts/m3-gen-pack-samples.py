#!/usr/bin/env python3
# PCL-DSH-Go M3-G | deterministic pack/plugin fixture generator.
# Generates malicious dspack samples, valid packs and local plugin fixtures.
# No network, no external files, no real credentials. Re-run: python3 scripts/m3-gen-pack-samples.py
import hashlib
import json
import os
import shutil
import struct
import warnings
import zipfile
import zlib

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MAL = os.path.join(REPO, "testdata", "pack", "malicious")
VAL = os.path.join(REPO, "testdata", "pack", "valid")
PLUGIN = os.path.join(REPO, "testdata", "plugin")
CASES = []
VALID = []


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def fixed_zipinfo(name):
    info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = 0o100644 << 16
    return info


def write_zip(path, entries):
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED, allowZip64=True) as zf:
        for name, data in entries:
            zf.writestr(fixed_zipinfo(name), data)


def build_zip_bytes(entries):
    out = bytearray()
    central = []
    for e in entries:
        name = e["name"]
        data = e.get("data", b"")
        method = e.get("method", 8)
        flags = e.get("flag", 0)
        if not name.isascii():
            flags |= 0x800
        if method == 8:
            compressor = zlib.compressobj(9, zlib.DEFLATED, -15)
            comp = compressor.compress(data) + compressor.flush()
        else:
            comp = data
        crc = e.get("crc", zlib.crc32(data) & 0xFFFFFFFF)
        usize = e.get("usize", len(data))
        csize = len(comp)
        ext = e.get("external_attr", 0o100644 << 16)
        encoded = name.encode("utf-8")
        offset = len(out)
        out += struct.pack("<IHHHHHIIIHH", 0x04034B50, 20, flags, method, 0, 0, crc, csize, usize, len(encoded), 0)
        out += encoded + comp
        central.append(struct.pack(
            "<IHHHHHHIIIHHHHHII", 0x02014B50, 0x031E, 20, flags, method, 0, 0,
            crc, csize, usize, len(encoded), 0, 0, 0, 0, ext, offset) + encoded)
    cd = b"".join(central)
    cd_offset = len(out)
    out += cd + struct.pack("<IHHHHIIH", 0x06054B50, 0, 0, len(central), len(central), len(cd), cd_offset, 0)
    return bytes(out)


def add_mal(path, cls, rule, note=""):
    CASES.append({"file": os.path.basename(path), "class": cls, "rule": rule,
                  "expectedStatus": 400, "expectedCode": "VALIDATION", "note": note})


def manifest_v4(profile="web", name="sample-v2", version="1.0.0", dsh="0.2.1-rc.3"):
    return {"manifestVersion": 4, "type": "profile", "name": name, "version": version,
            "profileName": profile, "dshVersion": dsh,
            "bundles": ["@deepseek-ai/dsh-base"], "dependencies": {}}


def manifest_v5(profile="web", name="sample-v3", version="1.0.0", dsh="0.2.1-rc.3"):
    m = manifest_v4(profile, name, version, dsh)
    m["manifestVersion"] = 5
    return m


def manifest_home_v5(profile="m3home", name="home-v3", version="1.0.0", dsh="0.2.1-rc.3"):
    return {"manifestVersion": 5, "type": "dshhome", "name": name, "version": version,
            "dshVersion": dsh, "defaultProfile": profile,
            "profiles": {profile: {"bundles": ["@deepseek-ai/dsh-base"], "dependencies": {}}}}


def marker(version):
    return json.dumps({"format": "dspack", "version": version}, separators=(",", ":")).encode()


def base_entries(manifest, container_version=3, profile_dir="web"):
    return [("dspack.json", marker(container_version)),
            ("manifest.json", json.dumps(manifest, separators=(",", ":"), ensure_ascii=False).encode()),
            ("overrides/%s/package.json" % profile_dir,
             json.dumps({"dsh": {"profile": {"bundles": ["@deepseek-ai/dsh-base"]}}, "dependencies": {}}, separators=(",", ":")).encode())]


def entry_dicts(pairs):
    return [{"name": n, "data": d} for n, d in pairs]


def mal(name, cls, rule, entries, note=""):
    path = os.path.join(MAL, name + ".dspack")
    write_zip(path, entries)
    add_mal(path, cls, rule, note)


def mal_raw(name, cls, rule, data, note=""):
    path = os.path.join(MAL, name + ".dspack")
    with open(path, "wb") as f:
        f.write(data)
    add_mal(path, cls, rule, note)


def generate_malicious():
    if os.path.isdir(MAL):
        shutil.rmtree(MAL)
    os.makedirs(MAL, exist_ok=True)
    m5 = manifest_v5()
    home = manifest_home_v5()
    ok = base_entries(m5)

    mal_raw("01_not_zip", "not_zip", "DSH_INTEGRATION 7.1 ZIP container",
            b"PCL-DSH-Go synthetic not-a-zip payload\n" * 4)
    good = build_zip_bytes([{"name": n, "data": d} for n, d in ok])
    mal_raw("02_truncated_zip", "truncated_zip", "7.2 corrupt container", good[: max(64, len(good) // 2)])
    mal("03_missing_dspack_marker", "missing_marker", "7.1 exactly one marker",
        [("manifest.json", json.dumps(m5).encode()), ("overrides/default/package.json", b"{}")])
    mal("04_missing_manifest", "missing_manifest", "7.1 dspack needs manifest.json", [("dspack.json", marker(3))])
    e = base_entries(m5)
    e.append(("dsh-snapshot.json", json.dumps({"format": 1, "platform": "linux"}).encode()))
    mal("05_conflicting_markers", "conflicting_markers", "7.1 exactly one marker", e)
    mal("06_dsh_pack_yaml", "dsh_pack_yaml", "7.1 Overture YAML unsupported",
        [("dsh-pack.yaml", b"name: test\nversion: 1.0.0\n")])
    mal("07_unsupported_container_version", "unsupported_container_version", "7.1 version 2|3 only",
        base_entries(manifest_v5(), 99))
    mal("08_unsupported_manifest_version", "unsupported_manifest_version", "7.1 manifestVersion 4|5 only",
        base_entries(dict(manifest_v5(), manifestVersion=6), 3))
    mal("10_dshhome_with_v2_container", "dshhome_v2", "7.1 dshhome requires v3 + mv5",
        base_entries(home, 2))
    mal("11_empty_zip", "empty_zip", "7.1 missing marker", [])
    mal("12_invalid_manifest_json", "invalid_manifest_json", "7.1 manifest must be JSON",
        [("dspack.json", marker(3)), ("manifest.json", b"{not-json")])
    big = json.dumps(dict(m5, filler="A" * (4 * 1024 * 1024 + 64)), separators=(",", ":")).encode()
    mal("13_manifest_metadata_too_large", "metadata_too_large", "7.2 metadata <= 4 MiB",
        [("dspack.json", marker(3)), ("manifest.json", big)])

    path_cases = [
        ("14_zip_slip_parent", "../evil.txt", "7.2 no .."),
        ("15_zip_slip_deep", "overrides/../../escape.txt", "7.2 no .."),
        ("16_absolute_unix", "/etc/passwd", "7.2 relative only"),
        ("17_absolute_windows", "C:/evil.txt", "7.2 relative only / drive"),
        ("18_unc_path", "//server/share/evil.txt", "7.2 UNC rejected"),
        ("19_backslash_path", "overrides\\bad.txt", "7.2 backslash rejected"),
        ("20_windows_reserved", "overrides/NUL.txt", "7.2 Windows reserved names"),
        ("21_trailing_dot", "overrides/bad.", "7.2 trailing dot"),
        ("22_trailing_space", "overrides/bad ", "7.2 trailing space"),
        ("23_colon_ads", "overrides/a:b", "7.2 colon/ADS"),
        ("24_control_char_name", "overrides/bad\x01.txt", "7.2 control chars"),
        ("25_empty_segment", "overrides//empty.txt", "7.2 no empty segment"),
        ("26_dot_segment", "overrides/./dot.txt", "7.2 no . segment"),
        ("27_path_too_long", "overrides/" + "a" * 1000 + ".txt", "7.2 path <= 1000"),
        ("28_forbidden_char", "overrides/a?b.txt", "7.2 forbidden characters"),
        ("29_case_collision", None, "7.2 case-insensitive collision"),
        ("30_nfc_collision", None, "7.2 Unicode NFC collision"),
        ("31_file_dir_collision", None, "7.2 file/dir collision"),
        ("32_duplicate_path", None, "7.2 duplicate path"),
    ]
    for name, bad, rule in path_cases:
        entries = base_entries(m5)
        if name.startswith("29_"):
            entries += [("overrides/Case/x.txt", b"x"), ("overrides/case/y.txt", b"y")]
        elif name.startswith("30_"):
            entries += [("overrides/caf\u00e9.txt", b"a"), ("overrides/cafe\u0301.txt", b"b")]
        elif name.startswith("31_"):
            entries += [("overrides/data", b"file"), ("overrides/data/file.txt", b"child")]
        elif name.startswith("32_"):
            entries += [("overrides/dup.txt", b"one"), ("overrides/dup.txt", b"two")]
        else:
            entries.append((bad, b"synthetic"))
        mal(name, name.split("_", 1)[1], rule, entries)

    entries = entry_dicts(base_entries(m5)) + [{"name": "overrides/link", "data": b"../../escape", "external_attr": 0o120777 << 16}]
    mal_raw("33_symlink_entry", "symlink_entry", "7.2 symlinks rejected", build_zip_bytes(entries))
    entries = entry_dicts(base_entries(m5)) + [{"name": "overrides/fifo", "data": b"", "external_attr": 0o010644 << 16}]
    mal_raw("34_special_file", "special_file", "7.2 special files rejected", build_zip_bytes(entries))
    entries = entry_dicts(base_entries(m5))
    entries[1]["flag"] = 1
    mal_raw("35_encrypted_zip", "encrypted_zip", "7.2 encrypted ZIP rejected", build_zip_bytes(entries))
    entries = entry_dicts(base_entries(m5))
    entries[1]["method"] = 12
    mal_raw("36_unsupported_compression", "unsupported_compression", "7.2 unsupported compression rejected",
            build_zip_bytes(entries))
    entries = entry_dicts(base_entries(m5))
    entries[1]["crc"] = (zlib.crc32(entries[1]["data"]) ^ 0xDEADBEEF) & 0xFFFFFFFF
    mal_raw("37_corrupt_crc", "corrupt_crc", "7.2 CRC mismatch rejected", build_zip_bytes(entries))
    mal("38_compression_bomb", "compression_bomb", "7.2 ratio > 1000 when > 1 MiB",
        base_entries(m5) + [("overrides/bomb.bin", b"\x00" * (2 * 1024 * 1024))])
    entries = entry_dicts(base_entries(m5))
    entries[1]["usize"] = 2 * 1024 * 1024 * 1024 + 1
    mal_raw("39_declared_file_too_large", "max_file_bytes", "7.2 single file <= 2 GiB", build_zip_bytes(entries))
    entries = entry_dicts(base_entries(m5)) + [
        {"name": "overrides/total-a.bin", "data": b"\x00" * 4096, "usize": 3 * 1024 * 1024 * 1024},
        {"name": "overrides/total-b.bin", "data": b"\x00" * 4096, "usize": 3 * 1024 * 1024 * 1024},
    ]
    mal_raw("40_declared_total_too_large", "max_unpacked_bytes", "7.2 unpacked total <= 6 GiB",
            build_zip_bytes(entries))
    # NOTE: 400000-entry limit is covered by internal/safe unit tests; a 400k-entry
    # fixture would be ~40 MB, so it is intentionally not generated here.

    mal("42_overture_format_2", "overture_format", "7.1 snapshot format 1 only",
        [("dsh-snapshot.json", json.dumps({"format": 2, "platform": "linux"}).encode()),
         ("profiles/demo/profile.yaml", b"name: demo\n")])
    mal("43_overture_missing_profile", "overture_missing_profile", "7.1 profiles/<id>/profile.yaml",
        [("dsh-snapshot.json", json.dumps({"format": 1, "platform": "linux"}).encode())])
    mal("44_overture_traversal_profile", "overture_path_traversal", "7.2 safe paths",
        [("dsh-snapshot.json", json.dumps({"format": 1, "platform": "linux"}).encode()),
         ("profiles/../evil/profile.yaml", b"name: evil\n")])
    mal("45_overture_invalid_yaml", "overture_invalid_yaml", "7.1 profile.yaml parse",
        [("dsh-snapshot.json", json.dumps({"format": 1, "platform": "linux"}).encode()),
         ("profiles/demo/profile.yaml", b"name: [unterminated\n")])
    m = manifest_v5(profile="../evil")
    mal("46_profile_name_traversal", "profile_name_traversal", "7.2 safe profile identifiers",
        [("dspack.json", marker(3)),
         ("manifest.json", json.dumps(m, separators=(",", ":")).encode()),
         ("overrides/../evil/package.json", b"{}")])

    with open(os.path.join(MAL, "cases.json"), "w", encoding="utf-8") as f:
        json.dump({"generator": "scripts/m3-gen-pack-samples.py", "cases": CASES}, f, ensure_ascii=False, indent=2)
        f.write("\n")
    with open(os.path.join(MAL, "SHA256SUMS"), "w", encoding="utf-8") as f:
        f.write("\n".join(sha256_file(os.path.join(MAL, c["file"])) + "  " + c["file"] for c in CASES) + "\n")


def generate_valid():
    if os.path.isdir(VAL):
        shutil.rmtree(VAL)
    os.makedirs(VAL, exist_ok=True)

    def valid(name, entries, kind):
        path = os.path.join(VAL, name + ".dspack")
        write_zip(path, entries)
        VALID.append({"file": name + ".dspack", "kind": kind, "size": os.path.getsize(path),
                      "sha256": sha256_file(path)})

    valid("dspack-v2-profile", base_entries(manifest_v4(), 2), "dspack v2 / manifest v4 / profile")
    valid("dspack-v3-profile", base_entries(manifest_v5(), 3), "dspack v3 / manifest v5 / profile")
    home_entries = [("dspack.json", marker(3)),
                    ("manifest.json", json.dumps(manifest_home_v5(), separators=(",", ":"), ensure_ascii=False).encode()),
                    ("overrides/profiles/m3home/package.json",
                     json.dumps({"dsh": {"profile": {"bundles": []}}, "dependencies": {}},
                                separators=(",", ":")).encode())]
    valid("dspack-v3-dshhome", home_entries, "dspack v3 / manifest v5 / dshhome")
    overture = [("dsh-snapshot.json", json.dumps({"format": 1, "platform": "linux"}).encode()),
                ("profiles/web/profile.yaml", b"name: 'Web title'\ndshVersion: '0.2.1-rc.3'\n"),
                ("profiles/web/package.json",
                 json.dumps({"dsh": {"profile": {"bundles": []}}, "dependencies": {}},
                            separators=(",", ":")).encode())]
    valid("overture-v1-minimal", overture, "Overture snapshot format 1")
    with open(os.path.join(VAL, "cases.json"), "w", encoding="utf-8") as f:
        json.dump({"generator": "scripts/m3-gen-pack-samples.py", "samples": VALID}, f, ensure_ascii=False, indent=2)
        f.write("\n")
    with open(os.path.join(VAL, "SHA256SUMS"), "w", encoding="utf-8") as f:
        f.write("\n".join(v["sha256"] + "  " + v["file"] for v in VALID) + "\n")


def generate_import_failure():
    """Valid-preview pack whose import must fail at pnpm dependency restore (rollback test)."""
    out = os.path.join(REPO, "testdata", "pack", "import-failure")
    if os.path.isdir(out):
        shutil.rmtree(out)
    os.makedirs(out, exist_ok=True)
    manifest = {
        "manifestVersion": 5, "type": "profile", "name": "failing-import",
        "version": "1.0.0", "profileName": "web", "dshVersion": "0.2.1-rc.3",
        "bundles": [], "dependencies": {"pcl-dsh-nonexistent-pkg-xyz": "0.0.1"},
    }
    entries = [
        ("dspack.json", marker(3)),
        ("manifest.json", json.dumps(manifest, separators=(",", ":")).encode()),
        ("overrides/web/package.json",
         json.dumps({"dsh": {"profile": {"bundles": []}},
                     "dependencies": {"pcl-dsh-nonexistent-pkg-xyz": "0.0.1"}},
                    separators=(",", ":")).encode()),
    ]
    path = os.path.join(out, "dspack-failing-deps.dspack")
    write_zip(path, entries)
    with open(os.path.join(out, "SHA256SUMS"), "w", encoding="utf-8") as f:
        f.write(sha256_file(path) + "  dspack-failing-deps.dspack\n")
    with open(os.path.join(out, "cases.json"), "w", encoding="utf-8") as f:
        json.dump({"generator": "scripts/m3-gen-pack-samples.py",
                   "samples": [{"file": "dspack-failing-deps.dspack",
                                "kind": "valid-preview / import must FAIL (dependency restore)",
                                "sha256": sha256_file(path),
                                "size": os.path.getsize(path)}]}, f, ensure_ascii=False, indent=2)
        f.write("\n")


def generate_plugin():
    os.makedirs(PLUGIN, exist_ok=True)
    pkg = os.path.join(PLUGIN, "local-test-plugin")
    if os.path.isdir(pkg):
        shutil.rmtree(pkg)
    os.makedirs(pkg, exist_ok=True)
    package_json = {"name": "@pcl-dsh/m3-test-plugin", "version": "0.1.0",
                    "description": "Synthetic local test plugin for PCL-DSH-Go M3 E2E (no runtime code needed)",
                    "main": "index.js", "files": ["index.js", "README.md"],
                    "license": "GPL-3.0-or-later"}
    with open(os.path.join(pkg, "package.json"), "w", encoding="utf-8") as f:
        json.dump(package_json, f, ensure_ascii=False, indent=2)
        f.write("\n")
    with open(os.path.join(pkg, "index.js"), "w", encoding="utf-8") as f:
        f.write("// Synthetic M3 test plugin. Not executed by acceptance code.\nmodule.exports = {};\n")
    with open(os.path.join(pkg, "README.md"), "w", encoding="utf-8") as f:
        f.write("# @pcl-dsh/m3-test-plugin\n\nLocal file: plugin used by M3 E2E (install/update/remove).\n"
                "Copy this directory into the instance workspace before adding the spec file:./local-test-plugin.\n")
    specs = [
        {"case": "leading_dash", "action": "add", "spec": "-evil", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "control_char", "action": "add", "spec": "evil\x01pkg", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "spec_too_long", "action": "add", "spec": "a" * 215, "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "invalid_npm_name", "action": "add", "spec": "@bad//name", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "file_escape_workspace", "action": "add", "spec": "file:../../outside", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "file_absolute", "action": "add", "spec": "file:/etc/passwd", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "link_escape", "action": "add", "spec": "link:../outside", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "builtin_bundle_remove", "action": "remove", "spec": "@deepseek-ai/dsh-base", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "remove_not_installed", "action": "remove", "spec": "not-installed-plugin", "expectedStatus": 400, "expectedCode": "VALIDATION"},
        {"case": "invalid_profile", "action": "add", "spec": "@pcl-dsh/m3-test-plugin", "profile": "../evil", "expectedStatus": 400, "expectedCode": "VALIDATION"},
    ]
    with open(os.path.join(PLUGIN, "malicious-specs.json"), "w", encoding="utf-8") as f:
        json.dump({"generator": "scripts/m3-gen-pack-samples.py", "specs": specs}, f, ensure_ascii=False, indent=2)
        f.write("\n")
    plugin_files = []
    for root, _dirs, names in os.walk(pkg):
        for name in sorted(names):
            full = os.path.join(root, name)
            plugin_files.append(os.path.relpath(full, PLUGIN))
    plugin_files.append("malicious-specs.json")
    with open(os.path.join(PLUGIN, "SHA256SUMS"), "w", encoding="utf-8") as f:
        f.write("\n".join(sha256_file(os.path.join(PLUGIN, rel)) + "  " + rel for rel in sorted(plugin_files)) + "\n")


def main():
    os.makedirs(os.path.join(REPO, "testdata"), exist_ok=True)
    generate_malicious()
    generate_valid()
    generate_import_failure()
    generate_plugin()
    print("malicious samples: %d" % len(CASES))
    print("valid samples: %d" % len(VALID))
    print("malicious dir: %s" % MAL)
    print("valid dir: %s" % VAL)
    print("plugin dir: %s" % PLUGIN)


if __name__ == "__main__":
    main()
