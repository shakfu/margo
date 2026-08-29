<script lang="ts">
  // Message composer: attachment strip, drag-and-drop, slash-command
  // hints, the textarea, the context gauge, and send/cancel.
  //
  // Extracted from App.svelte. It owns the input text and the pending
  // attachments (both bound out, because send() needs them) plus every
  // mechanic for getting files in. The parent keeps send/cancel, since
  // those drive the stream it owns.
  import { createEventDispatcher } from 'svelte';
  import { effectiveSettings, settings } from './store';
  import { SLASH_COMMANDS, type SlashSuggestion } from './slash';
  import {
    ATTACHMENT_MIME_ACCEPT,
    rejectionReason,
    toPendingAttachment,
    isImage,
    type PendingAttachment,
  } from './attachments';

  export let input = '';
  export let attachments: PendingAttachment[] = [];
  export let busy = false;
  export let streaming = false;
  export let cancelling = false;
  export let attachmentsBlocked = false;
  export let ctxUsed = 0;
  export let ctxWindow = 0;

  const dispatch = createEventDispatcher<{ send: void; cancel: void; error: string }>();

  let dragOver = false;
  let fileInputEl: HTMLInputElement | null = null;

  $: ctxPct = ctxWindow > 0 ? Math.min(100, Math.round((ctxUsed / ctxWindow) * 100)) : 0;

  // Slash autocomplete. Suggestions populate from the static command
  // catalog plus the user's persona names, which is why the personas
  // list is read here rather than passed in.
  function computeSlashSuggestions(s: string, personas: { name: string }[]): SlashSuggestion[] {
    if (!s.startsWith('/')) return [];
    const typed = s.slice(1).toLowerCase();
    const out: SlashSuggestion[] = [];
    for (const c of SLASH_COMMANDS) {
      if (c.command.toLowerCase().startsWith(typed)) {
        out.push({ text: `/${c.command} `, description: c.description });
      }
    }
    if ('persona'.startsWith(typed) || typed.startsWith('persona')) {
      for (const p of personas) {
        const slug = p.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
        const text = `/persona ${slug} `;
        if (text.toLowerCase().startsWith(s.toLowerCase())) {
          out.push({ text, description: `Switch to the ${p.name} persona` });
        }
      }
    }
    return out.slice(0, 8);
  }

  $: slashSuggestions = computeSlashSuggestions(input, $settings.personas);
  $: showSlashHint = input.startsWith('/') && slashSuggestions.length > 0;

  function applySuggestion(text: string) {
    input = text;
  }

  async function addFiles(files: FileList | File[] | null) {
    if (!files) return;
    for (const file of Array.from(files)) {
      const reason = rejectionReason(file);
      if (reason) {
        dispatch('error', reason);
        continue;
      }
      try {
        attachments = [...attachments, await toPendingAttachment(file)];
      } catch (e) {
        dispatch('error', `Failed to read "${file.name}": ${String(e)}`);
      }
    }
  }

  function removeAttachment(id: string) {
    const found = attachments.find((a) => a.id === id);
    if (found) URL.revokeObjectURL(found.previewUrl);
    attachments = attachments.filter((a) => a.id !== id);
  }

  function onPaperclip() {
    fileInputEl?.click();
  }

  function onFileInputChange(ev: Event) {
    const el = ev.currentTarget as HTMLInputElement;
    void addFiles(el.files);
    el.value = '';
  }

  function onComposerDragOver(ev: DragEvent) {
    ev.preventDefault();
    dragOver = true;
  }
  function onComposerDragLeave() {
    dragOver = false;
  }
  function onComposerDrop(ev: DragEvent) {
    ev.preventDefault();
    dragOver = false;
    void addFiles(ev.dataTransfer?.files ?? null);
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      dispatch('send');
    }
  }
</script>

<footer
  class="flex flex-col gap-2 px-5 pt-3.5 pb-4 border-t border-border max-w-[820px] w-full mx-auto box-border {dragOver ? 'bg-bubble-user/40' : ''}"
  on:dragover={onComposerDragOver}
  on:dragleave={onComposerDragLeave}
  on:drop={onComposerDrop}
>
  {#if attachments.length > 0}
    <div class="flex flex-wrap gap-2">
      {#each attachments as a (a.id)}
        {#if isImage(a.mimeType)}
          <div class="relative group" title="{a.name} ({Math.round(a.size / 1024)} KB)">
            <img src={a.previewUrl} alt={a.name} class="h-14 w-14 object-cover rounded border border-border" />
            <button
              class="absolute -top-1 -right-1 bg-bg-elev border border-border rounded-full w-4 h-4 flex items-center justify-center text-[0.7rem] leading-none cursor-pointer hover:bg-hover-bg"
              on:click={() => removeAttachment(a.id)}
              aria-label="remove attachment"
            >×</button>
          </div>
        {:else}
          <div class="relative group flex items-center gap-2 px-2 py-1.5 border border-border bg-input-bg rounded text-[0.74rem] text-fg-muted" title="{a.name} ({Math.round(a.size / 1024)} KB)">
            <span class="font-[family-name:var(--font-mono)]">📄</span>
            <span class="truncate max-w-[140px]">{a.name}</span>
            <button
              class="absolute -top-1 -right-1 bg-bg-elev border border-border rounded-full w-4 h-4 flex items-center justify-center text-[0.7rem] leading-none cursor-pointer hover:bg-hover-bg"
              on:click={() => removeAttachment(a.id)}
              aria-label="remove attachment"
            >×</button>
          </div>
        {/if}
      {/each}
    </div>
  {/if}
  {#if dragOver}
    <div class="text-[0.74rem] text-fg-muted italic">Drop image to attach…</div>
  {/if}
  {#if attachmentsBlocked}
    <div class="text-[0.74rem] text-error-fg">
      <strong>{$effectiveSettings.model}</strong> doesn't accept images. Switch to a vision-capable model (e.g. claude-sonnet-4-6, gpt-5.4) or remove the attachments to send.
    </div>
  {/if}
  <input
    type="file"
    accept={ATTACHMENT_MIME_ACCEPT.join(',')}
    multiple
    bind:this={fileInputEl}
    on:change={onFileInputChange}
    class="hidden"
  />
  {#if showSlashHint}
    <div class="border border-border rounded-md bg-bg-elev p-1 flex flex-col gap-0.5 text-[0.78rem]">
      {#each slashSuggestions as s (s.text)}
        <button
          type="button"
          class="text-left px-2 py-1 rounded hover:bg-hover-bg flex items-center gap-2 bg-bubble-user"
          on:click={() => applySuggestion(s.text)}
          on:mousedown|preventDefault
        >
          <code class="font-[family-name:var(--font-mono)] text-fg">{s.text.trim()}</code>
          <span class="text-fg-muted truncate">{s.description}</span>
        </button>
      {/each}
    </div>
  {/if}
  <div class="flex items-end gap-2">
    <button
      class="topbar-btn h-9 w-9 flex items-center justify-center"
      on:click={onPaperclip}
      title="Attach image"
      disabled={busy || !$effectiveSettings.provider}
      aria-label="attach image"
    >📎</button>
    <textarea
      class="flex-1 bg-input-bg text-fg border border-border rounded-md px-3 py-2.5 font-[inherit] text-[0.9rem] resize-none outline-none focus:border-border-strong disabled:opacity-50 disabled:cursor-not-allowed"
      placeholder={$effectiveSettings.provider ? "Send a message, or type / for commands…" : "Configure a provider in the settings panel..."}
      bind:value={input}
      on:keydown={onKey}
      disabled={busy || !$effectiveSettings.provider}
      rows="2"
    ></textarea>
    <div class="flex flex-col items-center gap-1">
      <div
        class="ctx-ring"
        title="{ctxUsed.toLocaleString()} / {ctxWindow.toLocaleString()} tokens"
        style="--pct: {ctxPct}"
      >
        <span>{ctxPct}%</span>
      </div>
      {#if busy && streaming}
        <button
          class="composer-btn cancel-btn"
          on:click={() => dispatch('cancel')}
          disabled={cancelling}
        >{cancelling ? 'cancelling…' : 'cancel'}</button>
      {:else}
        <button
          class="composer-btn send-btn"
          on:click={() => dispatch('send')}
          disabled={busy || !$effectiveSettings.provider || attachmentsBlocked || (!input.trim() && attachments.length === 0)}
        >{busy ? '...' : 'send'}</button>
      {/if}
    </div>
  </div>
</footer>

<style>

  .composer-btn {
    padding: 0 1.1rem;
    min-width: 80px;
    height: 2rem;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .send-btn:hover:not(:disabled) { background: var(--hover-bg); }
  .send-btn:disabled { opacity: 0.4; cursor: not-allowed; }
  .cancel-btn {
    background: var(--error-bg);
    color: var(--error-fg);
    border-color: var(--error-border);
  }
  .cancel-btn:hover { filter: brightness(1.05); }

  .ctx-ring {
    width: 2rem;
    height: 2rem;
    border-radius: 50%;
    background:
      conic-gradient(var(--fg-muted) calc(var(--pct) * 1%), var(--input-bg) 0);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.6rem;
    color: var(--fg-muted);
    font-variant-numeric: tabular-nums;
    position: relative;
  }
  .ctx-ring::before {
    content: '';
    position: absolute;
    inset: 3px;
    background: var(--bg);
    border-radius: 50%;
  }
  .ctx-ring span { position: relative; z-index: 1; }
</style>
