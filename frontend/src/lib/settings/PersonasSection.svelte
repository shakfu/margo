<script lang="ts">
  // Personas section of the Roles tab. Workspace-mode only — global
  // mode hides the whole tab. Owns its own create/edit dialog so the
  // parent settings shell doesn't carry persona state.
  import {
    settings, activeWorkspace,
    upsertPersona, deletePersona, duplicatePersona,
    setWorkspaceDefaultPersona,
    type Persona, type Workspace,
  } from '../store';
  import { createCollapsible, createDialog, melt } from '@melt-ui/svelte';
  import { get } from 'svelte/store';

  const { elements: { root: persRoot, trigger: persTrig, content: persContent }, states: { open: persOpen } } =
    createCollapsible({ defaultOpen: false });

  // Edit dialog (also used for Create and Edit-after-Duplicate).
  const {
    elements: { overlay: persDlgOverlay, content: persDlgContent, title: persDlgTitle, close: persDlgClose, portalled: persDlgPortalled },
    states: { open: persDlgOpen },
  } = createDialog({ role: 'dialog' });

  let editing: Persona | null = null;

  function openCreate() {
    editing = {
      id: crypto.randomUUID(),
      name: '',
      description: '',
      systemPrompt: '',
      builtin: false,
      // Default new entries to the active workspace; the user can flip
      // Scope to "Global" if they want it visible everywhere.
      workspaceId: $settings.activeWorkspaceId,
    };
    persDlgOpen.set(true);
  }
  function openEdit(p: Persona) {
    if (p.builtin) return;
    editing = { ...p };
    persDlgOpen.set(true);
  }
  function openDuplicate(p: Persona) {
    const newId = duplicatePersona(p.id);
    if (!newId) return;
    const fresh = get(settings).personas.find(x => x.id === newId);
    if (fresh) openEdit(fresh);
  }
  function commitEdit() {
    if (!editing) return;
    if (!editing.name.trim() || !editing.systemPrompt.trim()) return;
    upsertPersona(editing);
    editing = null;
    persDlgOpen.set(false);
  }
  function cancelEdit() {
    editing = null;
    persDlgOpen.set(false);
  }
  function onScopeChange(ev: Event) {
    if (!editing) return;
    const v = (ev.currentTarget as HTMLSelectElement).value;
    editing = { ...editing, workspaceId: v || undefined };
  }
  // scopeLabel renders the human name for a persona's workspace scope.
  function scopeLabel(workspaceId: string | undefined, workspaces: Workspace[]): string {
    if (!workspaceId) return 'Global';
    const ws = workspaces.find(w => w.id === workspaceId);
    return ws?.name ?? `(missing: ${workspaceId})`;
  }

  // Hoisted out of the template: Svelte's {@const} must be the
  // immediate child of a block (#if / #each / …) and the top-level
  // <ul> isn't one.
  $: noDefault = !$activeWorkspace?.defaultPersonaId;
</script>

<section class="border-b border-border" use:melt={$persRoot}>
  <button class="section-head" use:melt={$persTrig}>
    <span class="caret">{$persOpen ? '▾' : '▸'}</span>
    <span>Personas</span>
    <span class="ml-1 text-fg-faint text-[0.72rem]">({$settings.personas.length})</span>
  </button>
  <div use:melt={$persContent} class="section-body">
    <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
      Pick the default for new chats in this workspace. Existing chats are unaffected; use <code class="font-[family-name:var(--font-mono)] text-[0.72rem]">/persona &lt;name&gt;</code> in any chat to override per-conversation, or <code class="font-[family-name:var(--font-mono)] text-[0.72rem]">/persona</code> with no argument to revert that chat to the default voice.
    </p>
    <ul class="flex flex-col gap-1 mb-2">
      <!-- Explicit "Default" row: built-in assistant voice. Clicking
           it clears the workspace's defaultPersonaId. -->
      <li class="flex items-start gap-2 text-[0.78rem] {noDefault ? 'bg-bubble-user/40 border-border-strong' : 'bg-input-bg border-border'} border rounded px-2 py-1.5">
        <div class="flex-1 min-w-0">
          <div class="font-semibold text-fg flex items-center gap-1 flex-wrap">
            <span>Default</span>
            {#if noDefault}<span class="text-fg-muted text-[0.62rem] uppercase tracking-wider">active</span>{/if}
            <span class="text-fg-faint text-[0.62rem] uppercase tracking-wider">builtin</span>
          </div>
          <div class="text-fg-faint text-[0.7rem] leading-snug mt-0.5">
            No persona — the built-in 'assistant' voice. New chats start in this mode when no other persona is the workspace default.
          </div>
        </div>
        <div class="flex flex-col gap-1 shrink-0">
          {#if !noDefault}
            <button class="mini-btn" on:click={() => setWorkspaceDefaultPersona($settings.activeWorkspaceId, undefined)} title="New chats will start with no persona">Set default</button>
          {/if}
        </div>
      </li>
      {#each $settings.personas as p (p.id)}
        {@const isDefault = $activeWorkspace?.defaultPersonaId === p.id}
        <li class="flex items-start gap-2 text-[0.78rem] {isDefault ? 'bg-bubble-user/40 border-border-strong' : 'bg-input-bg border-border'} border rounded px-2 py-1.5">
          <div class="flex-1 min-w-0">
            <div class="font-semibold text-fg flex items-center gap-1 flex-wrap">
              <span>{p.name}</span>
              {#if isDefault}<span class="text-fg-muted text-[0.62rem] uppercase tracking-wider">active</span>{/if}
              {#if p.builtin}<span class="text-fg-faint text-[0.62rem] uppercase tracking-wider">builtin</span>{/if}
              <span class="text-fg-faint text-[0.62rem] uppercase tracking-wider">{scopeLabel(p.workspaceId, $settings.workspaces)}</span>
            </div>
            {#if p.description}
              <div class="text-fg-faint text-[0.7rem] leading-snug mt-0.5">{p.description}</div>
            {/if}
          </div>
          <div class="flex flex-col gap-1 shrink-0">
            {#if !isDefault}
              <button class="mini-btn" on:click={() => setWorkspaceDefaultPersona($settings.activeWorkspaceId, p.id)} title="New chats in this workspace will start with this persona">Set default</button>
            {/if}
            {#if p.builtin}
              <button class="mini-btn" on:click={() => openDuplicate(p)} title="Duplicate; the copy is editable">Duplicate</button>
            {:else}
              <button class="mini-btn" on:click={() => openEdit(p)}>Edit</button>
              <button class="mini-btn" on:click={() => deletePersona(p.id)}>Delete</button>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
    <button class="mini-btn" on:click={openCreate}>+ New persona</button>
  </div>
</section>

<!-- Edit dialog (portalled). Rendered here rather than in the parent so
     PersonasSection stays self-contained; Melt's portalled wrapper
     escapes the local DOM so z-index works correctly. -->
<div use:melt={$persDlgPortalled}>
  {#if $persDlgOpen && editing}
    <div use:melt={$persDlgOverlay} class="fixed inset-0 z-40 bg-black/40"></div>
    <div
      use:melt={$persDlgContent}
      class="fixed left-1/2 top-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-[min(32rem,92vw)] rounded-md border border-border bg-bg-elev p-4 shadow-xl"
    >
      <h2 use:melt={$persDlgTitle} class="text-[0.95rem] font-semibold text-fg">
        {$settings.personas.some(p => p.id === editing?.id) ? 'Edit persona' : 'New persona'}
      </h2>
      <div class="mt-3 flex flex-col gap-2.5">
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Name</span>
          <input type="text" class="text-input" bind:value={editing.name} placeholder="e.g. Concise Reviewer" />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Description (optional)</span>
          <input type="text" class="text-input" bind:value={editing.description} placeholder="Shown as the picker subtitle" />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>System prompt</span>
          <textarea class="text-input" rows="8" bind:value={editing.systemPrompt} placeholder="What this role does. The text here replaces the global system prompt when this persona is active."></textarea>
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Scope</span>
          <select class="text-input" value={editing.workspaceId ?? ''} on:change={onScopeChange}>
            <option value="">Global (visible in all workspaces)</option>
            {#each $settings.workspaces as w (w.id)}
              <option value={w.id}>{w.name}</option>
            {/each}
          </select>
        </label>
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <button use:melt={$persDlgClose} class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg" on:click={cancelEdit}>Cancel</button>
        <button class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg font-semibold" on:click={commitEdit} disabled={!editing.name.trim() || !editing.systemPrompt.trim()}>Save</button>
      </div>
    </div>
  {/if}
</div>
