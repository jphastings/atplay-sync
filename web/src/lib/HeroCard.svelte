<!-- web/src/lib/HeroCard.svelte -->
<script lang="ts">
  import type { LiveStatus } from '../atproto'

  let { status }: { status: LiveStatus } = $props()

  function timeAgo(iso: string): string {
    const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
    const units: [Intl.RelativeTimeFormatUnit, number][] = [
      ['day', 86400], ['hour', 3600], ['minute', 60],
    ]
    for (const [unit, secs] of units) {
      const value = Math.floor(seconds / secs)
      if (value >= 1) return new Intl.RelativeTimeFormat('en', { style: 'long' }).format(-value, unit)
    }
    return 'just now'
  }
</script>

<section class="hero" aria-live="polite">
  {#if status.coverURL}
    <img
      class="hero-cover"
      src={status.coverURL}
      alt={`${status.title} cover art`}
      loading="lazy"
      onerror={(e) => (e.currentTarget as HTMLImageElement).remove()}
    />
  {/if}
  <div class="hero-body">
    <p class="hero-eyebrow"><span class="live-dot"></span> Live</p>
    <h2 class="hero-title">{status.title}</h2>
    <p class="hero-meta">since {timeAgo(status.createdAt)}</p>
  </div>
</section>
