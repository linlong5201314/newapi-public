import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('task12_build_apk', ROOT / 'build_apk.py')
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class BuildSafetyTest(unittest.TestCase):
    def test_normal_https_origin_is_preserved_without_trailing_slash(self):
        self.assertEqual(build.validate_origin('https://gw.ailong.love/'), 'https://gw.ailong.love')

    def test_insecure_or_credential_bearing_build_origin_is_rejected(self):
        for origin in ['http://gw.ailong.love', 'https://user:password@example.com', 'https://example.com/path', 'https://example.com?token=secret', 'javascript:alert(1)']:
            with self.subTest(origin=origin), self.assertRaises(ValueError):
                build.validate_origin(origin)

    def test_original_hand_written_shell_is_not_a_complete_apk(self):
        legacy = ROOT.parent.parent / 'DeepSeek-NewAPI-Client.apk'
        with self.assertRaises(ValueError):
            build.verify_contents(legacy)

    def test_verified_artifact_contains_real_dex_and_full_frontend(self):
        artifacts = list((ROOT / 'artifacts/task12-final').glob('DeepSeek-NewAPI-Client-*-debug-signed.apk'))
        if not artifacts:
            artifacts = list((ROOT / 'artifacts/task12-verified').glob('DeepSeek-NewAPI-Client-*-debug-signed.apk'))
        self.assertTrue(artifacts, 'Run build_apk.py against the production dist first')
        artifact = max(artifacts, key=lambda path: path.stat().st_mtime)
        result = build.verify_contents(artifact)
        self.assertGreater(result['dex_bytes'], 8000)
        self.assertGreater(result['web_uncompressed_bytes'], 1_000_000)
        self.assertGreater(result['web_files'], 20)


if __name__ == '__main__':
    unittest.main()
