#!/usr/bin/env python3
"""Generate Rota's README diagrams (diagram-design skill grammar, Rota brand tokens).
Emits light + dark HTML into docs/diagrams/. PNGs are rendered separately."""
import pathlib

OUT = pathlib.Path(__file__).resolve().parent

TOKENS = {
    "light": dict(paper="#ffffff", paper2="#f5f5f5", ink="#111111", muted="#5c5c5c", soft="#8a8a8a",
                  rule="rgba(17,17,17,0.12)", zone_fill="rgba(17,17,17,0.02)", zone_stroke="rgba(17,17,17,0.10)",
                  zone_label="rgba(17,17,17,0.40)", accent="#0b2555", accent_tint="rgba(11,37,85,0.08)",
                  accent_soft="rgba(11,37,85,0.50)", link="#2a78d6", store_fill="rgba(17,17,17,0.05)",
                  ext_fill="rgba(17,17,17,0.03)", ext_stroke="rgba(17,17,17,0.30)", ext_tag="rgba(17,17,17,0.22)",
                  input_fill="rgba(92,92,92,0.10)", input_stroke="#8a8a8a", backend_fill="#ffffff",
                  tag_stroke="rgba(17,17,17,0.40)", num="rgba(17,17,17,0.06)", num_accent="rgba(11,37,85,0.10)"),
    "dark": dict(paper="#0d1117", paper2="#161b22", ink="#f0f0f0", muted="#b3b3b3", soft="#8b8b8b",
                 rule="rgba(240,240,240,0.12)", zone_fill="rgba(240,240,240,0.03)", zone_stroke="rgba(240,240,240,0.10)",
                 zone_label="rgba(240,240,240,0.35)", accent="#c7dff7", accent_tint="rgba(199,223,247,0.10)",
                 accent_soft="rgba(199,223,247,0.50)", link="#3987e5", store_fill="rgba(240,240,240,0.05)",
                 ext_fill="rgba(240,240,240,0.03)", ext_stroke="rgba(240,240,240,0.30)", ext_tag="rgba(240,240,240,0.22)",
                 input_fill="rgba(179,179,179,0.10)", input_stroke="#8b8b8b", backend_fill="#1c2129",
                 tag_stroke="rgba(240,240,240,0.40)", num="rgba(240,240,240,0.06)", num_accent="rgba(199,223,247,0.10)"),
}

SANS = "'Geist', sans-serif"
MONO = "'Geist Mono', monospace"

def page(title, eyebrow, h1, svg, t):
    return f"""<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{title}</title>
  <link href="https://fonts.googleapis.com/css2?family=Instrument+Serif:ital@0;1&family=Geist:wght@400;500;600&family=Geist+Mono:wght@400;500;600&display=swap" rel="stylesheet">
  <style>
    *, *::before, *::after {{ box-sizing: border-box; margin: 0; padding: 0; }}
    :root {{ --color-paper: {t['paper']}; --color-ink: {t['ink']}; --color-muted: {t['muted']}; --color-accent: {t['accent']};
      --font-sans: 'Geist', system-ui, sans-serif; --font-serif: 'Instrument Serif', serif; --font-mono: 'Geist Mono', ui-monospace, monospace; }}
    body {{ font-family: var(--font-sans); background: var(--color-paper); color: var(--color-ink); min-height: 100vh;
      display: flex; align-items: center; justify-content: center; padding: 3rem 2rem; }}
    .frame {{ max-width: 1200px; width: 100%; }}
    .eyebrow {{ font-family: var(--font-mono); font-size: 0.66rem; font-weight: 500; letter-spacing: 0.18em; text-transform: uppercase; color: var(--color-muted); margin-bottom: 0.5rem; }}
    h1 {{ font-family: var(--font-serif); font-size: clamp(1.5rem, 2.4vw + 0.75rem, 2rem); font-weight: 400; letter-spacing: -0.02em; line-height: 1.15; color: var(--color-ink); margin-bottom: 1.5rem; }}
    svg {{ width: 100%; min-width: 760px; display: block; }}
  </style>
</head>
<body>
  <div class="frame">
    <p class="eyebrow">{eyebrow}</p>
    <h1>{h1}</h1>
{svg}
  </div>
</body>
</html>
"""

def defs(t):
    return f"""        <defs>
          <marker id="arrow" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto"><polygon points="0 0, 8 3, 0 6" fill="{t['muted']}"/></marker>
          <marker id="arrow-accent" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto"><polygon points="0 0, 8 3, 0 6" fill="{t['accent']}"/></marker>
          <marker id="arrow-link" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto"><polygon points="0 0, 8 3, 0 6" fill="{t['link']}"/></marker>
        </defs>"""

def label(t, x, y, w, text, color=None, ls="0.08em"):
    color = color or t["muted"]
    return (f'        <rect x="{x}" y="{y}" width="{w}" height="12" rx="2" fill="{t["paper"]}"/>\n'
            f'        <text x="{x + w/2:g}" y="{y + 9}" fill="{color}" font-size="8" font-family="{MONO}" text-anchor="middle" letter-spacing="{ls}">{text}</text>\n')

def node(t, x, y, w, h, kind, tag, name, sub, num=None):
    fills = {
        "focal": (t["accent_tint"], t["accent"], t["accent_soft"], t["accent"]),
        "backend": (t["backend_fill"], t["ink"], t["tag_stroke"], t["ink"]),
        "store": (t["store_fill"], t["muted"], t["tag_stroke"], t["muted"]),
        "ext": (t["ext_fill"], t["ext_stroke"], t["ext_tag"], t["soft"]),
        "input": (t["input_fill"], t["input_stroke"], t["ext_tag"], t["soft"]),
    }
    fill, stroke, tag_stroke, tag_ink = fills[kind]
    cx, cy = x + w / 2, y + h / 2
    tw = 8 + 6 * len(tag)
    s = (f'        <rect x="{x}" y="{y}" width="{w}" height="{h}" rx="6" fill="{t["paper"]}"/>\n'
         f'        <rect x="{x}" y="{y}" width="{w}" height="{h}" rx="6" fill="{fill}" stroke="{stroke}" stroke-width="1"/>\n'
         f'        <rect x="{x+8}" y="{y+8}" width="{tw}" height="12" rx="2" fill="transparent" stroke="{tag_stroke}" stroke-width="0.8"/>\n'
         f'        <text x="{x+8+tw/2:g}" y="{y+17}" fill="{tag_ink}" font-size="7" font-family="{MONO}" text-anchor="middle" letter-spacing="0.08em">{tag}</text>\n')
    if num:
        s += f'        <text x="{x+w-8}" y="{y+h-4}" fill="{t["num_accent"] if kind=="focal" else t["num"]}" font-size="30" font-weight="600" font-family="{MONO}" text-anchor="end">{num}</text>\n'
    s += (f'        <text x="{cx:g}" y="{cy+4:g}" fill="{t["ink"]}" font-size="12" font-weight="600" font-family="{SANS}" text-anchor="middle">{name}</text>\n'
          f'        <text x="{cx:g}" y="{cy+20:g}" fill="{t["muted"]}" font-size="9" font-family="{MONO}" text-anchor="middle">{sub}</text>\n')
    return s

def legend_item(t, x, y, swatch, text):
    return swatch + f'        <text x="{x+20}" y="{y+8}" fill="{t["muted"]}" font-size="8.5" font-family="{SANS}">{text}</text>\n'

def swatch_rect(t, x, y, fill, stroke, dash=""):
    return f'        <rect x="{x}" y="{y}" width="14" height="10" rx="2" fill="{fill}" stroke="{stroke}" stroke-width="1"{dash}/>\n'

def swatch_line(t, x, y, color, marker, width="1.2", dash=""):
    return f'        <line x1="{x}" y1="{y+5}" x2="{x+28}" y2="{y+5}" stroke="{color}" stroke-width="{width}"{dash} marker-end="url(#{marker})"/>\n'

# ────────────────────────────────────────────────────────────── architecture
def architecture(variant):
    t = TOKENS[variant]
    slug = "architecture" + ("-dark" if variant == "dark" else "")
    s = f'''    <svg viewBox="0 0 1000 540" xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="{slug}-title {slug}-desc">
      <title id="{slug}-title">Rota in production</title>
      <desc id="{slug}-desc">Architecture diagram: an admin browser reaches Caddy, which serves the dashboard and forwards API and WebSocket calls to the Go core; the core stores state in TimescaleDB, and the same core accepts client traffic on port 8000 and rotates it through the upstream proxy pool.</desc>
{defs(t)}

        <rect width="100%" height="100%" fill="{t['paper']}"/>

        <!-- Zone: docker compose -->
        <rect x="216" y="72" width="696" height="256" rx="8" fill="{t['zone_fill']}" stroke="{t['zone_stroke']}" stroke-width="0.8"/>
        <rect x="232" y="76" width="112" height="12" rx="2" fill="{t['paper']}"/>
        <text x="288" y="85" fill="{t['zone_label']}" font-size="7" font-family="{MONO}" text-anchor="middle" letter-spacing="0.14em">DOCKER COMPOSE</text>

        <!-- Arrows (behind nodes) -->
        <line x1="168" y1="264" x2="240" y2="264" stroke="{t['link']}" stroke-width="1.2" marker-end="url(#arrow-link)"/>
        <path d="M 384,252 H 412 Q 420,252 420,244 V 152 Q 420,144 428,144 H 456" fill="none" stroke="{t['muted']}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="384" y1="276" x2="456" y2="276" stroke="{t['muted']}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="632" y1="268" x2="728" y2="268" stroke="{t['muted']}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <path d="M 168,424 H 504 Q 512,424 512,416 V 304" fill="none" stroke="{t['muted']}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <path d="M 568,304 V 416 Q 568,424 576,424 H 728" fill="none" stroke="{t['accent']}" stroke-width="1.4" marker-end="url(#arrow-accent)"/>

        <!-- Arrow labels -->
'''
    s += label(t, 184, 244, 40, "HTTPS", t["link"])
    s += label(t, 364, 190, 48, "PAGES")
    s += label(t, 392, 256, 56, "/API · /WS")
    s += label(t, 664, 248, 32, "SQL")
    s += label(t, 300, 404, 112, ":8000 · USER:PASS")
    s += label(t, 624, 404, 48, "ROTATE", t["accent"])
    s += "\n        <!-- Nodes -->\n"
    s += node(t, 40, 232, 128, 64, "input", "EXT", "Admin browser", "dashboard · :80 / :443")
    s += node(t, 240, 232, 144, 64, "ext", "EDGE", "Caddy", "one origin · auto-HTTPS")
    s += node(t, 456, 112, 176, 64, "backend", "UI", "Dashboard", "Next.js · same-origin")
    s += node(t, 456, 232, 176, 72, "focal", "GO", "Rota core", "API :8001 · proxy :8000")
    s += node(t, 728, 232, 160, 72, "store", "DB", "TimescaleDB", "proxies · pools · requests")
    s += node(t, 40, 392, 128, 64, "input", "EXT", "Your clients", "curl · scrapers · bots")
    s += node(t, 728, 392, 160, 64, "ext", "POOL", "Upstream proxies", "HTTP · SOCKS4 · SOCKS5")
    # legend
    ly = 484
    s += f'''
        <!-- Legend -->
        <line x1="40" y1="468" x2="960" y2="468" stroke="{t['rule']}" stroke-width="0.8"/>
'''
    x = 40
    s += legend_item(t, x, ly, swatch_rect(t, x, ly, t["accent_tint"], t["accent"]), "Rota (one Go process)"); x += 168
    s += legend_item(t, x, ly, swatch_rect(t, x, ly, t["backend_fill"], t["ink"]), "Service"); x += 96
    s += legend_item(t, x, ly, swatch_rect(t, x, ly, t["store_fill"], t["muted"]), "Store"); x += 88
    s += legend_item(t, x, ly, swatch_rect(t, x, ly, t["ext_fill"], t["ext_stroke"]), "Edge / external"); x += 136
    s += legend_item(t, x, ly, swatch_rect(t, x, ly, t["input_fill"], t["input_stroke"]), "Users of the system"); x += 160
    s += f'        <line x1="{x}" y1="{ly+5}" x2="{x+28}" y2="{ly+5}" stroke="{t["link"]}" stroke-width="1.2" marker-end="url(#arrow-link)"/>\n        <text x="{x+36}" y="{ly+8}" fill="{t["muted"]}" font-size="8.5" font-family="{SANS}">HTTPS</text>\n'; x += 92
    s += f'        <line x1="{x}" y1="{ly+5}" x2="{x+28}" y2="{ly+5}" stroke="{t["accent"]}" stroke-width="1.4" marker-end="url(#arrow-accent)"/>\n        <text x="{x+36}" y="{ly+8}" fill="{t["muted"]}" font-size="8.5" font-family="{SANS}">Proxied traffic</text>\n'
    s += "      </svg>"
    return page("Rota · Architecture", "Architecture · Rota", "Rota in production", s, t)

# ────────────────────────────────────────────────────────────── routing
def oval(t, x, y, w, h, name, sub=None, focal=False):
    fill = t["accent_tint"] if focal else t["ext_fill"]
    stroke = t["accent"] if focal else t["ext_stroke"]
    cx = x + w / 2
    s = f'        <rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{h//2}" fill="{t["paper"]}"/>\n'
    s += f'        <rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{h//2}" fill="{fill}" stroke="{stroke}" stroke-width="1"/>\n'
    if sub:
        s += f'        <text x="{cx:g}" y="{y+h/2:g}" fill="{t["ink"]}" font-size="12" font-weight="600" font-family="{SANS}" text-anchor="middle">{name}</text>\n'
        s += f'        <text x="{cx:g}" y="{y+h/2+14:g}" fill="{t["muted"]}" font-size="9" font-family="{MONO}" text-anchor="middle">{sub}</text>\n'
    else:
        s += f'        <text x="{cx:g}" y="{y+h/2+4:g}" fill="{t["ink"]}" font-size="12" font-weight="600" font-family="{SANS}" text-anchor="middle">{name}</text>\n'
    return s

def step(t, x, y, w, h, name, sub):
    cx = x + w / 2
    return (f'        <rect x="{x}" y="{y}" width="{w}" height="{h}" rx="6" fill="{t["paper"]}"/>\n'
            f'        <rect x="{x}" y="{y}" width="{w}" height="{h}" rx="6" fill="{t["backend_fill"]}" stroke="{t["ink"]}" stroke-width="1"/>\n'
            f'        <text x="{cx:g}" y="{y+h/2:g}" fill="{t["ink"]}" font-size="12" font-weight="600" font-family="{SANS}" text-anchor="middle">{name}</text>\n'
            f'        <text x="{cx:g}" y="{y+h/2+14:g}" fill="{t["muted"]}" font-size="9" font-family="{MONO}" text-anchor="middle">{sub}</text>\n')

def diamond(t, cx, cy, l1, l2):
    return (f'        <polygon points="{cx},{cy-48} {cx+100},{cy} {cx},{cy+48} {cx-100},{cy}" fill="{t["paper"]}"/>\n'
            f'        <polygon points="{cx},{cy-48} {cx+100},{cy} {cx},{cy+48} {cx-100},{cy}" fill="{t["backend_fill"]}" stroke="{t["ink"]}" stroke-width="1"/>\n'
            f'        <text x="{cx}" y="{cy-2}" fill="{t["ink"]}" font-size="11" font-weight="600" font-family="{SANS}" text-anchor="middle">{l1}</text>\n'
            f'        <text x="{cx}" y="{cy+12}" fill="{t["ink"]}" font-size="11" font-weight="600" font-family="{SANS}" text-anchor="middle">{l2}</text>\n')

def routing(variant):
    t = TOKENS[variant]
    slug = "routing" + ("-dark" if variant == "dark" else "")
    m = t["muted"]
    s = f'''    <svg viewBox="0 0 800 780" xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="{slug}-title {slug}-desc">
      <title id="{slug}-title">How a request is routed</title>
      <desc id="{slug}-desc">Flowchart: a request on port 8000 without proxy credentials uses global rotation; with credentials it goes to the user's main pool, falls back to the next pool when no proxy is alive, is forwarded through a pool proxy, and is retried with a fresh proxy until it succeeds or max retries is reached.</desc>
{defs(t)}

        <rect width="100%" height="100%" fill="{t['paper']}"/>

        <!-- Arrows (behind nodes) -->
        <line x1="300" y1="88" x2="300" y2="120" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="400" y1="168" x2="480" y2="168" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="300" y1="216" x2="300" y2="248" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="300" y1="296" x2="300" y2="328" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="400" y1="376" x2="480" y2="376" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <!-- fallback loops back into the selected pool -->
        <path d="M 560,352 V 280 Q 560,272 552,272 H 380" fill="none" stroke="{m}" stroke-width="1" stroke-dasharray="4,3" marker-end="url(#arrow)"/>
        <line x1="300" y1="424" x2="300" y2="456" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <!-- global rotation joins the forward step from the right -->
        <path d="M 640,168 H 712 Q 720,168 720,176 V 464 Q 720,472 712,472 H 380" fill="none" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="300" y1="504" x2="300" y2="536" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <line x1="400" y1="584" x2="480" y2="584" stroke="{m}" stroke-width="1.2" marker-end="url(#arrow)"/>
        <!-- retry loops back into forward -->
        <path d="M 560,560 V 496 Q 560,488 552,488 H 380" fill="none" stroke="{m}" stroke-width="1" stroke-dasharray="4,3" marker-end="url(#arrow)"/>
        <line x1="300" y1="632" x2="300" y2="664" stroke="{t['accent']}" stroke-width="1.4" marker-end="url(#arrow-accent)"/>

        <!-- Arrow labels -->
'''
    s += label(t, 420, 148, 40, "NONE", ls="0.12em")
    s += label(t, 284, 224, 32, "USER", ls="0.12em")
    s += label(t, 424, 356, 32, "NO", ls="0.12em")
    s += label(t, 284, 432, 32, "YES", ls="0.12em")
    s += label(t, 572, 306, 64, "RECHECK")
    s += label(t, 656, 148, 56, "ANY PROXY")
    s += label(t, 424, 564, 32, "NO", ls="0.12em")
    s += label(t, 284, 640, 32, "YES", t["accent"], ls="0.12em")
    s += label(t, 572, 518, 96, "≤ MAX RETRIES")
    s += "\n        <!-- Nodes -->\n"
    s += oval(t, 220, 40, 160, 48, "Request on :8000")
    s += diamond(t, 300, 168, "Proxy-Authorization", "header?")
    s += step(t, 480, 144, 160, 48, "Global rotation", "every active proxy")
    s += step(t, 220, 248, 160, 48, "Selected pool", "main pool first")
    s += diamond(t, 300, 376, "Alive proxy", "in this pool?")
    s += step(t, 480, 352, 160, 48, "Next fallback pool", "in priority order")
    s += step(t, 220, 456, 160, 48, "Forward via a proxy", "by the pool's rotation rule")
    s += diamond(t, 300, 584, "Upstream", "answered?")
    s += step(t, 480, 560, 160, 48, "Fresh proxy", "failed one is skipped")
    s += oval(t, 220, 664, 160, 48, "Response to client", None, focal=True)
    ly = 740
    s += f'''
        <!-- Legend -->
        <line x1="40" y1="724" x2="760" y2="724" stroke="{t['rule']}" stroke-width="0.8"/>
'''
    x = 40
    s += f'        <rect x="{x}" y="{ly}" width="24" height="12" rx="6" fill="{t["ext_fill"]}" stroke="{t["ext_stroke"]}" stroke-width="1"/>\n        <text x="{x+32}" y="{ly+10}" fill="{m}" font-size="8.5" font-family="{SANS}">Start / end</text>\n'; x += 128
    s += f'        <rect x="{x}" y="{ly}" width="24" height="12" rx="2" fill="{t["backend_fill"]}" stroke="{t["ink"]}" stroke-width="1"/>\n        <text x="{x+32}" y="{ly+10}" fill="{m}" font-size="8.5" font-family="{SANS}">Step</text>\n'; x += 88
    s += f'        <polygon points="{x+12},{ly} {x+24},{ly+6} {x+12},{ly+12} {x},{ly+6}" fill="{t["backend_fill"]}" stroke="{t["ink"]}" stroke-width="1"/>\n        <text x="{x+32}" y="{ly+10}" fill="{m}" font-size="8.5" font-family="{SANS}">Decision</text>\n'; x += 104
    s += f'        <line x1="{x}" y1="{ly+6}" x2="{x+28}" y2="{ly+6}" stroke="{m}" stroke-width="1" stroke-dasharray="4,3" marker-end="url(#arrow)"/>\n        <text x="{x+36}" y="{ly+10}" fill="{m}" font-size="8.5" font-family="{SANS}">Loop</text>\n'; x += 96
    s += f'        <line x1="{x}" y1="{ly+6}" x2="{x+28}" y2="{ly+6}" stroke="{t["accent"]}" stroke-width="1.4" marker-end="url(#arrow-accent)"/>\n        <text x="{x+36}" y="{ly+10}" fill="{m}" font-size="8.5" font-family="{SANS}">Happy path</text>\n'
    s += "      </svg>"
    return page("Rota · Request routing", "Flowchart · Rota", "How a request is routed", s, t)

OUT.mkdir(parents=True, exist_ok=True)
for variant in ("light", "dark"):
    suffix = "-dark" if variant == "dark" else ""
    (OUT / f"architecture{suffix}.html").write_text(architecture(variant))
    (OUT / f"routing{suffix}.html").write_text(routing(variant))
print("written", sorted(p.name for p in OUT.iterdir()))
