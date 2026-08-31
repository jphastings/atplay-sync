<!-- web/src/lib/SourceRow.svelte -->
<script lang="ts">
  import type { Me } from '../api'
  import { isConnected as isConnectedFn, isEnabled as isEnabledFn, type Source } from '../state.svelte'
  import type { SourceOutcome } from '../synclive'

  // Streamline "Simple Icons" Steam mark (https://streamlinehq.com), inlined
  // and recolored to currentColor. Decorative — the adjacent "Steam" text
  // already labels it, so it's hidden from assistive tech.
  const STEAM_ICON_PATH = "M11.979 0C5.678 0 0.511 4.86 0.022 11.037l6.432 2.658c0.545 -0.371 1.203 -0.59 1.912 -0.59 0.063 0 0.125 0.004 0.188 0.006l2.861 -4.142V8.91c0 -2.495 2.028 -4.524 4.524 -4.524 2.494 0 4.524 2.031 4.524 4.527s-2.03 4.525 -4.524 4.525h-0.105l-4.076 2.911c0 0.052 0.004 0.105 0.004 0.159 0 1.875 -1.515 3.396 -3.39 3.396 -1.635 0 -3.016 -1.173 -3.331 -2.727L0.436 15.27C1.862 20.307 6.486 24 11.979 24c6.627 0 11.999 -5.373 11.999 -12S18.605 0 11.979 0zM7.54 18.21l-1.473 -0.61c0.262 0.543 0.714 0.999 1.314 1.25 1.297 0.539 2.793 -0.076 3.332 -1.375 0.263 -0.63 0.264 -1.319 0.005 -1.949s-0.75 -1.121 -1.377 -1.383c-0.624 -0.26 -1.29 -0.249 -1.878 -0.03l1.523 0.63c0.956 0.4 1.409 1.5 1.009 2.455 -0.397 0.957 -1.497 1.41 -2.454 1.012H7.54zm11.415 -9.303c0 -1.662 -1.353 -3.015 -3.015 -3.015 -1.665 0 -3.015 1.353 -3.015 3.015 0 1.665 1.35 3.015 3.015 3.015 1.663 0 3.015 -1.35 3.015 -3.015zm-5.273 -0.005c0 -1.252 1.013 -2.266 2.265 -2.266 1.249 0 2.266 1.014 2.266 2.266 0 1.251 -1.017 2.265 -2.266 2.265 -1.253 0 -2.265 -1.014 -2.265 -2.265z"

  // Streamline "Simple Icons" Discord mark (https://streamlinehq.com), inlined
  // and recolored to currentColor. Decorative — the adjacent "Discord" text
  // already labels it, so it's hidden from assistive tech.
  const DISCORD_ICON_PATH = "M20.317 4.37a19.791 19.791 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028 14.09 14.09 0 0 0 1.226-1.994.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.955 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418Z"

  let { source, me, onToggle, outcome }: {
    source: Source
    me: Me
    onToggle: (source: Source, enabled: boolean) => Promise<boolean>
    outcome?: SourceOutcome
  } = $props()

  let connected = $derived(isConnectedFn(me, source))
  let label = $derived(source === 'steam' ? 'Steam' : 'Discord')
  let enabled = $state(isEnabledFn(me, source))
  let unknownPopoverId = $derived(`${source}-unknown-popover`)

  // Re-sync if `me` changes out from under us (e.g. a fresh fetch after a
  // reorder round-trips) without clobbering an in-flight optimistic toggle.
  $effect(() => {
    enabled = isEnabledFn(me, source)
  })

  async function handleChange(e: Event) {
    const next = (e.target as HTMLInputElement).checked
    enabled = next // optimistic
    const ok = await onToggle(source, next)
    if (!ok) enabled = !next // revert on failure — don't leave the UI claiming a state that didn't take
  }
</script>

<label
  class="toggle-row"
  data-connected={connected}
  data-source={source}
  onmousedowncapture={(e) => { if ((e.target as HTMLElement).closest('a, .toggle, .sync-dot')) e.stopPropagation() }}
  ontouchstartcapture={(e) => { if ((e.target as HTMLElement).closest('a, .toggle, .sync-dot')) e.stopPropagation() }}
>
  <span class="toggle-label">
    <span class="toggle-label-title">
      {#if source === 'steam'}
        <svg class="steam-icon" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d={STEAM_ICON_PATH} fill="currentColor"></path></svg>
      {:else}
        <svg class="discord-icon" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d={DISCORD_ICON_PATH} fill="currentColor"></path></svg>
      {/if}
      {label}
      {#if outcome?.status === 'unknown'}
        <button
          type="button"
          class="sync-dot"
          data-status="unknown"
          popovertarget={unknownPopoverId}
          aria-label={`Unrecognized activity reported: ${outcome.gameName || 'unknown'}`}
        ></button>
        <div id={unknownPopoverId} popover class="sync-popover">{outcome.gameName || 'Unrecognized activity'}</div>
      {:else if outcome}
        <span class="sync-dot" data-status={outcome.status} title={outcome.gameName}></span>
      {/if}
    </span>
    <span class="toggle-label-sub">
      {#if connected}
        {#if source === 'steam'}
          Sync data from <a href={`https://steamcommunity.com/profiles/${encodeURIComponent(me.steamSubject!)}`} target="_blank" rel="noopener noreferrer">{me.steamDisplayName ?? me.steamSubject}</a> on Steam
        {:else}
          Sync data from <a href={`https://discord.com/users/${encodeURIComponent(me.discordSubject!)}`} target="_blank" rel="noopener noreferrer">{me.discordDisplayName ?? me.discordSubject}</a> on Discord
        {/if}
      {:else if source === 'discord'}
        You must <a href="https://keytrace.dev/add/discord" target="_blank" rel="noopener noreferrer">link your Discord account</a> and <a href={me.discordInviteUrl} target="_blank" rel="noopener noreferrer">join our tracking server</a> before you can sync it
      {:else}
        You must <a href={`https://keytrace.dev/add/${source}`} target="_blank" rel="noopener noreferrer">link your {label} account</a> before you can sync it
      {/if}
    </span>
  </span>
  <span class="toggle">
    <input type="checkbox" checked={enabled} disabled={!connected} onchange={handleChange} />
    <span class="toggle-track"></span>
    <span class="toggle-thumb"></span>
  </span>
</label>
