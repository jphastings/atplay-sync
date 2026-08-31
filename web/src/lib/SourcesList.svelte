<!-- web/src/lib/SourcesList.svelte -->
<script lang="ts">
  import { dndzone, type DndEvent } from 'svelte-dnd-action'
  import { flip } from 'svelte/animate'
  import { appState, resolveSourceOrder, isConnected, toggleSource, reorderSources, type Source } from '../state.svelte'
  import SourceRow from './SourceRow.svelte'

  interface Item { id: Source }

  let connectedItems = $state<Item[]>([])
  let disconnectedSources = $derived(
    appState.me ? resolveSourceOrder(appState.me).filter((s) => !isConnected(appState.me!, s)) : [],
  )

  // Re-derives the draggable list straight from server-confirmed order
  // whenever `me` changes (initial load, a toggle, a reorder round-trip).
  // No local re-sort here: toggleSource already persists an enabled-first
  // order into me.sourceOrder on toggle (state.svelte.ts), and a manual
  // drag's order must survive untouched — re-sorting here would silently
  // revert every drag the instant reorderSources' own write re-triggers
  // this effect.
  $effect(() => {
    const me = appState.me
    if (!me) {
      connectedItems = []
      return
    }
    connectedItems = resolveSourceOrder(me)
      .filter((s) => isConnected(me, s))
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
  {#if connectedItems.length}
    <div
      id="sources"
      use:dndzone={{ items: connectedItems, flipDurationMs: 200, dropTargetStyle: {} }}
      onconsider={handleDndConsider}
      onfinalize={handleDndFinalize}
    >
      {#each connectedItems as item (item.id)}
        <div animate:flip={{ duration: 200 }}>
          {#if appState.me}
            <SourceRow source={item.id} me={appState.me} onToggle={toggleSource} outcome={appState.sourceOutcomes[item.id]} />
          {/if}
        </div>
      {/each}
    </div>
  {/if}
  {#each disconnectedSources as source (source)}
    {#if appState.me}
      <SourceRow {source} me={appState.me} onToggle={toggleSource} outcome={appState.sourceOutcomes[source]} />
    {/if}
  {/each}
</section>
