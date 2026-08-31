<!-- web/src/lib/SignedIn.svelte -->
<script lang="ts">
  import { appState, loadHandle } from '../state.svelte'
  import HeroList from './HeroList.svelte'
  import SourcesList from './SourcesList.svelte'

  $effect(() => {
    if (appState.me) loadHandle()
  })

  function handleSignOut() {
    window.location.href = '/logout'
  }
</script>

<div class="panel-screen"><div class="panel">
  <HeroList />
  <SourcesList />
  <footer class="utility-row">
    <span class="did-tag">{appState.handle ? `@${appState.handle}` : appState.me?.did}</span>
    <button class="btn btn-ghost" type="button" onclick={handleSignOut}>Sign out</button>
  </footer>
</div></div>
