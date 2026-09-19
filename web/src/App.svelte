<!-- web/src/App.svelte -->
<script lang="ts">
  import { appState, loadMe, loadLiveStatuses, applySourceOutcomes } from './state.svelte'
  import { watchOwnStatus } from './jetstream'
  import { watchSyncLive } from './synclive'
  import SignIn from './lib/SignIn.svelte'
  import SignedIn from './lib/SignedIn.svelte'

  $effect(() => {
    loadMe()
  })

  $effect(() => {
    if (!appState.me) return
    loadLiveStatuses()
    return watchOwnStatus(appState.me.did, () => loadLiveStatuses())
  })

  $effect(() => {
    if (!appState.me) return
    return watchSyncLive(applySourceOutcomes)
  })
</script>

{#if appState.meFailed}
  <div class="screen">
    <div class="marquee">
      <section class="hero hero--error" aria-live="polite">Couldn't reach At Play Sync.</section>
      <button class="btn btn-ghost" onclick={() => { appState.meFailed = false; loadMe() }}>Retry</button>
    </div>
  </div>
{:else if appState.me === null}
  <SignIn />
{:else if appState.me}
  <SignedIn />
{/if}
