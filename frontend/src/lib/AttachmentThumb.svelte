<script lang="ts">
  // Lazily renders a stored attachment as a thumbnail or document badge.
  // Calls the Wails LoadAttachment binding once per mount to read the
  // on-disk bytes back as base64; converts to a data: URL for an <img>.
  // No global cache — chats with dozens of attachments would benefit
  // from one, but the typical case is 1–3 per turn and the cost is a
  // single 1–10 KB Wails round-trip on first paint.
  import { onMount } from 'svelte';
  import type { StoredAttachment } from './store';
  import { LoadAttachment, OpenPath } from '../../wailsjs/go/main/App.js';

  export let a: StoredAttachment;

  let dataUrl = '';
  let err = '';

  $: isImage = a.mimeType.startsWith('image/');

  onMount(async () => {
    if (!isImage) return; // documents render as a badge; no need to load bytes
    try {
      const b64 = await LoadAttachment(a.path);
      dataUrl = `data:${a.mimeType};base64,${b64}`;
    } catch (e) {
      err = String(e);
    }
  });

  function openOriginal() {
    OpenPath(a.path).catch(() => {});
  }

  function sizeLabel(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
    return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  }
</script>

{#if isImage}
  <button
    class="relative group block"
    title={`${a.name} (${sizeLabel(a.size)})`}
    on:click={openOriginal}
    aria-label={`Open ${a.name}`}
  >
    {#if dataUrl}
      <img src={dataUrl} alt={a.name} class="h-14 w-14 object-cover rounded border border-border" />
    {:else if err}
      <div class="h-14 w-14 rounded border border-error-border bg-error-bg flex items-center justify-center text-[0.6rem] text-error-fg" title={err}>!</div>
    {:else}
      <div class="h-14 w-14 rounded border border-border bg-input-bg" />
    {/if}
  </button>
{:else}
  <button
    class="flex items-center gap-2 px-2 py-1 border border-border bg-input-bg rounded text-[0.74rem] text-fg-muted hover:bg-hover-bg"
    title={a.path}
    on:click={openOriginal}
  >
    <span class="font-[family-name:var(--font-mono)]">📄</span>
    <span class="truncate max-w-[180px]">{a.name}</span>
    <span class="text-fg-faint shrink-0">{sizeLabel(a.size)}</span>
  </button>
{/if}
