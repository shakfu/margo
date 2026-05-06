<script lang="ts">
  import { createDialog, createSelect, melt } from '@melt-ui/svelte';
  import { get } from 'svelte/store';
  import {
    chats, activeChatId, settings, activeWorkspace,
    newChat, deleteChat, renameChat,
    addWorkspace, renameWorkspace, setWorkspaceDir, deleteWorkspace, setActiveWorkspace,
    createWorkspaceFromTemplate,
    DEFAULT_WORKSPACE_ID,
    WORKSPACE_TEMPLATES,
    type Workspace,
  } from './store';
  import { PickWorkspaceDir } from '../../wailsjs/go/main/App.js';

  export let busy: boolean = false;

  let search = '';
  let renamingId = '';
  let renameValue = '';

  const {
    elements: { trigger: dlgTrigger, overlay: dlgOverlay, content: dlgContent, title: dlgTitle, close: dlgClose, portalled: dlgPortalled },
    states: { open: dlgOpen },
  } = createDialog({ role: 'alertdialog' });

  // Workspace manager dialog (separate from the chat-delete alertdialog).
  const {
    elements: {
      overlay: wsOverlay, content: wsContent, title: wsTitle,
      close: wsClose, portalled: wsPortalled,
    },
    states: { open: wsOpen },
  } = createDialog({ role: 'dialog' });

  let pendingDelete: { id: string; title: string } | null = null;
  let newWorkspaceName = '';
  // Template selection in the create form. Empty string = "Empty
  // workspace" (no template). 7.1.f.
  let newWorkspaceTemplateId = '';
  let editingWorkspaceId = '';
  let editingWorkspaceName = '';

  // Workspace Melt UI Select. Mirrors the Provider/Model selects in
  // SettingsPanel so the styling stays consistent. (Replaces the native
  // <select> shipped in 7.1.a.)
  const initialWs = $settings.workspaces.find(w => w.id === $settings.activeWorkspaceId);
  const {
    elements: { trigger: wsSelTrig, menu: wsSelMenu, option: wsSelOpt },
    states: { selectedLabel: wsSelLabel, open: wsSelOpen, selected: wsSelected },
    helpers: { isSelected: isWsSelected },
  } = createSelect<string>({
    positioning: { placement: 'bottom', sameWidth: true },
    defaultSelected: initialWs ? { value: initialWs.id, label: initialWs.name } : undefined,
  });

  // User picks a workspace -> swap. Guards against stale stores by
  // checking against the current settings before calling setActive
  // (which itself short-circuits on no-op anyway).
  wsSelected.subscribe(s => {
    if (!s) return;
    if (s.value !== get(settings).activeWorkspaceId) {
      setActiveWorkspace(s.value);
    }
  });
  // External activation (CLI flag, manage dialog, programmatic) -> sync
  // the picker's label/state.
  settings.subscribe(s => {
    const cur = get(wsSelected);
    if (cur?.value === s.activeWorkspaceId) return;
    const ws = s.workspaces.find(w => w.id === s.activeWorkspaceId);
    if (ws) wsSelected.set({ value: ws.id, label: ws.name });
  });

  function openManage() {
    wsOpen.set(true);
  }

  function createWorkspace() {
    // Effective name: explicit input wins; otherwise the chosen
    // template's default name; otherwise blocked. The button is
    // disabled when both are empty so this just guards against
    // direct keyboard submission.
    const tpl = WORKSPACE_TEMPLATES.find(t => t.id === newWorkspaceTemplateId);
    const name = newWorkspaceName.trim() || (tpl?.name ?? '');
    if (!name) return;
    const id = tpl
      ? createWorkspaceFromTemplate(tpl, name)
      : addWorkspace(name);
    newWorkspaceName = '';
    newWorkspaceTemplateId = '';
    setActiveWorkspace(id);
  }

  // When the user picks a template, prefill the name field with the
  // template's default name unless they've already typed something.
  function onTemplateChange(ev: Event) {
    const v = (ev.currentTarget as HTMLSelectElement).value;
    newWorkspaceTemplateId = v;
    if (!newWorkspaceName.trim()) {
      const tpl = WORKSPACE_TEMPLATES.find(t => t.id === v);
      newWorkspaceName = tpl?.name ?? '';
    }
  }

  async function pickDir(id: string) {
    try {
      const path = await PickWorkspaceDir();
      if (path) setWorkspaceDir(id, path);
    } catch (_) {}
  }

  function clearDir(id: string) {
    setWorkspaceDir(id, undefined);
  }

  function startWsRename(w: Workspace) {
    editingWorkspaceId = w.id;
    editingWorkspaceName = w.name;
  }

  function commitWsRename() {
    if (editingWorkspaceId) renameWorkspace(editingWorkspaceId, editingWorkspaceName);
    editingWorkspaceId = '';
  }

  function onWsRenameKey(e: KeyboardEvent) {
    if (e.key === 'Enter') { e.preventDefault(); commitWsRename(); }
    if (e.key === 'Escape') { editingWorkspaceId = ''; }
  }

  function removeWorkspace(id: string) {
    deleteWorkspace(id);
  }

  // Workspaces sorted most-recently-used first (7.1.e). updatedAt is
  // bumped by setActiveWorkspace, so just-activated workspaces float
  // to the top of the picker. The manage dialog keeps insertion order.
  $: workspacesByRecency = [...$settings.workspaces].sort((a, b) => b.updatedAt - a.updatedAt);

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
  <div class="px-3.5 pt-3 pb-2 flex flex-col gap-1.5">
    <div class="flex items-baseline justify-between">
      <span class="font-semibold text-[0.9rem]">Workspace</span>
      <button
        class="text-[0.7rem] text-fg-faint hover:text-fg cursor-pointer"
        on:click={openManage}
        title="Create, rename, or delete workspaces"
      >manage…</button>
    </div>
    <button
      class="select-trigger"
      disabled={busy}
      use:melt={$wsSelTrig}
      title={$activeWorkspace?.dir ? `${$activeWorkspace.name} (${$activeWorkspace.dir})` : ($activeWorkspace?.name ?? 'Default')}
    >
      <span class="overflow-hidden text-ellipsis whitespace-nowrap">{$wsSelLabel || $activeWorkspace?.name || 'Default'}{$activeWorkspace?.dir ? ' ◦' : ''}</span>
      <span class="text-fg-faint text-[0.7rem]">{$wsSelOpen ? '▴' : '▾'}</span>
    </button>
    {#if $wsSelOpen}
      <div class="select-menu" use:melt={$wsSelMenu}>
        {#each workspacesByRecency as w (w.id)}
          <div
            class="select-item {$isWsSelected(w.id) ? 'bg-accent' : ''}"
            use:melt={$wsSelOpt({ value: w.id, label: w.name })}
            title={w.dir ?? ''}
          >
            <span>{w.name}</span>
            {#if w.dir}<span class="ml-1 text-fg-faint text-[0.7rem]">◦</span>{/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
  <div class="flex items-center justify-between px-3.5 pb-1.5 pt-1 border-t border-border">
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

<div use:melt={$wsPortalled}>
  {#if $wsOpen}
    <div use:melt={$wsOverlay} class="fixed inset-0 z-40 bg-black/40"></div>
    <div
      use:melt={$wsContent}
      class="fixed left-1/2 top-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-[min(28rem,92vw)] max-h-[80vh] overflow-y-auto rounded-md border border-border bg-bg-elev p-4 shadow-xl"
    >
      <h2 use:melt={$wsTitle} class="text-[0.95rem] font-semibold text-fg">Manage workspaces</h2>
      <p class="mt-1 text-[0.78rem] text-fg-faint">
        Each workspace has its own chat history. The optional directory will power knowledge-search and per-workspace agents in later releases.
      </p>

      <div class="mt-3 flex flex-col gap-2">
        {#each $settings.workspaces as w (w.id)}
          <div class="border border-border rounded-md px-2.5 py-2 bg-bg">
            <div class="flex items-center gap-2">
              {#if editingWorkspaceId === w.id}
                <input
                  class="flex-1 px-1.5 py-1 border border-border-strong rounded bg-bg text-fg text-[0.85rem] outline-none"
                  bind:value={editingWorkspaceName}
                  on:blur={commitWsRename}
                  on:keydown={onWsRenameKey}
                  use:focus
                />
              {:else}
                <span class="flex-1 text-[0.88rem] font-medium overflow-hidden text-ellipsis whitespace-nowrap">{w.name}</span>
                {#if w.id === $settings.activeWorkspaceId}
                  <span class="text-[0.65rem] uppercase tracking-wider text-fg-faint">active</span>
                {/if}
              {/if}
              <button
                class="text-[0.72rem] px-1.5 py-0.5 rounded border border-border bg-bg text-fg-muted cursor-pointer hover:bg-hover-bg hover:text-fg"
                on:click={() => startWsRename(w)}
                title="Rename"
              >rename</button>
              {#if w.id !== DEFAULT_WORKSPACE_ID}
                <button
                  class="text-[0.72rem] px-1.5 py-0.5 rounded border border-error-border bg-error-bg text-error-fg cursor-pointer hover:opacity-90"
                  on:click={() => removeWorkspace(w.id)}
                  title="Delete workspace and its chats"
                >delete</button>
              {/if}
            </div>
            <div class="mt-1.5 flex items-center gap-2 text-[0.75rem]">
              <span class="text-fg-faint shrink-0">Folder:</span>
              <span class="flex-1 text-fg-muted overflow-hidden text-ellipsis whitespace-nowrap" title={w.dir ?? ''}>
                {w.dir ?? '(none)'}
              </span>
              <button
                class="px-1.5 py-0.5 rounded border border-border bg-bg text-fg-muted cursor-pointer hover:bg-hover-bg hover:text-fg"
                on:click={() => pickDir(w.id)}
              >choose…</button>
              {#if w.dir}
                <button
                  class="px-1.5 py-0.5 rounded border border-border bg-bg text-fg-muted cursor-pointer hover:bg-hover-bg hover:text-fg"
                  on:click={() => clearDir(w.id)}
                >clear</button>
              {/if}
            </div>
          </div>
        {/each}
      </div>

      <div class="mt-4 border-t border-border pt-3 flex flex-col gap-2">
        <select
          class="px-2 py-1 border border-border rounded bg-bg text-fg text-[0.85rem] outline-none focus:border-border-strong cursor-pointer"
          value={newWorkspaceTemplateId}
          on:change={onTemplateChange}
        >
          <option value="">Empty workspace</option>
          {#each WORKSPACE_TEMPLATES as t (t.id)}
            <option value={t.id}>{t.name} — {t.description}</option>
          {/each}
        </select>
        <div class="flex items-center gap-2">
          <input
            class="flex-1 px-2 py-1 border border-border rounded bg-bg text-fg text-[0.85rem] outline-none focus:border-border-strong"
            type="text"
            placeholder="New workspace name"
            bind:value={newWorkspaceName}
            on:keydown={(e) => e.key === 'Enter' && createWorkspace()}
          />
          <button
            class="px-3 py-1 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg disabled:opacity-40 disabled:cursor-not-allowed"
            on:click={createWorkspace}
            disabled={!newWorkspaceName.trim() && !newWorkspaceTemplateId}
          >Create</button>
        </div>
      </div>

      <div class="mt-4 flex justify-end">
        <button
          use:melt={$wsClose}
          class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
        >Close</button>
      </div>
    </div>
  {/if}
</div>

<style>
  /* Workspace Select styling — mirrors SettingsPanel.svelte's
     .select-trigger / .select-menu / .select-item so the picker
     looks identical to the Provider/Model selects in the right
     pane. Duplicated rather than hoisted to a global stylesheet
     to keep components self-contained. */
  .select-trigger {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.35rem 0.5rem;
    font-size: 0.85rem;
    outline: none;
    font-family: inherit;
    text-align: left;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    cursor: pointer;
    width: 100%;
  }
  .select-trigger:disabled { opacity: 0.5; cursor: not-allowed; }

  .select-menu {
    z-index: 50;
    background: var(--bg-elev);
    border: 1px solid var(--border);
    border-radius: 4px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.12);
    padding: 0.25rem 0;
    max-height: 15rem;
    overflow-y: auto;
    outline: none;
  }
  .select-item {
    padding: 0.4rem 0.7rem;
    font-size: 0.85rem;
    color: var(--fg);
    cursor: pointer;
  }
  .select-item:hover { background: var(--hover-bg); }
  .select-item :global(*) { pointer-events: none; }
</style>
