import importlib.util
from pathlib import Path
import plistlib
import tempfile
import unittest
from unittest.mock import patch


script = Path(__file__).resolve().parents[1] / "prepare-r6-app-associations.py"
spec = importlib.util.spec_from_file_location("associations", script)
associations = importlib.util.module_from_spec(spec)
spec.loader.exec_module(associations)

HOST = "openpocket-app.example.org"
ORIGIN = "https://" + HOST
APP_ID = "ABCDE12345.com.kaixuan.opencode.pocket"
MANIFEST = f"""<manifest xmlns:android="http://schemas.android.com/apk/res/android"
  package="com.kaixuan.opencode.pocket">
  <application><activity>
    <intent-filter android:autoVerify="true">
      <action android:name="android.intent.action.VIEW"/>
      <category android:name="android.intent.category.DEFAULT"/>
      <category android:name="android.intent.category.BROWSABLE"/>
      <data android:scheme="https" android:host="{HOST}" android:path="/auth/sso/app-return"/>
    </intent-filter>
  </activity></application>
</manifest>"""


class AppAssociationTests(unittest.TestCase):
    def test_manifest_rejects_widening_and_debug_mode(self):
        associations.assert_android_manifest(MANIFEST, HOST)
        for manifest in (
            MANIFEST.replace("android:path=", "android:pathPrefix="),
            MANIFEST.replace("android:autoVerify=\"true\"", "android:autoVerify=\"false\""),
            MANIFEST.replace("<application>", '<application android:debuggable="true">'),
            MANIFEST.replace("</activity>", """
              <intent-filter><data android:scheme="https" android:host="*.example.org"/></intent-filter>
              </activity>"""),
        ):
            with self.subTest(manifest=manifest), self.assertRaises(ValueError):
                associations.assert_android_manifest(manifest, HOST)

    def test_ios_uses_signed_application_identifier_and_exact_host(self):
        with tempfile.TemporaryDirectory() as temp:
            app = Path(temp) / "App.app"
            asset = app / "public/assets/LoginView.js"
            asset.parent.mkdir(parents=True)
            asset.write_text(f"{ORIGIN} /auth/sso/app-return /api/auth/sso/native/start", encoding="utf-8")
            (app / "App").write_bytes(b"fixture executable")
            (app / "Info.plist").write_bytes(plistlib.dumps({
                "CFBundleIdentifier": associations.PACKAGE,
                "CFBundleSupportedPlatforms": ["iPhoneOS"],
                "CFBundleExecutable": "App",
            }))
            unsigned_config = {
                "application-identifier": APP_ID,
                "com.apple.developer.associated-domains": ["applinks:" + HOST],
            }

            def output(command, purpose):
                if "--verify" in command:
                    self.assertIn("-R=anchor apple generic", command)
                    return b"", b""
                if "--verbose=4" in command:
                    return b"", identity
                return plistlib.dumps(entitlements), b""

            identity = b"Authority=Apple Distribution: Fixture\n"
            entitlements = dict(unsigned_config)
            with patch.object(associations, "checked_command", side_effect=output):
                links, receipt = associations.ios_association(app, APP_ID, ORIGIN, HOST)
                self.assertEqual(receipt["application_identifier"], APP_ID)
                self.assertEqual(links["applinks"]["details"], [
                    {"appIDs": [APP_ID], "components": [{"/": "/auth/sso/app-return"}]},
                ])
                for invalid in (
                    {"application-identifier": "ZZZZZ12345.com.kaixuan.opencode.pocket"},
                    {"get-task-allow": True},
                    {"com.apple.developer.associated-domains": ["applinks:*.example.org"]},
                ):
                    entitlements = {**unsigned_config, **invalid}
                    with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                        associations.ios_association(app, APP_ID, ORIGIN, HOST)
                entitlements = unsigned_config
                identity = b"Authority=Apple Development: Fixture\n"
                with self.assertRaisesRegex(ValueError, "distribution signing identity"):
                    associations.ios_association(app, APP_ID, ORIGIN, HOST)

    def test_hosted_association_does_not_accept_duplicate_keys(self):
        with self.assertRaises(ValueError):
            associations.unique_json_object([("applinks", {}), ("applinks", {})])


if __name__ == "__main__":
    unittest.main()
