<script lang="ts">
  import { settings } from './store';
  import { setHighlightTheme } from './markdown';

  export let providers: string[] = [];
  export let busy: boolean = false;

  let showSystem = true;
  let showAppearance = true;
  let showProvider = true;

  function toggleTheme() {
    const next: 'light' | 'dark' = $settings.theme === 'light' ? 'dark' : 'light';
    settings.update(s => ({ ...s, theme: next }));
    document.documentElement.classList.toggle('dark', next === 'dark');
    setHighlightTheme(next);
  }
</script>

<div class="panel">
  <div class="header">Model Parameters</div>

  <section>
    <button class="section-head" on:click={() => (showProvider = !showProvider)}>
      <span class="caret">{showProvider ? '▾' : '▸'}</span>
      <span>Provider</span>
    </button>
    {#if showProvider}
      <div class="section-body">
        <label class="field">
          <span>Provider</span>
          <select
            bind:value={$settings.provider}
            disabled={busy || providers.length === 0}
          >
            {#each providers as p}<option value={p}>{p}</option>{/each}
          </select>
        </label>
        <label class="field row">
          <input type="checkbox" bind:checked={$settings.streaming} disabled={busy} />
          <span>Stream tokens</span>
        </label>
      </div>
    {/if}
  </section>

  <section>
    <button class="section-head" on:click={() => (showSystem = !showSystem)}>
      <span class="caret">{showSystem ? '▾' : '▸'}</span>
      <span>System Prompt</span>
    </button>
    {#if showSystem}
      <div class="section-body">
        <textarea
          bind:value={$settings.system}
          disabled={busy}
          rows="6"
          placeholder="Optional system prompt. Applies to all messages in the active chat."
        ></textarea>
      </div>
    {/if}
  </section>

  <section>
    <button class="section-head" on:click={() => (showAppearance = !showAppearance)}>
      <span class="caret">{showAppearance ? '▾' : '▸'}</span>
      <span>Appearance</span>
    </button>
    {#if showAppearance}
      <div class="section-body">
        <label class="field row">
          <span>Theme</span>
          <button class="theme-btn" on:click={toggleTheme}>
            {$settings.theme === 'light' ? 'light → dark' : 'dark → light'}
          </button>
        </label>
      </div>
    {/if}
  </section>
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--bg-elev);
    border-left: 1px solid var(--border);
    overflow-y: auto;
  }

  .header {
    padding: 0.85rem 0.85rem 0.6rem;
    font-weight: 600;
    font-size: 0.9rem;
    border-bottom: 1px solid var(--border);
  }

  section { border-bottom: 1px solid var(--border); }

  .section-head {
    width: 100%;
    background: transparent;
    border: none;
    padding: 0.6rem 0.85rem;
    text-align: left;
    cursor: pointer;
    color: var(--fg);
    font-size: 0.85rem;
    font-weight: 500;
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-family: inherit;
  }
  .section-head:hover { background: var(--hover-bg); }
  .caret { font-size: 0.7rem; color: var(--fg-muted); width: 0.8em; }

  .section-body {
    padding: 0.4rem 0.85rem 0.75rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.8rem;
    color: var(--fg-muted);
  }
  .field.row {
    flex-direction: row;
    align-items: center;
    gap: 0.5rem;
  }
  .field.row input[type="checkbox"] { margin: 0; }

  select {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.35rem 0.5rem;
    font-size: 0.85rem;
    outline: none;
    font-family: inherit;
  }
  select:disabled { opacity: 0.5; }

  textarea {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.5rem;
    font-family: inherit;
    font-size: 0.85rem;
    resize: vertical;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }
  textarea:focus { border-color: var(--border-strong); }
  textarea:disabled { opacity: 0.5; }

  .theme-btn {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.3rem 0.6rem;
    font-size: 0.8rem;
    cursor: pointer;
    font-family: inherit;
  }
  .theme-btn:hover { background: var(--hover-bg); }
</style>
