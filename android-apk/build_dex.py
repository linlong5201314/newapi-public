#!/usr/bin/env python3
"""Retired hand-written DEX assembler.

The old implementation did not compile MainActivity.java and silently omitted
its lifecycle and security behavior. Use build_apk.py: javac -> D8 -> aapt ->
zipalign -> apksigner. It requires an explicit pre-built --web-dist directory.
"""
import sys

if __name__ == '__main__':
    sys.exit('Hand-written placeholder DEX is no longer supported. Run python -I build_apk.py --help and use the real Java/D8 build.')
