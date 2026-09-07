#!/usr/bin/env sh
# Rebuilds docs/banner.svg and docs/banner.png from scene.html.
# Needs Chromium (CHROMIUM=... to point at it) and a Python with fonttools + brotli (PYTHON=... to pick one).
# Snap Chromium can only read files under $HOME, so run this from a checkout inside your home directory.
set -e
cd "$(dirname "$0")"
CHROMIUM="${CHROMIUM:-chromium}"
PYTHON="${PYTHON:-python3}"
sed 's#</body>#<script src="./export.js"></script></body>#' scene.html > tmp-export.html
"$CHROMIUM" --headless=new --disable-gpu --window-size=1280,640 --virtual-time-budget=6000 --dump-dom "file://$PWD/tmp-export.html" > tmp-dom.html 2>/dev/null
"$PYTHON" assemble.py tmp-dom.html
printf '%s' '<!doctype html><html><head><meta charset="utf-8"><style>html,body{margin:0;background:#000}</style></head><body><img src="./tmp-still.svg" width="1280" height="640" style="display:block"></body></html>' > tmp-img.html
"$CHROMIUM" --headless=new --disable-gpu --hide-scrollbars --window-size=1280,640 --force-device-scale-factor=2 --virtual-time-budget=3000 --screenshot="$PWD/../banner.png" "file://$PWD/tmp-img.html" 2>/dev/null
rm -f tmp-export.html tmp-dom.html tmp-img.html tmp-still.svg
echo "wrote docs/banner.png"
