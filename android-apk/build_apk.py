#!/usr/bin/env python3
"""Build the existing Android client from Java and a pre-built NewAPI frontend.

No account/API secrets are accepted. Release signing uses environment passwords;
--debug-sign explicitly opts into an install-test certificate, not a release key.
Run with python -I. Frontend production build is a separate, coordinated step.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from urllib.parse import urlsplit
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parent
ANDROID = '{http://schemas.android.com/apk/res/android}'


def run(args, *, capture=False):
    # Never log signing passwords or dump process environments.
    print('RUN', Path(str(args[0])).name, ' '.join(str(x) for x in args[1:] if not str(x).startswith('env:')), flush=True)
    result = subprocess.run([str(x) for x in args], cwd=ROOT, check=True,
                            stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.STDOUT if capture else None, encoding='utf-8', errors='replace')
    return result.stdout or ''


def sha256(path):
    return hashlib.file_digest(path.open('rb'), 'sha256').hexdigest()


def validate_origin(origin):
    parts = urlsplit(origin)
    if parts.scheme != 'https' or not parts.hostname or parts.username or parts.password or parts.query or parts.fragment or parts.path not in ('', '/'):
        raise ValueError('site-origin must be HTTPS and contain no credentials, query or path')
    if not re.fullmatch(r'https://[A-Za-z0-9.:-]+/?', origin):
        raise ValueError('site-origin must be a plain HTTPS origin')
    return origin.rstrip('/')


def verify_contents(apk):
    with zipfile.ZipFile(apk) as archive:
        bad = archive.testzip()
        if bad:
            raise ValueError('Corrupt APK entry: ' + bad)
        names = archive.namelist()
        dex = archive.read('classes.dex')
        if not dex.startswith(b'dex\n') or len(dex) < 8000:
            raise ValueError('No substantive compiled DEX')
        required = [b'Lcom/newapi/deepseek/MainActivity;', b'Lcom/newapi/deepseek/AppPolicy;']
        if any(name not in dex for name in required):
            raise ValueError('APK missing compiled Activity or navigation policy')
        web = [name for name in names if name.startswith('assets/www/')]
        total = sum(archive.getinfo(name).file_size for name in web)
        if 'assets/www/index.html' not in names or len(web) < 20 or total < 1_000_000:
            raise ValueError('Frontend bundle is missing or a placeholder')
        if any(name.lower().endswith(('.apk', '.keystore', '.jks', '.env', '.map')) for name in names):
            raise ValueError('APK contains forbidden build input or signing material')
        return {'dex_bytes': len(dex), 'web_files': len(web), 'web_uncompressed_bytes': total, 'zip_entries': len(names)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sdk', required=True, type=Path)
    parser.add_argument('--java-home', required=True, type=Path)
    parser.add_argument('--r8', required=True, type=Path)
    parser.add_argument('--web-dist', required=True, type=Path)
    parser.add_argument('--site-origin', default='https://gw.ailong.love')
    parser.add_argument('--out-dir', default=ROOT / 'artifacts', type=Path)
    parser.add_argument('--debug-sign', action='store_true')
    parser.add_argument('--keystore', type=Path)
    parser.add_argument('--key-alias')
    args = parser.parse_args()
    origin = validate_origin(args.site_origin)
    sdk, jdk, r8, web = (path.resolve() for path in (args.sdk, args.java_home, args.r8, args.web_dist))
    tools = sdk / 'build-tools' / '34.0.0'
    platform = sdk / 'platforms' / 'android-34' / 'android.jar'
    java = jdk / 'bin' / 'java.exe'
    javac = jdk / 'bin' / 'javac.exe'
    for path in [java, javac, r8, platform, tools / 'aapt.exe', tools / 'zipalign.exe', tools / 'lib/apksigner.jar', web / 'index.html']:
        if not path.is_file():
            raise FileNotFoundError(path)
    manifest = ET.parse(ROOT / 'AndroidManifest.xml').getroot()
    version = manifest.attrib[ANDROID + 'versionName']
    stamp = datetime.now(timezone.utc).strftime('%Y%m%d')
    output = args.out_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)
    filename = 'DeepSeek-NewAPI-Client-' + version + '-' + stamp + ('-debug-signed' if args.debug_sign else '-release') + '.apk'
    apk = output / filename
    if apk.exists():
        raise FileExistsError('Refusing to overwrite existing artifact: ' + str(apk))
    build = ROOT / 'build' / version
    if build.exists():
        shutil.rmtree(build)
    for name in ['classes', 'gen/com/newapi/deepseek', 'dex', 'assets/www']:
        (build / name).mkdir(parents=True, exist_ok=True)
    copied = 0
    for source in web.rglob('*'):
        if not source.is_file() or source.is_symlink():
            continue
        if source.suffix.lower() in {'.apk', '.map', '.keystore', '.jks'} or source.name.startswith('.'):
            continue
        target = build / 'assets/www' / source.relative_to(web)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        copied += 1
    if copied < 20:
        raise ValueError('Not a complete production frontend')
    # Distribute project licensing and provenance without copying any workspace credentials.
    for name in ['LICENSE', 'NOTICE', 'THIRD-PARTY-LICENSES.md']:
        source = ROOT.parent / name
        if source.is_file():
            shutil.copy2(source, build / 'assets' / name)
    config = build / 'gen/com/newapi/deepseek/BuildConfig.java'
    config.write_text('package com.newapi.deepseek;\npublic final class BuildConfig {\n public static final String SITE_ORIGIN = ' + json.dumps(origin) + ';\n}\n', encoding='utf-8')
    unsigned = build / 'unsigned.apk'
    run([tools / 'aapt.exe', 'package', '-f', '-M', ROOT / 'AndroidManifest.xml', '-S', ROOT / 'res', '-I', platform, '-A', build / 'assets', '-J', build / 'gen', '-F', unsigned])
    sources = sorted((ROOT / 'src').rglob('*.java')) + sorted((build / 'gen').rglob('*.java'))
    run([javac, '-encoding', 'UTF-8', '--release', '8', '-classpath', platform, '-d', build / 'classes', *sources])
    classes = sorted((build / 'classes').rglob('*.class'))
    run([java, '-cp', r8, 'com.android.tools.r8.D8', '--release', '--min-api', '24', '--lib', platform, '--output', build / 'dex', *classes])
    with zipfile.ZipFile(unsigned, 'a', compression=zipfile.ZIP_DEFLATED) as archive:
        archive.write(build / 'dex/classes.dex', 'classes.dex')
    aligned = build / 'aligned.apk'
    run([tools / 'zipalign.exe', '-f', '-p', '4', unsigned, aligned])
    if args.debug_sign:
        keystore = args.keystore or Path.home() / '.android/debug.keystore'
        alias = args.key_alias or 'androiddebugkey'
        os.environ['TASK12_STORE_PASSWORD'] = 'android'
        os.environ['TASK12_KEY_PASSWORD'] = 'android'
    else:
        if not args.keystore or not args.key_alias:
            raise ValueError('Release signing requires --keystore and --key-alias')
        keystore, alias = args.keystore, args.key_alias
        os.environ['TASK12_STORE_PASSWORD'] = os.environ['ANDROID_KEYSTORE_PASSWORD']
        os.environ['TASK12_KEY_PASSWORD'] = os.environ.get('ANDROID_KEY_PASSWORD', os.environ['ANDROID_KEYSTORE_PASSWORD'])
    run([java, '-jar', tools / 'lib/apksigner.jar', 'sign', '--ks', keystore, '--ks-key-alias', alias, '--ks-pass', 'env:TASK12_STORE_PASSWORD', '--key-pass', 'env:TASK12_KEY_PASSWORD', '--v1-signing-enabled', 'true', '--v2-signing-enabled', 'true', '--v3-signing-enabled', 'true', '--out', apk, aligned])
    os.environ.pop('TASK12_STORE_PASSWORD', None)
    os.environ.pop('TASK12_KEY_PASSWORD', None)
    signature = run([java, '-jar', tools / 'lib/apksigner.jar', 'verify', '--verbose', '--print-certs', apk], capture=True)
    print(signature)
    run([tools / 'zipalign.exe', '-c', '-v', '4', apk], capture=True)
    # aapt's legacy Windows ziparchive cannot open non-ASCII absolute paths.
    apk_relative = Path(os.path.relpath(apk, ROOT))
    badging = run([tools / 'aapt.exe', 'dump', 'badging', apk_relative], capture=True)
    if "package: name='com.newapi.deepseek'" not in badging or "launchable-activity: name='com.newapi.deepseek.MainActivity'" not in badging:
        raise ValueError('Unexpected package or entry Activity')
    manifest_dump = run([tools / 'aapt.exe', 'dump', 'xmltree', apk_relative, 'AndroidManifest.xml'], capture=True)
    for key in ['usesCleartextTraffic', 'allowBackup', 'debuggable']:
        if not re.search(r'android:' + key + r'\(.*?\)=\(type 0x12\)0x0', manifest_dump):
            raise ValueError('Unsafe or missing manifest flag: ' + key)
    report = {
        'apk': str(apk), 'bytes': apk.stat().st_size, 'sha256': sha256(apk),
        'version_name': version, 'version_code': manifest.attrib[ANDROID + 'versionCode'],
        'site_origin': origin, 'signing': 'debug-install-test-only' if args.debug_sign else 'release',
        'r8_sha256': sha256(r8), 'frontend_index_sha256': sha256(web / 'index.html'),
        **verify_contents(apk),
    }
    if report['bytes'] < 1_000_000:
        raise ValueError('Refusing placeholder-size APK')
    prefix = output / apk.stem
    Path(str(prefix) + '.verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    Path(str(prefix) + '.signature.txt').write_text(signature, encoding='utf-8')
    Path(str(prefix) + '.manifest.txt').write_text(manifest_dump, encoding='utf-8')
    Path(str(prefix) + '.badging.txt').write_text(badging, encoding='utf-8')
    apk.with_suffix('.apk.sha256').write_text(report['sha256'] + '  ' + apk.name + '\n', encoding='utf-8')
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    sys.stdout.reconfigure(encoding='utf-8')
    main()
