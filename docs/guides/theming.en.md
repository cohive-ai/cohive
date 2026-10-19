# Custom themes

[中文](theming.md)

The look of the board (colors, corner radius, shadows) comes entirely from a set of CSS variables. Their defaults are in [`web/src/theme.css`](../../web/src/theme.css), and no component hard-codes a color. A server admin can add a **custom theme file** that overrides some of these variables, for example to swap the brand yellow for a company blue.

- Without a custom theme file the board is exactly the default.
- A custom theme can only change variables. It cannot change layout or font sizes or add any other styles.
- Everyone can also choose light, dark or same as system under Profile → Appearance. That is separate from the custom theme; see the end of this page.

## Turning it on

Only someone who can change the server's configuration or data directory can add a theme file; there is no upload in the web UI. Use one of two ways:

1. Put it in the data directory as `theme.css` (`/data/theme.css` in the data volume with Docker). If the file exists it is loaded; otherwise the default theme is used.
2. Point the `COHIVE_THEME_FILE` environment variable at it. If that file cannot be read, the server **refuses to start**, so you never think a theme is live when it is not.

**Restart the server** after changing the theme file. The startup log says which theme file was loaded and how many variables it sets.

Check the file before going live; see "Checking" below:

```bash
cohive-server theme check /path/to/theme.css
```

## Syntax

```css
/* Light theme, and the base for variables the dark block does not set */
:root {
  --accent-fill: #1f6feb;
  --accent-ink: #ffffff;
}

/* Dark theme: write it once; it applies both when following the system and when dark is picked by hand */
:root[data-theme="dark"] {
  --accent-fill: #4c8dff;
  --accent-ink: #0b0b0c;
}
```

Rules:

- Only two kinds of block are allowed: `:root` (light; `[data-theme="light"]` also works) and `:root[data-theme="dark"]` (dark; `[data-theme="dark"]` also works).
- A block may only contain CSS variable declarations, `--name: value;`, and the name must be one of the variables below.
- Variables the default dark theme does not set separately, such as radius and shadows, go in `:root` and apply to both themes.
- Values are checked by type:
  - color: `#rgb` or `#rrggbb`;
  - color that may be translucent: also `#rrggbbaa`, `rgb()` and `rgba()`;
  - length: `0` or a number with `px`, `rem` or `em`;
  - shadow: `none`, or up to two groups of "2 to 4 lengths plus one color", where the color is hex or `var(--a-color-variable)`.
- Comments `/* … */` are fine; they are dropped when loading.
- At most 64 KB.

**Always rejected**: `url()`, `image-set()`, `@import`, `@media` and any other at-rule, `expression()`, `attr()`, `env()`, `javascript:`, quotes, backslash escapes, `!important`, angle brackets, any other selector, ordinary properties (such as `color: red`) and nested blocks. A single occurrence means **the whole file is not loaded**: the board keeps the default theme and the startup log says so.

Misspelled or unknown variables are ignored, and the check command lists them.

**How it stays safe**: the server never sends the file to browsers as is. It parses it declaration by declaration, keeps only valid values of known variables, and generates a fresh stylesheet. That stylesheet is served at `/theme.css` and loads after the default styles.

## Variables

[`web/theme-spec.json`](../../web/theme-spec.json) is the authoritative list. Both the web tests and the check command read it.

| Variable | Used for | Type | Light default | Dark default |
|---|---|---|---|---|
| `--bg` | Page background | color | `#f5f5f5` | `#0b0b0c` |
| `--surface` | Cards, panels, header, inputs | color | `#ffffff` | `#141416` |
| `--sunken` | Board columns, table headers, neutral tags | color | `#ebebeb` | `#070708` |
| `--ink` | Body text and headings | color | `#0b0b0c` | `#f5f5f5` |
| `--muted` | Secondary text | color | `#555555` | `#9c9ca3` |
| `--line` | Dividers and card borders | color | `#dcdcdc` | `#2b2b30` |
| `--line-strong` | Borders of inputs and secondary buttons | color | `#8a8a8a` | `#6a6a72` |
| `--accent` | Links, current items, focus outline, selected borders | color | `#0b0b0c` | `#f5f5f5` |
| `--accent-soft` | Background of selected cards and notices | color | `#fff5cc` | `#2a2410` |
| `--accent-fill` | Primary button, current-tab underline, focus ring | color | `#ffcc00` | `#ffcc00` |
| `--accent-ink` | Text on accent-fill and on wait-mark | color | `#0b0b0c` | `#0b0b0c` |
| `--wait` | "Your turn" text | color | `#0b0b0c` | `#ffcc00` |
| `--wait-soft` | Highlighter behind "waiting for you", warning tags | color | `#ffe680` | `#3a3000` |
| `--wait-mark` | "Your turn" bar, ready-for-verification tag, handoff flash | color | `#ffcc00` | `#ffcc00` |
| `--teal` | Finished well: closed, valid, online, linked | color | `#0e7436` | `#4cc47f` |
| `--teal-soft` | Background of success notices and tags | color | `#e1f3e7` | `#12301e` |
| `--danger` | Errors, declined, deactivated, destructive buttons | color | `#c2261a` | `#ff6b5c` |
| `--on-status` | Text on solid teal and danger | color | `#ffffff` | `#0b0b0c` |
| `--tool-claude` | Claude Code icon | color | `#d97757` | `#d97757` |
| `--tool-codex` | Codex icon | color | `#000000` | `#ffffff` |
| `--tool-other` | Icon of any other coding tool | color | `#8a8a8a` | `#8a8a92` |
| `--brand-yellow` | The agent cell in the logo (same in both themes) | color | `#ffcc00` | same as light |
| `--backdrop` | Dimmed page behind dialogs | color (may be translucent) | `rgb(0 0 0 / 0.4)` | `rgb(0 0 0 / 0.6)` |
| `--radius-control` | Corner radius of buttons, inputs, tags, cards | length | `2px` | same as light |
| `--radius-panel` | Corner radius of panels and lists | length | `4px` | same as light |
| `--hard-shadow` | Primary button and card hover shadow | shadow | `2px 2px 0 var(--ink)` | same as light |
| `--focus-ring` | Ring around a focused input | shadow | `0 0 0 3px var(--accent-fill)` | same as light |

**Text, at least 4.5:1** (foreground / background):

- `--ink` / `--bg`
- `--ink` / `--surface`
- `--ink` / `--sunken`
- `--muted` / `--bg`
- `--muted` / `--surface`
- `--muted` / `--sunken`
- `--accent` / `--bg`
- `--accent` / `--surface`
- `--accent` / `--sunken`
- `--teal` / `--bg`
- `--teal` / `--surface`
- `--teal` / `--sunken`
- `--wait` / `--bg`
- `--wait` / `--surface`
- `--wait` / `--sunken`
- `--danger` / `--bg`
- `--danger` / `--surface`
- `--danger` / `--sunken`
- `--accent-ink` / `--wait-mark` (ready-for-verification and reopened tags)
- `--surface` / `--ink` (accepted, fixing and admin tags)
- `--on-status` / `--teal` (closed and valid tags)
- `--on-status` / `--danger` (declined and deactivated tags, destructive button)
- `--wait` / `--wait-soft` ("waiting for you" highlighter)
- `--teal` / `--teal-soft`
- `--ink` / `--teal-soft`
- `--ink` / `--wait-soft`
- `--ink` / `--accent-soft`
- `--accent` / `--accent-soft`
- `--accent-ink` / `--accent-fill` (primary button)

**Graphics, at least 3:1** (foreground / background):

- `--line-strong` / `--surface` (input and secondary button borders)
- `--tool-claude` / `--surface` (Claude Code icon on a card)
- `--tool-codex` / `--surface` (Codex icon on a card)
- `--tool-other` / `--surface` (other tool icon on a card)

## Checking

```bash
cohive-server theme check /path/to/theme.css
```

With Docker:

```bash
docker compose exec cohive cohive-server theme check /data/theme.css
```

It uses the same rules as the web UI's contrast tests (`web/theme-spec.json`) and reports:

- **Disallowed parts**: line, variable and reason. With any of them the server will not load the file.
- **Unknown variables**: declarations that will be ignored, usually typos.
- **Contrast**: the actual ratio of every pair above, in both the light and the dark theme, after your file is applied on top of the default theme. Pairs below the minimum are marked `FAIL`.

Without a file it checks the built-in default theme.

Exit codes for scripts:

| Exit code | Meaning |
|---|---|
| 0 | All passed |
| 1 | Disallowed parts, unknown variables or contrast below the minimum |
| 2 | Wrong usage, or the file cannot be read |

A theme with contrast problems is still loaded, and the startup log warns about it. Going live is the admin's call, but please fix it until everything passes.

## Full example

[`examples/themes/harbor.css`](../../examples/themes/harbor.css) swaps the brand yellow for blue, rounds the corners a little more and drops the hard shadow. Both themes pass the check.

```css
/*
 * Harbor: an example custom theme for Cohive (a blue accent instead of the brand yellow).
 * Only CSS variables of the default theme are allowed; see docs/guides/theming.en.md.
 *
 *   cohive-server theme check examples/themes/harbor.css
 *   COHIVE_THEME_FILE=/path/to/harbor.css cohive-server serve
 */

/* Light theme (also the base for anything the dark block does not set) */
:root {
  --bg: #f3f5f8;
  --surface: #ffffff;
  --sunken: #e6ebf1;
  --ink: #0f1b2d;
  --muted: #4d5b6e;
  --line: #d5dce6;
  --line-strong: #7d8a9c;

  /* Interactive things: links and current items in navy, the primary button in blue */
  --accent: #0b4f8a;
  --accent-soft: #e3eefb;
  --accent-fill: #1f6feb;
  --accent-ink: #ffffff;

  /* "Your turn": a blue bar and tag, navy text on a pale blue highlighter */
  --wait: #0b2e59;
  --wait-soft: #dbeafe;
  --wait-mark: #1f6feb;

  /* Softer corners and no hard shadow; the focus ring keeps its default (accent-fill) */
  --radius-control: 4px;
  --radius-panel: 8px;
  --hard-shadow: none;
}

/* Dark theme */
:root[data-theme="dark"] {
  --bg: #0d1420;
  --surface: #152031;
  --sunken: #0a101a;
  --ink: #e8eef7;
  --muted: #9aa8bb;
  --line: #24324a;
  --line-strong: #5d6f8a;
  --accent: #9cc8ff;
  --accent-soft: #14294a;
  --accent-fill: #4c8dff;
  --accent-ink: #0b0b0c;
  --wait: #9cc8ff;
  --wait-soft: #10294d;
  --wait-mark: #4c8dff;
}
```

## Light and dark for each person

Profile → Appearance offers same as system (the default), light or dark. The choice is saved in the current browser, not on the account, because:

- each computer can have its own (an office monitor by day, a laptop at night);
- it works before signing in, and the sign-in page does not flash the other theme first;
- nothing on the server needs it, so the account data does not change for it.

It only picks the light or the dark block; a custom theme can override both.
