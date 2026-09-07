document.fonts.ready.then(() => setTimeout(run, 400));
function run() {
  const stage = document.getElementById('stage');
  const S = stage.getBoundingClientRect();
  const ops = [];
  const rx = (v) => parseFloat(v) || 0;
  const alpha = (c) => { const m = c.match(/\/\s*([\d.]+)\)$/); if (m) return parseFloat(m[1]); const r = c.match(/rgba\([^)]*,\s*([\d.]+)\)/); return r ? parseFloat(r[1]) : (c === 'transparent' ? 0 : 1); };
  const shadows = (str) => { const out = []; const re = /((?:rgba?|oklch|color)\([^)]*\))\s+(-?[\d.]+)px\s+(-?[\d.]+)px\s+(-?[\d.]+)px(?:\s+(-?[\d.]+)px)?(\s+inset)?/g; let m; while ((m = re.exec(str))) out.push({ color: m[1], dx: +m[2], dy: +m[3], blur: +m[4], spread: +(m[5] || 0), inset: !!m[6] }); return out; };
  const box = (el, cs) => {
    const r = el.getBoundingClientRect();
    const o = { t: 'rect', x: r.left - S.left, y: r.top - S.top, w: r.width, h: r.height, rx: rx(cs.borderTopLeftRadius), fill: cs.backgroundColor, tag: el.dataset.svg || '' };
    if (o.rx > Math.min(o.w, o.h) / 2) o.rx = Math.min(o.w, o.h) / 2;
    const sh = shadows(cs.boxShadow || '');
    o.stroke = null; o.soft = []; o.lines = [];
    for (const s of sh) {
      if (s.blur === 0 && s.spread === 1 && s.dx === 0 && s.dy === 0) o.stroke = { color: s.color, inset: s.inset };
      else if (s.blur === 0 && s.spread === 0 && s.dx === 0 && s.dy !== 0) o.lines.push({ dy: s.dy, color: s.color });
      else o.soft.push(s);
    }
    if (alpha(o.fill) > 0 || o.stroke || o.soft.length || o.lines.length) ops.push(o);
  };
  const svg = (el) => {
    const r = el.getBoundingClientRect(); const vb = el.viewBox.baseVal;
    ops.push({ t: 'svg', x: r.left - S.left, y: r.top - S.top, sx: r.width / vb.width, sy: r.height / vb.height, inner: el.innerHTML, tag: el.dataset.svg || '' });
  };
  const text = (node) => {
    const parent = node.parentElement; const cs = getComputedStyle(parent);
    const raw = node.textContent; if (!raw.trim()) return;
    const pre = /^pre/.test(cs.whiteSpace);
    const tf = cs.textTransform;
    let wrap = null;
    if (/flex|grid/.test(cs.display)) { wrap = document.createElement('span'); parent.insertBefore(wrap, node); wrap.appendChild(node); }
    const lines = []; let cur = null; let first = -1;
    for (let i = 0; i < raw.length; i++) {
      const rg = document.createRange(); rg.setStart(node, i); rg.setEnd(node, i + 1);
      const rects = rg.getClientRects(); if (!rects.length) continue; const cr = rects[0];
      if (cr.width === 0 && /\s/.test(raw[i])) continue;
      if (first < 0) first = i;
      if (!cur || Math.abs(cr.top - cur.top) > 1) { cur = { top: cr.top, left: cr.left, right: cr.right, s: '', chars: [] }; lines.push(cur); }
      cur.s += raw[i]; cur.right = cr.right; cur.chars.push([tf === 'uppercase' ? raw[i].toUpperCase() : raw[i], cr.left - S.left]);
    }
    if (!lines.length) { if (wrap) { parent.insertBefore(node, wrap); wrap.remove(); } return; }
    // Baseline: a zero-size inline-block dropped right before the first rendered character sits on that line's baseline.
    const probe = document.createElement('span'); probe.style.cssText = 'display:inline-block;width:0;height:0;';
    const at = document.createRange(); at.setStart(node, first); at.setEnd(node, first); at.insertNode(probe);
    const base = probe.getBoundingClientRect().bottom; probe.remove();
    if (wrap) { while (wrap.firstChild) parent.insertBefore(wrap.firstChild, wrap); wrap.remove(); }
    parent.normalize();
    for (const l of lines) {
      let str = pre ? l.s : l.s.replace(/\s+/g, ' ');
      if (!str.trim()) continue;
      if (tf === 'uppercase') str = str.toUpperCase();
      ops.push({ t: 'text', chars: l.chars, x: l.left - S.left, y: base + (l.top - lines[0].top) - S.top, w: l.right - l.left, str, family: cs.fontFamily, size: parseFloat(cs.fontSize), weight: cs.fontWeight, ls: cs.letterSpacing === 'normal' ? 0 : parseFloat(cs.letterSpacing), fill: cs.color, underline: /underline/.test(cs.textDecorationLine) ? cs.textDecorationColor : null, tnum: /tabular/.test(cs.fontVariantNumeric) });
    }
  };
  const walk = (el) => {
    for (const n of Array.from(el.childNodes)) {
      if (n.nodeType === 3) { text(n); continue; }
      if (n.nodeType !== 1) continue;
      if (n.dataset.svg === 'skip') continue;
      const cs = getComputedStyle(n);
      if (n.tagName.toLowerCase() === 'svg') { svg(n); continue; }
      if (n.dataset.svg === 'ring') { const p = n.parentElement.getBoundingClientRect(); ops.push({ t: 'ring', cx: p.left + p.width / 2 - S.left, cy: p.top + p.height / 2 - S.top, r: n.offsetWidth / 2, color: cs.borderTopColor }); continue; }
      if (n.dataset.svg !== 'stage') box(n, cs);
      walk(n);
    }
  };
  walk(stage);
  document.body.innerHTML = '<pre id="ops">' + JSON.stringify(ops).replace(/&/g, '&amp;').replace(/</g, '&lt;') + '</pre>';
}
