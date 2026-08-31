<!-- web/src/App.svelte -->
<script lang="ts">
  import { state, loadMe, loadLiveStatuses } from './state.svelte'
  import { watchOwnStatus } from './jetstream'
  import SignIn from './lib/SignIn.svelte'
  import SignedIn from './lib/SignedIn.svelte'

  $effect(() => {
    loadMe()
  })

  $effect(() => {
    if (!state.me) return
    loadLiveStatuses()
    return watchOwnStatus(state.me.did, () => loadLiveStatuses())
  })
</script>

{#if state.me === null}
  <SignIn />
{:else if state.me}
  <SignedIn />
{/if}
