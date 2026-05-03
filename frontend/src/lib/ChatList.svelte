<script lang="ts">
  import { createDialog, melt } from '@melt-ui/svelte';
  import { chats, activeChatId, newChat, deleteChat, renameChat } from './store';

  export let busy: boolean = false;

  let search = '';
  let renamingId = '';
  let renameValue = '';

  const {
    elements: { trigger: dlgTrigger, overlay: dlgOverlay, content: dlgContent, title: dlgTitle, close: dlgClose, portalled: dlgPortalled },
    states: { open: dlgOpen },
  } = createDialog({ role: 'alertdialog' });

  let pendingDelete: { id: string; title: string } | null = null;

  function askDelete(id: string, title: string, e: Event) {
    e.stopPropagation();
    pendingDelete = { id, title };
    dlgOpen.set(true);
  }

  function confirmDelete() {
    if (pendingDelete) deleteChat(pendingDelete.id);
    pendingDelete = null;
    dlgOpen.set(false);
  }

  function cancelDelete() {
    pendingDelete = null;
    dlgOpen.set(false);
  }

  $: filtered = $chats.filter(c =>
    c.title.toLowerCase().includes(search.toLowerCase())
  );

  function select(id: string) {
    if (busy) return;
    activeChatId.set(id);
  }

  function startRename(id: string, current: string, e: Event) {
    e.stopPropagation();
    renamingId = id;
    renameValue = current;
  }

  function commitRename() {
    if (renamingId && renameValue.trim()) {
      renameChat(renamingId, renameValue.trim());
    }
    renamingId = '';
  }

  function onRenameKey(e: KeyboardEvent) {
    if (e.key === 'Enter') { e.preventDefault(); commitRename(); }
    if (e.key === 'Escape') { renamingId = ''; }
  }

  function focus(node: HTMLInputElement) {
    node.focus();
    node.select();
  }

  function relativeTime(ts: number): string {
    const s = Math.floor((Date.now() - ts) / 1000);
    if (s < 60) return 'now';
    if (s < 3600) return `${Math.floor(s / 60)}m`;
    if (s < 86400) return `${Math.floor(s / 3600)}h`;
    return `${Math.floor(s / 86400)}d`;
  }
</script>

<div class="flex flex-col h-full bg-bg-elev border-r border-border">
  <div class="flex items-center justify-between px-3.5 pt-3 pb-1.5">
    <span class="font-semibold text-[0.9rem]">Chats</span>
    <button
      class="bg-input-bg text-fg border border-border rounded px-2 py-0.5 text-base leading-none cursor-pointer hover:bg-hover-bg disabled:opacity-40 disabled:cursor-not-allowed"
      on:click={newChat}
      disabled={busy}
      title="New chat"
    >+</button>
  </div>

  <input
    class="mx-3.5 mb-2 px-2 py-1.5 bg-input-bg text-fg border border-border rounded outline-none text-[0.85rem] focus:border-border-strong"
    type="text"
    placeholder="Search chats..."
    bind:value={search}
  />

  <div class="flex-1 overflow-y-auto px-2 pb-2">
    {#each filtered as c (c.id)}
      <div
        class="group relative px-2.5 py-2 rounded cursor-pointer mb-0.5 hover:bg-hover-bg {c.id === $activeChatId ? 'bg-accent' : ''}"
        on:click={() => select(c.id)}
        on:keydown={(e) => e.key === 'Enter' && select(c.id)}
        role="button"
        tabindex="0"
      >
        {#if renamingId === c.id}
          <input
            class="w-full px-1.5 py-1 border border-border-strong rounded bg-bg text-fg text-[0.85rem] outline-none box-border"
            bind:value={renameValue}
            on:blur={commitRename}
            on:keydown={onRenameKey}
            on:click|stopPropagation
            use:focus
          />
        {:else}
          <div class="flex justify-between gap-2 items-baseline">
            <span class="text-[0.88rem] overflow-hidden text-ellipsis whitespace-nowrap flex-1">{c.title}</span>
            <span class="text-[0.7rem] text-fg-faint shrink-0">{relativeTime(c.updatedAt)}</span>
          </div>
          <div class="text-[0.7rem] text-fg-faint mt-0.5">{c.messages.length} msg</div>
          <div class="absolute right-1.5 top-1.5 flex gap-0.5 opacity-0 transition-opacity duration-100 group-hover:opacity-100">
            <button
              class="bg-bg text-fg-muted border border-border rounded-sm px-1.5 py-0.5 text-[0.7rem] cursor-pointer hover:bg-hover-bg hover:text-fg"
              on:click={(e) => startRename(c.id, c.title, e)}
              title="Rename"
            >edit</button>
            <button
              class="bg-bg text-fg-muted border border-border rounded-sm p-1 cursor-pointer hover:text-error-fg hover:border-error-border inline-flex items-center justify-center"
              on:click={(e) => askDelete(c.id, c.title, e)}
              title="Delete"
              aria-label="Delete chat"
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="12" height="12" aria-hidden="true">
                <path d="M3 6h18"/>
                <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/>
                <path d="M10 11v6M14 11v6"/>
              </svg>
            </button>
          </div>
        {/if}
      </div>
    {/each}
    {#if filtered.length === 0}
      <div class="py-6 text-center text-fg-faint text-[0.85rem]">{search ? 'No matches.' : 'No chats yet.'}</div>
    {/if}
  </div>
</div>

<div use:melt={$dlgPortalled}>
  {#if $dlgOpen}
    <div use:melt={$dlgOverlay} class="fixed inset-0 z-40 bg-black/40"></div>
    <div
      use:melt={$dlgContent}
      class="fixed left-1/2 top-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-[min(22rem,90vw)] rounded-md border border-border bg-bg-elev p-4 shadow-xl"
    >
      <h2 use:melt={$dlgTitle} class="text-[0.95rem] font-semibold text-fg">Delete chat?</h2>
      <p class="mt-2 text-[0.85rem] text-fg-muted break-words">
        {pendingDelete ? `"${pendingDelete.title}" will be removed permanently.` : ''}
      </p>
      <div class="mt-4 flex justify-end gap-2">
        <button
          use:melt={$dlgClose}
          class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
          on:click={cancelDelete}
        >Cancel</button>
        <button
          class="px-3 py-1.5 text-[0.85rem] rounded border border-error-border bg-error-bg text-error-fg cursor-pointer hover:opacity-90 font-semibold"
          on:click={confirmDelete}
        >Delete</button>
      </div>
    </div>
  {/if}
</div>
