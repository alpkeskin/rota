# README diagrams

Editorial SVG diagrams for the project README, drawn with the
[diagram-design](https://github.com/cathrynlavery/diagram-design) grammar and
Rota's brand tokens (navy accent, light + dark variants).

| Diagram | Source | Exported to |
|---|---|---|
| Architecture | `architecture.html`, `architecture-dark.html` | `static/architecture.png`, `static/architecture-dark.png` |
| Request routing | `routing.html`, `routing-dark.html` | `static/routing.png`, `static/routing-dark.png` |

## Regenerate

```bash
python3 docs/diagrams/generate.py        # rewrites the four HTML files
# then screenshot each <svg> at 2× (e.g. with Playwright) into static/
```

The HTML files are self-contained (inline SVG, Google Fonts only) and open
directly in a browser. Edit `generate.py`, not the HTML.
