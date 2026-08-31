<!-- web/src/App.svelte -->
<script lang="ts">
  import { appState, loadMe, loadLiveStatuses } from './state.svelte'
  import { watchOwnStatus } from './jetstream'
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
</script>

{#if appState.me === null}
  <SignIn />
{:else if appState.me}
  <SignedIn />
{/if}
