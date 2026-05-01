<script lang="ts">
  import { chats, activeChatId, newChat, deleteChat, renameChat } from './store';

  export let busy: boolean = false;

  let search = '';
  let renamingId = '';
  let renameValue = '';

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

  let confirmingId = '';
  let confirmTimer: ReturnType<typeof setTimeout> | null = null;

  function clickDelete(id: string, e: Event) {
    e.stopPropagation();
    if (confirmingId === id) {
      if (confirmTimer) { clearTimeout(confirmTimer); confirmTimer = null; }
      confirmingId = '';
      deleteChat(id);
      return;
    }
    confirmingId = id;
    if (confirmTimer) clearTimeout(confirmTimer);
    confirmTimer = setTimeout(() => { confirmingId = ''; }, 3000);
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

<div class="chatlist">
  <div class="header">
    <span class="title">Chats</span>
    <button class="icon-btn" on:click={newChat} disabled={busy} title="New chat">+</button>
  </div>

  <input
    class="search"
    type="text"
    placeholder="Search chats..."
    bind:value={search}
  />

  <div class="list">
    {#each filtered as c (c.id)}
      <div
        class="item"
        class:active={c.id === $activeChatId}
        class:armed={confirmingId === c.id}
        on:click={() => select(c.id)}
        on:keydown={(e) => e.key === 'Enter' && select(c.id)}
        role="button"
        tabindex="0"
      >
        {#if renamingId === c.id}
          <input
            class="rename-input"
            bind:value={renameValue}
            on:blur={commitRename}
            on:keydown={onRenameKey}
            on:click|stopPropagation
            use:focus
          />
        {:else}
          <div class="row">
            <span class="name">{c.title}</span>
            <span class="time">{relativeTime(c.updatedAt)}</span>
          </div>
          <div class="meta">{c.messages.length} msg</div>
          <div class="actions">
            <button class="mini" on:click={(e) => startRename(c.id, c.title, e)} title="Rename">edit</button>
            <button
              class="mini danger"
              class:armed={confirmingId === c.id}
              on:click={(e) => clickDelete(c.id, e)}
              title={confirmingId === c.id ? 'Click again to confirm' : 'Delete'}
            >{confirmingId === c.id ? 'sure?' : '×'}</button>
          </div>
        {/if}
      </div>
    {/each}
    {#if filtered.length === 0}
      <div class="empty">{search ? 'No matches.' : 'No chats yet.'}</div>
    {/if}
  </div>
</div>

<style>
  .chatlist {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--bg-elev);
    border-right: 1px solid var(--border);
  }

  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.75rem 0.85rem 0.4rem;
  }
  .title { font-weight: 600; font-size: 0.9rem; }
  .icon-btn {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.15rem 0.5rem;
    font-size: 1rem;
    line-height: 1;
    cursor: pointer;
  }
  .icon-btn:hover:not(:disabled) { background: var(--hover-bg); }
  .icon-btn:disabled { opacity: 0.4; cursor: not-allowed; }

  .search {
    margin: 0 0.85rem 0.5rem;
    padding: 0.4rem 0.55rem;
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 5px;
    outline: none;
    font-size: 0.85rem;
    font-family: inherit;
  }
  .search:focus { border-color: var(--border-strong); }

  .list {
    flex: 1;
    overflow-y: auto;
    padding: 0 0.5rem 0.5rem;
  }

  .item {
    position: relative;
    padding: 0.5rem 0.6rem;
    border-radius: 5px;
    cursor: pointer;
    margin-bottom: 0.15rem;
  }
  .item:hover { background: var(--hover-bg); }
  .item.active { background: var(--accent); }
  .item:hover .actions, .item.armed .actions { opacity: 1; }

  .row {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
    align-items: baseline;
  }
  .name {
    font-size: 0.88rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
  }
  .time {
    font-size: 0.7rem;
    color: var(--fg-faint);
    flex-shrink: 0;
  }
  .meta {
    font-size: 0.7rem;
    color: var(--fg-faint);
    margin-top: 0.15rem;
  }

  .actions {
    position: absolute;
    right: 0.4rem;
    top: 0.4rem;
    display: flex;
    gap: 0.2rem;
    opacity: 0;
    transition: opacity 100ms;
  }
  .mini {
    background: var(--bg);
    color: var(--fg-muted);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0.1rem 0.35rem;
    font-size: 0.7rem;
    cursor: pointer;
  }
  .mini:hover { background: var(--hover-bg); color: var(--fg); }
  .mini.danger:hover { color: var(--error-fg); border-color: var(--error-border); }
  .mini.armed {
    background: var(--error-bg);
    color: var(--error-fg);
    border-color: var(--error-border);
    padding: 0.1rem 0.5rem;
    font-weight: 600;
  }

  .rename-input {
    width: 100%;
    padding: 0.3rem 0.4rem;
    border: 1px solid var(--border-strong);
    border-radius: 4px;
    background: var(--bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.85rem;
    outline: none;
    box-sizing: border-box;
  }

  .empty {
    padding: 1.5rem 0.5rem;
    text-align: center;
    color: var(--fg-faint);
    font-size: 0.85rem;
  }
</style>
