<!-- web/src/lib/SignedIn.svelte -->
<script lang="ts">
  import { state, loadHandle } from '../state.svelte'
  import HeroList from './HeroList.svelte'
  import SourcesList from './SourcesList.svelte'

  $effect(() => {
    if (state.me) loadHandle()
  })

  function handleSignOut() {
    window.location.href = '/logout'
  }
</script>

<div class="panel-screen"><div class="panel">
  <HeroList />
  <SourcesList />
  <footer class="utility-row">
    <span class="did-tag">{state.handle ? `@${state.handle}` : state.me?.did}</span>
    <button class="btn btn-ghost" type="button" onclick={handleSignOut}>Sign out</button>
  </footer>
</div></div>
