# Product

## Register

product

## Users

People with an atproto account who want their PDS to carry a live "now
playing" record while they game. They arrive to sign in with atproto OAuth,
verify their Steam identity via a keytrace claim, and flip syncing on or off.
Scale is real but modest: this instance serves up to a few hundred people,
and the project is built to be self-hosted by anyone in the wider atproto
community, not just its author.

## Product Purpose

A small control panel for the `games.atmosphere.status`
lexicon: it keeps a live "now playing" record on the user's own PDS while
they play, and gives them the one thing that record can't show on its own —
clear, immediate control over whether it's happening at all. Success is a
user who can tell at a glance whether they're currently broadcasting, and
who trusts that flipping the toggle actually does something right away.

## Brand Personality

Playful and game-y, not corporate. Status chips over stat cards, a bit of
arcade energy, feels good to pop open and check. Unfussy rather than
polished-for-its-own-sake — this is a community tool, not an enterprise
product, even at a few-hundred-user scale.

## Anti-references

No specific site to avoid — the one hard rule is that it shouldn't read as
generic or AI-generated. The shared design laws' standing bans (hero-metric
template, identical card grids, gradient text, side-stripe borders) apply by
default since nothing here overrides them.

## Design Principles

- **Consent is the headline, not a footnote.** The lexicon itself warns
  against publishing "covert play" without consent — the UI's central job is
  making "am I visible right now" unmissable, not burying it in a settings
  row.
- **Show, don't just toggle.** The actual live status (what game, since
  when) is the payoff for trusting a background sync service — it should be
  the most prominent thing on the page, not a caption under a switch.
- **Always current, never stale-looking.** The site reads the PDS live on
  every load, no local caching — the UI should look and feel like it's
  telling the truth right now, not a cached snapshot.
- **Personal warmth at community scale.** Built for hundreds of users across
  many self-hosted instances, but it should still feel like someone's fun
  side project, not software a company shipped.
- **Playful without clutter.** Game-y personality — arcade energy, status
  chips — stays legible and fast rather than turning into a busy dashboard.

## Accessibility & Inclusion

Solid baseline: full keyboard navigation, sufficient contrast, no
motion-only cues. No additional stated requirements.
