<script lang="ts">
  // Knowledge sources section of the Roles tab. Workspace-mode only —
  // each workspace has its own chromem-go collection.
  import { settings } from '../store';
  import { createCollapsible, melt } from '@melt-ui/svelte';
  import { IndexPath, KnowledgeSources, DeleteKnowledgeSource, PickKnowledgePath } from '../../../wailsjs/go/main/App.js';
  import { onMount } from 'svelte';

  type KSource = { path: string; isDir: boolean; fileCount: number; chunkCount: number; indexedAt: string };
  let kbSources: KSource[] = [];
  let kbBusy = false;
  let kbError = '';
  let kbStatus = '';

  const { elements: { root: kbRoot, trigger: kbTrig, content: kbContent }, states: { open: kbOpen } } =
    createCollapsible({ defaultOpen: false });

  async function refreshKbSources() {
    try {
      kbSources = (await KnowledgeSources($settings.activeWorkspaceId)) as KSource[];
    } catch (e) {
      kbError = String(e);
    }
  }

  onMount(refreshKbSources);
  // Reload when active workspace changes — the indexed-source list is per-workspace.
  $: { void $settings.activeWorkspaceId; refreshKbSources(); }

  async function indexAtPath(path: string) {
    kbError = '';
    kbStatus = '';
    kbBusy = true;
    try {
      const r = await IndexPath($settings.activeWorkspaceId, path) as { path: string; fileCount: number; chunkCount: number; skippedFiles?: number; embeddedFiles?: number };
      const skipped = r.skippedFiles ?? 0;
      const embedded = r.embeddedFiles ?? r.fileCount;
      if (skipped > 0) {
        kbStatus = `indexed ${r.path}: ${embedded} embedded, ${skipped} unchanged`;
      } else {
        kbStatus = `indexed ${r.path}: ${embedded} ${embedded === 1 ? 'file' : 'files'}, ${r.chunkCount} chunks`;
      }
      await refreshKbSources();
    } catch (e) {
      kbError = String(e);
    } finally {
      kbBusy = false;
    }
  }

  async function addKbPath(dirOnly: boolean) {
    kbError = '';
    const path = await PickKnowledgePath(dirOnly);
    if (!path) return;
    await indexAtPath(path);
  }

  async function refreshKbSource(path: string) {
    await indexAtPath(path);
  }

  async function removeKbSource(path: string) {
    kbError = '';
    try {
      await DeleteKnowledgeSource($settings.activeWorkspaceId, path);
      await refreshKbSources();
    } catch (e) {
      kbError = String(e);
    }
  }
</script>

<section class="border-b border-border" use:melt={$kbRoot}>
  <button class="section-head" use:melt={$kbTrig}>
    <span class="caret">{$kbOpen ? '▾' : '▸'}</span>
    <span>Knowledge sources</span>
    {#if kbSources.length > 0}
      <span class="ml-1 text-fg-faint text-[0.72rem]">({kbSources.length})</span>
    {/if}
  </button>
  <div use:melt={$kbContent} class="section-body">
    <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
      Files and folders indexed for this workspace. Pick one, then enable the <code class="font-[family-name:var(--font-mono)] text-[0.72rem]">search_knowledge</code> tool on an agent to let it query the index.
    </p>
    {#if kbSources.length === 0}
      <div class="text-[0.78rem] text-fg-faint italic mb-2">Nothing indexed yet.</div>
    {:else}
      <ul class="flex flex-col gap-1 mb-2">
        {#each kbSources as s (s.path)}
          <li class="flex items-start gap-2 text-[0.78rem] bg-input-bg border border-border rounded px-2 py-1.5">
            <div class="flex-1 min-w-0">
              <div class="font-[family-name:var(--font-mono)] text-[0.72rem] break-all">{s.path}</div>
              <div class="text-fg-faint text-[0.65rem] mt-0.5">
                {s.isDir ? 'folder' : 'file'} · {s.fileCount} {s.fileCount === 1 ? 'file' : 'files'} · {s.chunkCount} chunks
              </div>
            </div>
            <div class="flex flex-col gap-1 shrink-0">
              <button class="mini-btn" on:click={() => refreshKbSource(s.path)} disabled={kbBusy} title="Re-scan; unchanged files are reused">Refresh</button>
              <button class="mini-btn" on:click={() => removeKbSource(s.path)} disabled={kbBusy}>Remove</button>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
    <div class="flex gap-2">
      <button class="mini-btn" on:click={() => addKbPath(false)} disabled={kbBusy}>+ Index file</button>
      <button class="mini-btn" on:click={() => addKbPath(true)} disabled={kbBusy}>+ Index folder</button>
    </div>
    {#if kbBusy}<div class="text-[0.7rem] text-fg-faint mt-1 italic">indexing…</div>{/if}
    {#if kbStatus && !kbBusy}<div class="text-[0.7rem] text-fg-muted mt-1 break-words">{kbStatus}</div>{/if}
    {#if kbError}<div class="text-[0.7rem] text-error-fg mt-1 break-words">{kbError}</div>{/if}
  </div>
</section>
