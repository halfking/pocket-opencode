#!/usr/bin/env python3
"""Generate R6 app-link associations from the distributed APK and iOS app."""

import argparse
import hashlib
import json
from pathlib import Path
import plistlib
import re
import subprocess
import sys
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET
from zipfile import BadZipFile, ZipFile


RETURN_PATH = "/auth/sso/app-return"
START_PATH = "/api/auth/sso/native/start"
PACKAGE = "com.kaixuan.opencode.pocket"
ANDROID = "{http://schemas.android.com/apk/res/android}"
ORIGIN = re.compile(r"https://([a-z0-9-]+(?:\.[a-z0-9-]+)+)")
APP_ID = re.compile(rf"[A-Z0-9]{{10}}\.{re.escape(PACKAGE)}")
MAX_ASSOCIATION_BYTES = 128 * 1024


def checked_command(args, purpose):
    result = subprocess.run(args, capture_output=True, check=False, timeout=90)
    if result.returncode:
        raise ValueError(f"{purpose} failed")
    return result.stdout, result.stderr


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fingerprint(value):
    normalized = value.strip().replace(":", "").lower()
    if not re.fullmatch(r"[0-9a-f]{64}", normalized):
        raise ValueError("Android signing certificate SHA-256 must contain 64 hex digits")
    return normalized


def contains_app_link_bundle(scripts, origin):
    values = tuple(item.encode("ascii") for item in (origin, RETURN_PATH, START_PATH))
    return any(all(value in script for value in values) for script in scripts)


def covers_android_host(entry, expected):
    host = entry.get(ANDROID + "host", "")
    return host == expected or (host.startswith("*") and expected.endswith(host[1:]))


def assert_android_manifest(data, host):
    manifest = ET.fromstring(data)
    if manifest.get("package") != PACKAGE:
        raise ValueError("APK package does not match the OpenPocket release")
    application = manifest.find("application")
    if application is None or application.get(ANDROID + "debuggable") == "true":
        raise ValueError("APK must be a non-debuggable release")
    filters = [
        intent for intent in application.findall(".//intent-filter")
        if any(covers_android_host(entry, host) for entry in intent.findall("data"))
    ]
    if len(filters) != 1:
        raise ValueError("APK must declare exactly one filter for the return host")
    intent = filters[0]
    data_entries = intent.findall("data")
    required = {ANDROID + "scheme": "https", ANDROID + "host": host, ANDROID + "path": RETURN_PATH}
    actions = {entry.get(ANDROID + "name") for entry in intent.findall("action")}
    categories = {entry.get(ANDROID + "name") for entry in intent.findall("category")}
    if (intent.get(ANDROID + "autoVerify") != "true" or len(data_entries) != 1
            or data_entries[0].attrib != required or actions != {"android.intent.action.VIEW"}
            or not {"android.intent.category.DEFAULT", "android.intent.category.BROWSABLE"} <= categories):
        raise ValueError(f"APK must verify the exact HTTPS {RETURN_PATH} path")


def android_association(apk, expected_fingerprint, origin, host, apksigner, apkanalyzer):
    expected = fingerprint(expected_fingerprint)
    signed, _ = checked_command([apksigner, "verify", "--print-certs", str(apk)], "Android APK signature verification")
    values = re.findall(rb"^Signer #\d+ certificate SHA-256 digest: ([0-9a-fA-F:]+)\s*$", signed, re.M)
    if [fingerprint(value.decode("ascii")) for value in values] != [expected]:
        raise ValueError("APK signer does not match the expected release signing certificate")
    manifest, _ = checked_command([apkanalyzer, "manifest", "print", str(apk)], "APK manifest inspection")
    assert_android_manifest(manifest, host)
    with ZipFile(apk) as archive:
        scripts = (archive.read(name) for name in archive.namelist()
                   if name.startswith("assets/public/assets/") and name.endswith(".js"))
        if not contains_app_link_bundle(scripts, origin):
            raise ValueError("APK WebView bundle does not contain the expected R6 return origin and routes")
    display_fingerprint = ":".join(expected[index:index + 2] for index in range(0, len(expected), 2)).upper()
    return [{
        "relation": ["delegate_permission/common.handle_all_urls"],
        "target": {"namespace": "android_app", "package_name": PACKAGE,
                   "sha256_cert_fingerprints": [display_fingerprint]},
    }], {"apk_sha256": sha256(apk), "signing_certificate_sha256": display_fingerprint}


def ios_association(app, expected_app_id, origin, host):
    if not APP_ID.fullmatch(expected_app_id):
        raise ValueError("Expected iOS application identifier must include the App ID prefix and OpenPocket bundle ID")
    info = plistlib.loads((app / "Info.plist").read_bytes())
    if info.get("CFBundleIdentifier") != PACKAGE or "iPhoneOS" not in info.get("CFBundleSupportedPlatforms", []):
        raise ValueError("iOS input must be an OpenPocket device build")
    checked_command(["codesign", "--verify", "--deep", "--strict", "-R=anchor apple generic", str(app)],
                    "iOS device app signature verification")
    _, identity = checked_command(["codesign", "--display", "--verbose=4", str(app)], "iOS signing identity inspection")
    if not re.search(rb"^Authority=(?:Apple Distribution|iPhone Distribution):", identity, re.M):
        raise ValueError("iOS app must use an Apple distribution signing identity")
    entitlements, _ = checked_command(["codesign", "--display", "--entitlements", "-", "--xml", str(app)],
                                      "iOS signed entitlement inspection")
    signed = plistlib.loads(entitlements)
    if signed.get("application-identifier") != expected_app_id or signed.get("get-task-allow", False) is not False:
        raise ValueError("iOS app identifier or distribution entitlements do not match the release")
    if "applinks:" + host not in signed.get("com.apple.developer.associated-domains", []):
        raise ValueError("iOS signature does not include the exact return host in Associated Domains")
    scripts = (path.read_bytes() for path in (app / "public/assets").glob("*.js"))
    if not contains_app_link_bundle(scripts, origin):
        raise ValueError("iOS WebView bundle does not contain the expected R6 return origin and routes")
    executable = info.get("CFBundleExecutable", "")
    if not executable or executable != Path(executable).name:
        raise ValueError("iOS bundle has an invalid executable name")
    return {"applinks": {"details": [{"appIDs": [expected_app_id], "components": [{"/": RETURN_PATH}]}]}}, {
        "executable_sha256": sha256(app / executable), "application_identifier": expected_app_id,
    }


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def unique_json_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Hosted association contains a duplicate JSON key")
        result[key] = value
    return result


def check_hosted(origin, files):
    client = urllib.request.build_opener(NoRedirect())
    for name, content in files.items():
        try:
            with client.open(origin + "/.well-known/" + name, timeout=8) as response:
                if response.status != 200 or response.headers.get_content_type() != "application/json":
                    raise ValueError(f"Hosted {name} must return HTTP 200 and application/json")
                body = response.read(MAX_ASSOCIATION_BYTES + 1)
        except (urllib.error.URLError, TimeoutError) as exc:
            raise ValueError(f"Hosted {name} must be served directly over HTTPS") from exc
        if len(body) > MAX_ASSOCIATION_BYTES or json.loads(body, object_pairs_hook=unique_json_object) != content:
            raise ValueError(f"Hosted {name} differs from the association extracted from the signed artifact")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--origin", required=True)
    parser.add_argument("--output", type=Path, required=True, help="new directory for association files and receipt")
    parser.add_argument("--android-apk", type=Path)
    parser.add_argument("--android-fingerprint", help="approved release certificate SHA-256 as hex")
    parser.add_argument("--ios-app", type=Path, help="distribution-signed device .app from the exported release")
    parser.add_argument("--ios-app-id", help="approved application-identifier including its App ID prefix")
    parser.add_argument("--apksigner", default="apksigner")
    parser.add_argument("--apkanalyzer", default="apkanalyzer")
    parser.add_argument("--check-hosted", action="store_true", help="compare deployed HTTPS files with generated content")
    args = parser.parse_args()
    match = ORIGIN.fullmatch(args.origin)
    if not match:
        parser.error("--origin must be an HTTPS DNS origin without a trailing slash or port")
    if bool(args.android_apk) != bool(args.android_fingerprint) or bool(args.ios_app) != bool(args.ios_app_id):
        parser.error("each artifact requires its expected signing identity")
    if not (args.android_apk or args.ios_app):
        parser.error("pass at least one distribution artifact")
    try:
        host = match[1]
        files = {}
        receipt = {"origin": args.origin, "return_path": RETURN_PATH, "hosted_check": args.check_hosted}
        if args.android_apk:
            files["assetlinks.json"], receipt["android"] = android_association(
                args.android_apk, args.android_fingerprint, args.origin, host, args.apksigner, args.apkanalyzer,
            )
        if args.ios_app:
            files["apple-app-site-association"], receipt["ios"] = ios_association(
                args.ios_app, args.ios_app_id, args.origin, host,
            )
        if args.check_hosted:
            check_hosted(args.origin, files)
        args.output.mkdir()
        for name, value in {**files, "receipt.json": receipt}.items():
            (args.output / name).write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    except (ValueError, OSError, BadZipFile, ET.ParseError, plistlib.InvalidFileException, subprocess.TimeoutExpired) as exc:
        print(f"R6 app association verification failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
