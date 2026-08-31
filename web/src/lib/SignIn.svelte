<!-- web/src/lib/SignIn.svelte -->
<script lang="ts">
  function handleSubmit(e: SubmitEvent) {
    e.preventDefault()
    const form = e.currentTarget as HTMLFormElement
    const handle = (new FormData(form).get('handle') as string).trim()
    if (!handle) return
    window.location.href = `/login?handle=${encodeURIComponent(handle)}`
  }
</script>

<div class="screen">
  <div class="marquee">
    <h1 class="marquee-title">AT PLAY<br>SYNC</h1>
    <form class="signin-form" onsubmit={handleSubmit}>
      <label class="field-label" for="handle">Your Atmosphere handle</label>
      <input class="text-input" id="handle" name="handle" placeholder="your.handle" autocomplete="username" required />
      <button class="btn btn-primary" type="submit">Press Start</button>
    </form>
  </div>
</div>
