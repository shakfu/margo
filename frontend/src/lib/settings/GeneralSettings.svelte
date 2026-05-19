<script lang="ts">
  // General tab: Appearance / Output / Reset. Global-mode only; the
  // workspace right-pane hides this whole tab. Owns the Reset confirm
  // dialog so the parent shell stays free of dialog state.
  import { settings } from '../store';
  import { setHighlightTheme } from '../markdown';
  import { createCollapsible, createDialog, melt } from '@melt-ui/svelte';
  import { OpenPath } from '../../../wailsjs/go/main/App.js';

  export let outputDir: string = '';
  export let onReset: () => void = () => {};

  const mk = (open: boolean) => createCollapsible({ defaultOpen: open });
  const { elements: { root: apprRoot, trigger: apprTrig, content: apprContent }, states: { open: apprOpen } } = mk(false);
  const { elements: { root: outRoot, trigger: outTrig, content: outContent }, states: { open: outOpen } } = mk(false);
  const { elements: { root: resetRoot, trigger: resetTrig, content: resetContent }, states: { open: resetOpen } } = mk(false);

  const {
    elements: { overlay: resetDlgOverlay, content: resetDlgContent, title: resetDlgTitle, close: resetDlgClose, portalled: resetDlgPortalled },
    states: { open: resetDlgOpen },
  } = createDialog({ role: 'alertdialog' });

  function toggleTheme() {
    const next: 'light' | 'dark' = $settings.theme === 'light' ? 'dark' : 'light';
    settings.update(s => ({ ...s, theme: next }));
    document.documentElement.classList.toggle('dark', next === 'dark');
    setHighlightTheme(next);
  }

  function openOutputDir() {
    if (outputDir) OpenPath(outputDir);
  }

  function confirmReset() {
    resetDlgOpen.set(false);
    onReset();
  }
</script>

<!-- Appearance -->
<section class="border-b border-border" use:melt={$apprRoot}>
  <button class="section-head" use:melt={$apprTrig}>
    <span class="caret">{$apprOpen ? '▾' : '▸'}</span>
    <span>Appearance</span>
  </button>
  <div use:melt={$apprContent} class="section-body">
    <label class="flex flex-row items-center gap-2 text-[0.8rem] text-fg-muted">
      <span>Theme</span>
      <button class="mini-btn" on:click={toggleTheme}>
        {$settings.theme === 'light' ? 'light → dark' : 'dark → light'}
      </button>
    </label>
  </div>
</section>

<!-- Output -->
<section class="border-b border-border" use:melt={$outRoot}>
  <button class="section-head" use:melt={$outTrig}>
    <span class="caret">{$outOpen ? '▾' : '▸'}</span>
    <span>Output</span>
  </button>
  <div use:melt={$outContent} class="section-body">
    <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
      Tools that generate files (e.g. <code>quarto_render</code>) write here.
    </p>
    <div class="text-[0.78rem] font-[family-name:var(--font-mono)] text-fg break-all bg-input-bg border border-border rounded px-2 py-1 mb-2">
      {outputDir || 'unavailable'}
    </div>
    <button class="mini-btn" on:click={openOutputDir} disabled={!outputDir}>Open in Finder</button>
  </div>
</section>

<!-- Reset -->
<section class="border-b border-border" use:melt={$resetRoot}>
  <button class="section-head" use:melt={$resetTrig}>
    <span class="caret">{$resetOpen ? '▾' : '▸'}</span>
    <span>Reset</span>
  </button>
  <div use:melt={$resetContent} class="section-body">
    <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
      Cancels any in-flight stream, clears all chats and settings from this device, and reloads the app. Cannot be undone.
    </p>
    <button
      class="mini-btn border-error-border bg-error-bg text-error-fg hover:opacity-90"
      on:click={() => resetDlgOpen.set(true)}
    >Reset margo…</button>
  </div>
</section>

<div use:melt={$resetDlgPortalled}>
  {#if $resetDlgOpen}
    <div use:melt={$resetDlgOverlay} class="fixed inset-0 z-40 bg-black/40"></div>
    <div
      use:melt={$resetDlgContent}
      class="fixed left-1/2 top-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-[min(24rem,90vw)] rounded-md border border-border bg-bg-elev p-4 shadow-xl"
    >
      <h2 use:melt={$resetDlgTitle} class="text-[0.95rem] font-semibold text-fg">Reset margo?</h2>
      <p class="mt-2 text-[0.85rem] text-fg-muted break-words">
        All chats and settings on this device will be deleted, the active stream (if any) will be cancelled, and the app will reload. This cannot be undone.
      </p>
      <div class="mt-4 flex justify-end gap-2">
        <button use:melt={$resetDlgClose} class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg">Cancel</button>
        <button class="px-3 py-1.5 text-[0.85rem] rounded border border-error-border bg-error-bg text-error-fg cursor-pointer hover:opacity-90 font-semibold" on:click={confirmReset}>Reset</button>
      </div>
    </div>
  {/if}
</div>
