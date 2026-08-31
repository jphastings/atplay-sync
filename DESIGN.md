<!-- SEED — re-run $impeccable document once there's code to capture the actual tokens and components. -->

---
name: At Play Sync
description: An arcade-cabinet-drenched control panel for broadcasting your live "now playing" status to atproto.
---

# Design System: At Play Sync

## 1. Overview

**Creative North Star: "The Neon Cabinet"**

This is an arcade cabinet powering on, not a SaaS dashboard loading. The
surface is drenched in a single saturated magenta-violet — the marquee glow,
not a tasteful accent — in the lineage of Discord, itch.io, and the printed
art on an arcade cabinet's side panel. Nothing here is trying to look
trustworthy-and-corporate; it's trying to look like it's fun to boot up.

Motion is choreographed: the page should feel like it powers on rather than
simply appears. A cabinet's marquee flickers to life; the live status readout
should land with the same sense of arrival, not just fade in.

Type pairs a characterful display face for marquee moments (the page title,
the live status headline) against a monospace face for the HUD-style
readout — game title, timestamps, DIDs — the scoreboard-and-terminal texture
underneath the neon.

This explicitly rejects the corporate SaaS dashboard: no restrained neutral
canvas with a single polite accent, no settings-page grayscale. Per
PRODUCT.md, the one hard boundary is not reading as generic or
AI-generated — everything else here leans into commitment rather than away
from it.

**Key Characteristics:**
- Drenched magenta-violet surface — the color IS the interface, not a coat of paint on a neutral one
- Choreographed power-on motion, not passive fades
- Display marquee type + monospace HUD readout
- Arcade cabinet / Discord / itch.io lineage, zero enterprise-dashboard DNA

## 2. Colors

One hue owns the surface: a saturated arcade magenta-violet, close to a
neon marquee tube. Exact values are unresolved until implementation, but the
hue family and doctrine are committed now.

### Primary
- **Arcade Magenta-Violet** (`[to be resolved during implementation]`): the
  drenching hue — backgrounds, primary surfaces, the dominant color of the
  page, not a small accent on a neutral canvas.

### Neutral
- **Near-black, tinted toward magenta-violet** (`[to be resolved]`): text
  and deep-surface color, per the shared design law that neutrals are never
  true `#000`/`#fff` — tint low-chroma toward the primary hue instead.
- **Near-white, tinted toward magenta-violet** (`[to be resolved]`):
  foreground text against the drenched surface.

### Named Rules
**The Drenched Rule.** The surface IS the color. There is no neutral
canvas with the magenta-violet used sparingly as an accent — it's the
background, and everything else is negotiated against it.

**The One-Hue-Off-States Rule.** "Sync disabled" / "not currently playing"
states are conveyed through this same hue at lower lightness or saturation
(a dimmed marquee tube), not by introducing a second competing color.
Resolve the exact treatment at implementation, but don't reach for gray.

## 3. Typography

**Display Font:** `[to be resolved during implementation]` — a characterful
display face with real personality, not a default system sans.
**Body/Label Font:** `[to be resolved during implementation]` — monospace,
carrying the HUD/scoreboard texture.

**Character:** marquee announcement paired with terminal readout — the
display face is for arrival moments (page title, "Currently: [game]"), the
mono face is for data (timestamps, DIDs, game IDs, anything read as a
value rather than a headline).

### Hierarchy
- **Display** (`[weight/size TBD]`): the page title and the live status
  headline — the marquee moment.
- **Headline** (`[weight/size TBD]`): section openers (Steam, sign-in).
- **Title** (`[weight/size TBD]`): labels for individual controls.
- **Body** (`[weight/size TBD]`, mono): status detail — game name, "since
  [time]", claim state.
- **Label** (`[weight/size TBD]`, mono, uppercase): chip and button text.

## 4. Elevation

Layered, not flat — choreographed motion needs surfaces to arrive from
somewhere. Depth reads as stacked glow rather than soft drop-shadows: think
the layered light of a cabinet marquee over the cabinet body, not a
Material-style ambient shadow. Exact shadow vocabulary is unresolved until
there's real layout to hang it on.

## 5. Components

None yet — the frontend is currently unstyled HTML. Re-run
`$impeccable document` once real components exist to extract the actual
button, toggle, and status-chip treatments.

## 6. Do's and Don'ts

### Do:
- **Do** let the primary magenta-violet cover the majority of the surface — commitment is the point, not restraint.
- **Do** give state changes (toggle flips, a status arriving) real choreographed motion, not a passive fade.
- **Do** pair a characterful display face with a monospace data face.
- **Do** convey "off"/disabled states by dimming the one hue, not introducing gray or a second color.

### Don't:
- **Don't** build a corporate SaaS dashboard — no restrained neutral canvas, no polite single accent, no settings-page grayscale (PRODUCT.md anti-reference).
- **Don't** let this read as generic or AI-generated — the one hard line from PRODUCT.md.
- **Don't** use the hero-metric template (big number, small label, gradient accent).
- **Don't** use identical icon+heading card grids.
- **Don't** use gradient text (`background-clip: text` with a gradient).
- **Don't** use side-stripe borders (`border-left`/`border-right` as a colored accent).
- **Don't** reach for glassmorphism as a default decorative treatment.
