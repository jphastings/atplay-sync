<!-- web/src/lib/SourcesList.svelte -->
<script lang="ts">
  import { dndzone, type DndEvent } from 'svelte-dnd-action'
  import { flip } from 'svelte/animate'
  import { state as appState, resolveSourceOrder, isConnected, isEnabled, toggleSource, reorderSources, type Source } from '../state.svelte'
  import SourceRow from './SourceRow.svelte'

  interface Item { id: Source }

  let connectedItems = $state<Item[]>([])
  let disconnectedSources = $derived(
    appState.me ? resolveSourceOrder(appState.me).filter((s) => !isConnected(appState.me!, s)) : [],
  )

  // Re-derives the draggable list from server-confirmed state whenever `me`
  // changes (initial load, a toggle, a reorder round-trip) — svelte-dnd-action
  // needs a locally-reassignable array for live drag feedback, so this can't
  // just be a plain $derived.
  $effect(() => {
    const me = appState.me
    if (!me) {
      connectedItems = []
      return
    }
    connectedItems = resolveSourceOrder(me)
      .filter((s) => isConnected(me, s))
      .sort((a, b) => Number(isEnabled(me, b)) - Number(isEnabled(me, a)))
      .map((id) => ({ id }))
  })

  function handleDndConsider(e: CustomEvent<DndEvent<Item>>) {
    connectedItems = e.detail.items
  }

  function handleDndFinalize(e: CustomEvent<DndEvent<Item>>) {
    connectedItems = e.detail.items
    reorderSources([...connectedItems.map((i) => i.id), ...disconnectedSources])
  }
</script>

<section class="consent-zone">
  <div id="sources" use:dndzone={{ items: connectedItems, flipDurationMs: 200 }} onconsider={handleDndConsider} onfinalize={handleDndFinalize}>
    {#each connectedItems as item (item.id)}
      <div animate:flip={{ duration: 200 }}>
        {#if appState.me}
          <SourceRow source={item.id} me={appState.me} onToggle={toggleSource} />
        {/if}
      </div>
    {/each}
  </div>
  {#each disconnectedSources as source (source)}
    {#if appState.me}
      <SourceRow {source} me={appState.me} onToggle={toggleSource} />
    {/if}
  {/each}
</section>
