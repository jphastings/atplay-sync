<!-- web/src/lib/HeroList.svelte -->
<script lang="ts">
  import { state } from '../state.svelte'
  import HeroCard from './HeroCard.svelte'
</script>

<div class="hero-list" id="hero-list">
  {#if state.liveStatuses === undefined}
    <section class="hero hero--loading" aria-live="polite">
      <div class="hero-cover hero-cover--loading"></div>
      <div class="hero-body">
        <p class="hero-eyebrow"><span class="live-dot"></span> Checking your PDS…</p>
      </div>
    </section>
  {:else if state.liveStatuses === 'error'}
    <section class="hero hero--error" aria-live="polite">Couldn't reach your PDS to check status.</section>
  {:else if state.liveStatuses.length === 0}
    <section class="hero hero--empty" aria-live="polite">
      <div class="hero-body">
        <p class="hero-eyebrow"><span class="live-dot"></span> Idle</p>
        <p class="hero-meta">Not currently playing a game.</p>
      </div>
    </section>
  {:else}
    {#each state.liveStatuses as status (status.pageURL)}
      <HeroCard {status} />
    {/each}
  {/if}
</div>
