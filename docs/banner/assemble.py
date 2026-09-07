"""Turns the measured banner scene into docs/banner.svg (text as outlines, the tray ring animates) and a still SVG.

Usage: python3 assemble.py DOM_HTML   (DOM_HTML is scene.html + export.js dumped by headless Chromium; see build.sh)
Needs: pip install fonttools brotli
"""
import json, re, math, sys, os, html
here = os.path.dirname(os.path.abspath(__file__))
dom = open(sys.argv[1]).read()
ops = json.loads(html.unescape(re.search(r'<pre id="ops">(.*?)</pre>', dom, re.S).group(1)))
W, H = 1280, 640
FONT_DIR = os.path.join(here, '..', '..', 'apps', 'desktop', 'src', 'fonts')

def oklch_to_srgb(L, C, h):
    a = C * math.cos(math.radians(h)); b = C * math.sin(math.radians(h))
    l_ = L + 0.3963377774 * a + 0.2158037573 * b
    m_ = L - 0.1055613458 * a - 0.0638541728 * b
    s_ = L - 0.0894841775 * a - 1.2914855480 * b
    l, m, s = l_ ** 3, m_ ** 3, s_ ** 3
    r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
    g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
    bb = -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s
    def gam(c):
        c = min(1.0, max(0.0, c))
        return 12.92 * c if c <= 0.0031308 else 1.055 * c ** (1 / 2.4) - 0.055
    return tuple(round(gam(c) * 255) for c in (r, g, bb))

def color(c):
    """CSS color string -> (hex, alpha)."""
    c = c.strip()
    m = re.match(r'oklch\(\s*([\d.]+)(%?)\s+([\d.]+)\s+([\d.]+)\s*(?:/\s*([\d.]+)(%?))?\)', c)
    if m:
        L = float(m.group(1)) / (100 if m.group(2) else 1)
        A = 1.0 if m.group(5) is None else float(m.group(5)) / (100 if m.group(6) else 1)
        r, g, b = oklch_to_srgb(L, float(m.group(3)), float(m.group(4)))
        return '#%02x%02x%02x' % (r, g, b), A
    m = re.match(r'rgba?\(\s*([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)', c)
    if m:
        A = 1.0 if m.group(4) is None else float(m.group(4))
        return '#%02x%02x%02x' % tuple(int(float(m.group(i))) for i in (1, 2, 3)), A
    if c == 'transparent': return '#000000', 0.0
    if c.startswith('#'): return c, 1.0
    raise ValueError(c)

def fill_attrs(c, prop='fill'):
    hx, a = color(c)
    s = f' {prop}="{hx}"'
    if a < 1: s += f' {prop}-opacity="{a:.3g}"'
    return s

def esc(t): return t.replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')
def n(v): return f'{v:.2f}'.rstrip('0').rstrip('.')

from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
FONT_FILES = {'Geist': 'Geist-Variable.woff2', 'Geist Mono': 'GeistMono-Variable.woff2'}
_fonts, _sets, glyph_defs = {}, {}, {}
def font_for(fam):
    if fam not in _fonts: _fonts[fam] = TTFont(os.path.join(FONT_DIR, FONT_FILES[fam]))
    return _fonts[fam]
def glyph_id(fam, weight, ch):
    """Register the outline of one character at one weight; returns its def id, or None when the font lacks it."""
    f = font_for(fam); name = f.getBestCmap().get(ord(ch))
    if name is None: return None
    key = (fam, weight, name)
    if key not in glyph_defs:
        sk = (fam, weight)
        if sk not in _sets: _sets[sk] = f.getGlyphSet(location={'wght': int(weight)})
        pen = SVGPathPen(_sets[sk]); _sets[sk][name].draw(pen)
        gid = ('m' if 'Mono' in fam else 'g') + str(weight) + '-' + re.sub(r'[^A-Za-z0-9_-]', '_', name)
        glyph_defs[key] = (gid, pen.getCommands(), f['head'].unitsPerEm)
    return glyph_defs[key][0]

ACC = color('oklch(78% 0.17 65)')[0]
G, G2 = color('oklch(12.5% 0.012 60)')[0], color('oklch(16% 0.016 62)')[0]
glow1, glow2 = color('oklch(30% 0.05 62)')[0], color('oklch(24% 0.03 220)')[0]
beam_a, beam_b = color('oklch(78% 0.17 65)')[0], color('oklch(85% 0.1 200)')[0]

# CSS linear-gradient(112deg) across the frame: gradient line through the centre, length |w sin| + |h cos|.
th = math.radians(112); dx, dy = math.sin(th), -math.cos(th)
Lg = abs(W * math.sin(th)) + abs(H * math.cos(th))
bx1, by1 = W / 2 - dx * Lg / 2, H / 2 - dy * Lg / 2
bx2, by2 = W / 2 + dx * Lg / 2, H / 2 + dy * Lg / 2

def build(still):
    out = []
    out.append(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" role="img" aria-labelledby="title desc">')
    out.append('<title id="title">Prism. Your agents ask. You decide.</title>')
    out.append('<desc id="desc">A desktop scene. On the left: Your agents ask. You decide. On the right, the Prism tray icon glows amber and its panel shows a held call: Claude Code wants to call merge_pull_request on GitHub, with Allow once and Deny buttons, above a record of recent agent actions.</desc>')
    out.append('<defs>')
    if not still:
        out.append('<style>.ring-still{display:none;}@media (prefers-reduced-motion: reduce){.ring-anim{display:none;}.ring-still{display:inline;}}</style>')
    out.append('GLYPH_DEFS')
    out.append(f'<linearGradient id="base" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{G2}"/><stop offset="1" stop-color="{G}"/></linearGradient>')
    cx, cy = 0.78 * W, 0.18 * H
    out.append(f'<radialGradient id="glow1" gradientUnits="userSpaceOnUse" cx="{n(cx)}" cy="{n(cy)}" r="900" gradientTransform="translate(0 {n(cy)}) scale(1 {n(600/900)}) translate(0 {n(-cy)})"><stop offset="0" stop-color="{glow1}" stop-opacity="0.55"/><stop offset="0.65" stop-color="{glow1}" stop-opacity="0"/></radialGradient>')
    cx, cy = 0.20 * W, 0.90 * H
    out.append(f'<radialGradient id="glow2" gradientUnits="userSpaceOnUse" cx="{n(cx)}" cy="{n(cy)}" r="700" gradientTransform="translate(0 {n(cy)}) scale(1 {n(500/700)}) translate(0 {n(-cy)})"><stop offset="0" stop-color="{glow2}" stop-opacity="0.5"/><stop offset="0.6" stop-color="{glow2}" stop-opacity="0"/></radialGradient>')
    out.append(f'<linearGradient id="beam" gradientUnits="userSpaceOnUse" x1="{n(bx1)}" y1="{n(by1)}" x2="{n(bx2)}" y2="{n(by2)}"><stop offset="0.38" stop-color="{beam_a}" stop-opacity="0"/><stop offset="0.46" stop-color="{beam_a}" stop-opacity="0.06"/><stop offset="0.5" stop-color="{beam_b}" stop-opacity="0.05"/><stop offset="0.58" stop-color="{beam_b}" stop-opacity="0"/></linearGradient>')
    out.append('<filter id="shadowA" x="-20%" y="-15%" width="140%" height="135%" color-interpolation-filters="sRGB"><feDropShadow dx="0" dy="8" stdDeviation="12" flood-color="#000" flood-opacity="0.55"/></filter>')
    out.append('<filter id="shadowB" x="-10%" y="-10%" width="120%" height="120%" color-interpolation-filters="sRGB"><feDropShadow dx="0" dy="2" stdDeviation="3" flood-color="#000" flood-opacity="0.35"/></filter>')
    out.append('</defs>')
    out.append(f'<rect width="{W}" height="{H}" fill="url(#base)"/><rect width="{W}" height="{H}" fill="url(#glow1)"/><rect width="{W}" height="{H}" fill="url(#glow2)"/><rect width="{W}" height="{H}" fill="url(#beam)"/>')
    for o in ops:
        t = o['t']
        if t == 'rect':
            for ln in o['lines']:
                y = o['y'] + ln['dy'] if ln['dy'] < 0 else o['y'] + o['h']
                out.append(f'<rect x="{n(o["x"])}" y="{n(y)}" width="{n(o["w"])}" height="{n(abs(ln["dy"]))}"{fill_attrs(ln["color"])}/>')
            hx, a = color(o['fill'])
            attrs = f'x="{n(o["x"])}" y="{n(o["y"])}" width="{n(o["w"])}" height="{n(o["h"])}" rx="{n(o["rx"])}"'
            if o['soft'] and o['tag'] == 'shadow':
                x, y, w, h, r = o['x'] + 8, o['y'] + 8, o['w'] - 16, o['h'] - 16, max(0, o['rx'] - 8)
                out.append(f'<rect x="{n(x)}" y="{n(y)}" width="{n(w)}" height="{n(h)}" rx="{n(r)}" fill="{hx}" filter="url(#shadowA)"/>')
                out.append(f'<rect {attrs} fill="{hx}" filter="url(#shadowB)"/>')
            body = f' fill="{hx}"' + (f' fill-opacity="{a:.3g}"' if a < 1 else '') if a > 0 else ' fill="none"'
            if o['stroke']:
                body += fill_attrs(o['stroke']['color'], 'stroke') + ' stroke-width="1"'
            out.append(f'<rect {attrs}{body}/>')
        elif t == 'text':
            fam = o['family'].split(',')[0].strip().strip('"\'')
            s = o['size'] / 1000.0
            uses = []
            for ch, x in o['chars']:
                if ch.isspace(): continue
                gid = glyph_id(fam, o['weight'], ch)
                if gid is None:
                    uses.append(f'<text x="{n(x)}" y="{n(o["y"])}" font-family="system-ui, sans-serif" font-size="{n(o["size"])}" font-weight="{o["weight"]}">{esc(ch)}</text>')
                else:
                    uses.append(f'<use href="#{gid}" transform="translate({n(x)} {n(o["y"])}) scale({s:.4g} -{s:.4g})"/>')
            out.append(f'<g{fill_attrs(o["fill"])}>' + ''.join(uses) + '</g>')
            if o.get('underline'):
                out.append(f'<rect x="{n(o["x"])}" y="{n(o["y"] + 3)}" width="{n(o["w"])}" height="1"{fill_attrs(o["underline"])}/>')
        elif t == 'svg':
            inner = re.sub(r'oklch\([^)]*\)', lambda m: color(m.group(0))[0], o['inner'])
            out.append(f'<g transform="translate({n(o["x"])} {n(o["y"])}) scale({n(o["sx"])} {n(o["sy"])})">{inner}</g>')
        elif t == 'ring':
            hx, _ = color(o['color']); cx, cy, r = o['cx'], o['cy'], o['r']
            if still:
                out.append(f'<circle cx="{n(cx)}" cy="{n(cy)}" r="{n(r * 1.02)}" fill="none" stroke="{hx}" stroke-width="1.5" opacity="0.55"/>')
            else:
                out.append(f'<circle class="ring-anim" cx="{n(cx)}" cy="{n(cy)}" r="{n(r * 0.55)}" fill="none" stroke="{hx}" stroke-width="1.5" opacity="0.9">'
                           f'<animate attributeName="r" values="{n(r * 0.55)};{n(r * 1.5)}" dur="1.6s" repeatCount="indefinite" calcMode="spline" keySplines="0.16 1 0.3 1"/>'
                           f'<animate attributeName="opacity" values="0.9;0" dur="1.6s" repeatCount="indefinite" calcMode="spline" keySplines="0.16 1 0.3 1"/></circle>')
                out.append(f'<circle class="ring-still" cx="{n(cx)}" cy="{n(cy)}" r="{n(r * 1.02)}" fill="none" stroke="{hx}" stroke-width="1.5" opacity="0.55"/>')
    out.append('</svg>')
    defs = ''.join(f'<path id="{gid}" d="{d}"/>' for gid, d, upem in glyph_defs.values())
    return '\n'.join(out).replace('GLYPH_DEFS', defs)

open(os.path.join(here, '..', 'banner.svg'), 'w').write(build(False))
open(os.path.join(here, 'tmp-still.svg'), 'w').write(build(True))
print('wrote docs/banner.svg', os.path.getsize(os.path.join(here, '..', 'banner.svg')), 'bytes')
