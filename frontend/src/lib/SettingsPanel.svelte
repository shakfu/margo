<script lang="ts">
  import {
    settings,
    effectiveSettings,
    setEffectiveOverride,
    upsertPersona, deletePersona, duplicatePersona,
    upsertAgent, deleteAgent, duplicateAgent, agentMissingTools,
    DEFAULT_WORKSPACE_ID, activeWorkspace,
    type Persona, type Agent, type Workspace, type WorkspaceOverrides,
  } from './store';
  import { setHighlightTheme } from './markdown';
  import { createSelect, createCollapsible, createDialog, createTabs, melt } from '@melt-ui/svelte';
  import { OpenPath } from '../../wailsjs/go/main/App.js';
  import { get } from 'svelte/store';

  export let providers: string[] = [];
  export let models: string[] = [];
  export let busy: boolean = false;
  export let outputDir: string = '';
  export let availableTools: string[] = [];
  export let onReset: () => void = () => {};
  // mode controls which scope this panel reads/writes:
  //   - 'workspace' (right sidebar): inputs read from effectiveSettings
  //     and write via setEffectiveOverride, which routes by active
  //     workspace — Default → in-memory sessionOverrides (transient);
  //     non-Default → workspace.overrides (sticky).
  //   - 'global' (Cmd+, dialog): inputs read/write the raw global
  //     `settings` store. These are the defaults that seed new
  //     workspaces and the Default workspace's baseline.
  // Sections are also gated by mode (Personas/Agents only in workspace;
  // Trusted tools / Theme / Output / Reset only in global).
  export let mode: 'workspace' | 'global' = 'global';

  // Reactive view of the values controls should display. In workspace
  // mode, this resolves overrides on top of globals. In global mode,
  // it's the raw global store.
  $: display = mode === 'workspace' ? $effectiveSettings : $settings;

  // writeKey routes a write to the appropriate scope based on mode.
  // Used by every input that previously did `bind:value={$settings.X}`.
  function writeKey<K extends keyof WorkspaceOverrides>(key: K, value: WorkspaceOverrides[K]) {
    if (mode === 'workspace') {
      setEffectiveOverride(key, value);
    } else {
      settings.update(s => ({ ...s, [key]: value }));
    }
  }

  function openOutputDir() {
    if (outputDir) OpenPath(outputDir);
  }

  // Provider select. Reads / writes route through `display` /
  // writeKey so the same component works in both modes.
  const initialProvider = mode === 'workspace' ? get(effectiveSettings).provider : get(settings).provider;
  const {
    elements: { trigger: provSelTrig, menu: provSelMenu, option: provSelOpt },
    states: { selectedLabel: provLabel, open: provOpen, selected: provSelected },
    helpers: { isSelected: isProvSelected }
  } = createSelect<string>({
    positioning: { placement: 'bottom', sameWidth: true },
    defaultSelected: initialProvider
      ? { value: initialProvider, label: initialProvider }
      : undefined
  });

  // User picks a provider -> propagate to the right scope. Changing
  // provider also invalidates model, since model lists are
  // per-provider; we clear it and let the model auto-pick reset
  // pick a fresh default.
  provSelected.subscribe(s => {
    if (!s) return;
    const cur = mode === 'workspace' ? get(effectiveSettings).provider : get(settings).provider;
    if (s.value === cur) return;
    writeKey('provider', s.value);
    writeKey('model', '');
  });
  // Inverse sync: external changes (e.g. workspace switch in
  // workspace mode, or programmatic update in global mode) update
  // the picker label.
  const provSource = mode === 'workspace' ? effectiveSettings : settings;
  provSource.subscribe(s => {
    const cur = get(provSelected);
    if (s.provider && (!cur || cur.value !== s.provider)) {
      provSelected.set({ value: s.provider, label: s.provider });
    }
  });

  // Model select. Same dual-mode pattern as Provider.
  const initialModel = mode === 'workspace' ? get(effectiveSettings).model : get(settings).model;
  const {
    elements: { trigger: modSelTrig, menu: modSelMenu, option: modSelOpt },
    states: { selectedLabel: modLabel, open: modOpen, selected: modSelected },
    helpers: { isSelected: isModSelected }
  } = createSelect<string>({
    positioning: { placement: 'bottom', sameWidth: true },
    defaultSelected: initialModel
      ? { value: initialModel, label: initialModel }
      : undefined
  });

  modSelected.subscribe(s => {
    if (!s) return;
    const cur = mode === 'workspace' ? get(effectiveSettings).model : get(settings).model;
    if (s.value === cur) return;
    writeKey('model', s.value);
  });
  const modSource = mode === 'workspace' ? effectiveSettings : settings;
  modSource.subscribe(s => {
    const cur = get(modSelected);
    if (s.model && (!cur || cur.value !== s.model)) {
      modSelected.set({ value: s.model, label: s.model });
    }
  });

  // When models prop arrives, ensure the effective model is still in
  // the allowlist; otherwise reset to the provider's default (first
  // entry). Routes via writeKey so the auto-reset writes to the right
  // scope.
  $: if (models.length > 0 && display && !models.includes(display.model)) {
    writeKey('model', models[0]);
  }

  // Sections
  const mkSection = (open: boolean) => createCollapsible({ defaultOpen: open });

  const {
    elements: { root: provRoot, trigger: provTrig, content: provContent },
    states: { open: provSectOpen }
  } = mkSection(true);

  const {
    elements: { root: modRoot, trigger: modTrig, content: modContent },
    states: { open: modSectOpen }
  } = mkSection(true);

  const {
    elements: { root: sampRoot, trigger: sampTrig, content: sampContent },
    states: { open: sampOpen }
  } = mkSection(false);

  const {
    elements: { root: thinkRoot, trigger: thinkTrig, content: thinkContent },
    states: { open: thinkSectOpen }
  } = mkSection(false);

  const {
    elements: { root: sysRoot, trigger: sysTrig, content: sysContent },
    states: { open: sysOpen }
  } = mkSection(false);

  const {
    elements: { root: apprRoot, trigger: apprTrig, content: apprContent },
    states: { open: apprOpen }
  } = mkSection(false);

  const {
    elements: { root: outRoot, trigger: outTrig, content: outContent },
    states: { open: outOpen }
  } = mkSection(false);

  const {
    elements: { root: trustRoot, trigger: trustTrig, content: trustContent },
    states: { open: trustOpen }
  } = mkSection(false);

  const {
    elements: { root: resetRoot, trigger: resetTrig, content: resetContent },
    states: { open: resetOpen }
  } = mkSection(false);

  function revokeTool(name: string) {
    settings.update(s => ({
      ...s,
      autoApproveTools: (s.autoApproveTools ?? []).filter(t => t !== name),
    }));
  }
  function revokeAllTools() {
    settings.update(s => ({ ...s, autoApproveTools: [] }));
  }

  function onPersonaScopeChange(ev: Event) {
    if (!editing) return;
    const v = (ev.currentTarget as HTMLSelectElement).value;
    editing = { ...editing, workspaceId: v || undefined };
  }
  function onAgentScopeChange(ev: Event) {
    if (!editingAgent) return;
    const v = (ev.currentTarget as HTMLSelectElement).value;
    editingAgent = { ...editingAgent, workspaceId: v || undefined };
  }

  // scopeLabel renders the human name of a persona/agent's workspace
  // scope ("Global" for unscoped). Used in the list and the editor's
  // Scope selector. (7.1.b)
  function scopeLabel(workspaceId: string | undefined, workspaces: Workspace[]): string {
    if (!workspaceId) return 'Global';
    const ws = workspaces.find(w => w.id === workspaceId);
    return ws?.name ?? `(missing: ${workspaceId})`;
  }

  // Persona management
  const {
    elements: { root: persRoot, trigger: persTrig, content: persContent },
    states: { open: persOpen }
  } = mkSection(false);

  // Edit dialog reused for both Create and Edit (and the Edit-after-
  // Duplicate flow). `editing` carries the working copy; null when the
  // dialog is closed.
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
      // Default new entries to the active workspace. If the user is in
      // "Default" they probably want a global persona; offer both via
      // the Scope selector. Picking "current workspace" is the more
      // useful default once the user has a non-Default workspace.
      workspaceId: $settings.activeWorkspaceId,
    };
    persDlgOpen.set(true);
  }
  function openEdit(p: Persona) {
    if (p.builtin) return; // Edit on a builtin should have been intercepted in the UI.
    editing = { ...p };
    persDlgOpen.set(true);
  }
  function openDuplicate(p: Persona) {
    const newId = duplicatePersona(p.id);
    if (!newId) return;
    // Open the freshly-duplicated entry in edit mode so the user can
    // immediately rename / tweak. Reading from the store guarantees we
    // see the same object the list will render.
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

  // Agent management — same shape as personas but with a tool allowlist.
  const {
    elements: { root: agtsRoot, trigger: agtsTrig, content: agtsContent },
    states: { open: agtsOpen }
  } = mkSection(false);

  const {
    elements: { overlay: agtDlgOverlay, content: agtDlgContent, title: agtDlgTitle, close: agtDlgClose, portalled: agtDlgPortalled },
    states: { open: agtDlgOpen },
  } = createDialog({ role: 'dialog' });

  let editingAgent: Agent | null = null;

  function openCreateAgent() {
    editingAgent = {
      id: crypto.randomUUID(),
      name: '',
      description: '',
      systemPrompt: '',
      tools: [],
      builtin: false,
      workspaceId: $settings.activeWorkspaceId,
    };
    agtDlgOpen.set(true);
  }
  function openEditAgent(a: Agent) {
    if (a.builtin) return;
    editingAgent = { ...a, tools: [...a.tools] };
    agtDlgOpen.set(true);
  }
  function openDuplicateAgent(a: Agent) {
    const newId = duplicateAgent(a.id);
    if (!newId) return;
    const fresh = get(settings).agents.find(x => x.id === newId);
    if (fresh) openEditAgent(fresh);
  }
  function toggleAgentTool(name: string) {
    if (!editingAgent) return;
    const has = editingAgent.tools.includes(name);
    editingAgent = {
      ...editingAgent,
      tools: has ? editingAgent.tools.filter(t => t !== name) : [...editingAgent.tools, name],
    };
  }
  function commitAgent() {
    if (!editingAgent) return;
    if (!editingAgent.name.trim() || !editingAgent.systemPrompt.trim() || editingAgent.tools.length === 0) return;
    upsertAgent(editingAgent);
    editingAgent = null;
    agtDlgOpen.set(false);
  }
  function cancelAgent() {
    editingAgent = null;
    agtDlgOpen.set(false);
  }

  // Top-level tabs grouping the sections by what they actually affect.
  // "models"  — provider, model, sampling, thinking (model selection + params).
  // "agents"  — agent / tool-related settings (trusted tools today).
  // "general" — everything else: system prompt, appearance, output, reset.
  const {
    elements: { root: tabsRoot, list: tabsList, trigger: tabsTrigger, content: tabsContent },
  } = createTabs({ defaultValue: 'models' });

  // Reset confirm dialog
  const {
    elements: { overlay: resetDlgOverlay, content: resetDlgContent, title: resetDlgTitle, close: resetDlgClose, portalled: resetDlgPortalled },
    states: { open: resetDlgOpen },
  } = createDialog({ role: 'alertdialog' });

  function confirmReset() {
    resetDlgOpen.set(false);
    onReset();
  }

  function toggleTheme() {
    const next: 'light' | 'dark' = $settings.theme === 'light' ? 'dark' : 'light';
    settings.update(s => ({ ...s, theme: next }));
    document.documentElement.classList.toggle('dark', next === 'dark');
    setHighlightTheme(next);
  }

  // Stop sequences edited as comma-separated text. Reads from the
  // effective scope so the field reflects workspace overrides; writes
  // route via writeKey. Initialised empty so this top-level `let`
  // doesn't reference `display` (which is `$:`-bound and undefined
  // until the first reactive run); the reactive block below seeds
  // the actual value before first paint.
  let stopText = '';
  $: stopText = (display?.stopSequences ?? []).join(', ');
  function commitStopSequences() {
    const arr = stopText
      .split(',')
      .map(s => s.trim())
      .filter(s => s.length > 0);
    writeKey('stopSequences', arr);
  }
</script>

<div class="flex flex-col h-full bg-bg-elev border-l border-border overflow-y-auto" use:melt={$tabsRoot}>
  <div class="px-3.5 pt-3.5 pb-2 font-semibold text-[0.9rem]">Settings</div>
  <div use:melt={$tabsList} class="flex border-b border-border px-2 gap-0.5" aria-label="Settings tabs">
    <button class="tab-trigger" use:melt={$tabsTrigger('models')}>Models</button>
    <button class="tab-trigger" use:melt={$tabsTrigger('agents')}>Agents</button>
    {#if mode === 'global'}
      <!-- General tab carries Appearance / Output / Reset, all
           global-only. Hidden in workspace mode (right sidebar)
           since the system prompt moved to the Models tab and
           nothing else workspace-scoped lives here. -->
      <button class="tab-trigger" use:melt={$tabsTrigger('general')}>General</button>
    {/if}
  </div>

  <!-- Models tab -->
  <div use:melt={$tabsContent('models')}>

  <!-- Provider -->
  <section class="border-b border-border" use:melt={$provRoot}>
    <button class="section-head" use:melt={$provTrig}>
      <span class="caret">{$provSectOpen ? '▾' : '▸'}</span>
      <span>Provider</span>
    </button>
    <div use:melt={$provContent} class="section-body">
      <button
        class="select-trigger"
        disabled={busy || providers.length === 0}
        use:melt={$provSelTrig}
      >
        <span>{$provLabel || 'select provider'}</span>
        <span class="text-fg-faint text-[0.7rem]">{$provOpen ? '▴' : '▾'}</span>
      </button>
      {#if $provOpen}
        <div class="select-menu" use:melt={$provSelMenu}>
          {#each providers as p}
            <div
              class="select-item {$isProvSelected(p) ? 'bg-accent' : ''}"
              use:melt={$provSelOpt({ value: p, label: p })}
            >{p}</div>
          {/each}
        </div>
      {/if}
      {#if mode === 'global'}
        <!-- "streaming" isn't a workspace-overridable key (it's a
             global runtime preference), so the toggle only appears
             in the global Settings dialog. -->
        <label class="flex flex-row items-center gap-2 text-[0.8rem] text-fg-muted">
          <input type="checkbox" bind:checked={$settings.streaming} disabled={busy} class="m-0" />
          <span>Stream tokens</span>
        </label>
      {/if}
    </div>
  </section>

  <!-- Model -->
  <section class="border-b border-border" use:melt={$modRoot}>
    <button class="section-head" use:melt={$modTrig}>
      <span class="caret">{$modSectOpen ? '▾' : '▸'}</span>
      <span>Model</span>
    </button>
    <div use:melt={$modContent} class="section-body">
      <button
        class="select-trigger"
        disabled={busy || models.length === 0}
        use:melt={$modSelTrig}
      >
        <span>{$modLabel || 'select model'}</span>
        <span class="text-fg-faint text-[0.7rem]">{$modOpen ? '▴' : '▾'}</span>
      </button>
      {#if $modOpen}
        <div class="select-menu" use:melt={$modSelMenu}>
          {#each models as m}
            <div
              class="select-item {$isModSelected(m) ? 'bg-accent' : ''}"
              use:melt={$modSelOpt({ value: m, label: m })}
            >{m}</div>
          {/each}
        </div>
      {/if}
    </div>
  </section>

  <!-- System Prompt — sits under Model so the model + its system
       prompt are configured together. Applies to every message in
       chats using this scope. -->
  <section class="border-b border-border" use:melt={$sysRoot}>
    <button class="section-head" use:melt={$sysTrig}>
      <span class="caret">{$sysOpen ? '▾' : '▸'}</span>
      <span>System Prompt</span>
    </button>
    <div use:melt={$sysContent} class="section-body">
      <textarea
        class="text-input"
        value={display.system}
        on:input={(e) => writeKey('system', e.currentTarget.value)}
        disabled={busy}
        rows="6"
        placeholder="Optional system prompt. Applies to all messages in the active chat."
      ></textarea>
    </div>
  </section>

  <!-- Sampling -->
  <section class="border-b border-border" use:melt={$sampRoot}>
    <button class="section-head" use:melt={$sampTrig}>
      <span class="caret">{$sampOpen ? '▾' : '▸'}</span>
      <span>Sampling</span>
    </button>
    <div use:melt={$sampContent} class="section-body">
      <label class="field">
        <div class="flex justify-between">
          <span>Temperature</span>
          <span class="text-fg-faint">{display.temperature ?? 'default'}</span>
        </div>
        <div class="flex items-center gap-2">
          <input
            type="range"
            min="0" max="2" step="0.05"
            value={display.temperature ?? 1}
            on:input={(e) => writeKey('temperature', parseFloat(e.currentTarget.value))}
            disabled={busy}
            class="flex-1"
          />
          <button
            class="mini-btn"
            on:click={() => writeKey('temperature', null)}
            disabled={busy}
            title="Use provider default"
          >reset</button>
        </div>
      </label>

      <label class="field">
        <div class="flex justify-between">
          <span>Top-p</span>
          <span class="text-fg-faint">{display.topP ?? 'default'}</span>
        </div>
        <div class="flex items-center gap-2">
          <input
            type="range"
            min="0" max="1" step="0.01"
            value={display.topP ?? 1}
            on:input={(e) => writeKey('topP', parseFloat(e.currentTarget.value))}
            disabled={busy}
            class="flex-1"
          />
          <button
            class="mini-btn"
            on:click={() => writeKey('topP', null)}
            disabled={busy}
          >reset</button>
        </div>
      </label>

      <label class="field">
        <span>Max tokens</span>
        <input
          type="number"
          class="text-input"
          min="1" step="1"
          value={display.maxTokens}
          on:input={(e) => writeKey('maxTokens', parseInt(e.currentTarget.value, 10) || 0)}
          disabled={busy}
        />
      </label>

      <label class="field">
        <span>Stop sequences (comma-separated)</span>
        <input
          type="text"
          class="text-input"
          bind:value={stopText}
          on:blur={commitStopSequences}
          disabled={busy}
        />
      </label>
    </div>
  </section>

  <!-- Thinking -->
  <section class="border-b border-border" use:melt={$thinkRoot}>
    <button class="section-head" use:melt={$thinkTrig}>
      <span class="caret">{$thinkSectOpen ? '▾' : '▸'}</span>
      <span>Thinking</span>
      {#if display.thinkEnabled}
        <span class="ml-auto text-[0.65rem] text-fg-faint uppercase tracking-wider">on</span>
      {/if}
    </button>
    <div use:melt={$thinkContent} class="section-body">
      <label class="flex flex-row items-center gap-2 text-[0.8rem] text-fg-muted">
        <input
          type="checkbox"
          checked={display.thinkEnabled}
          on:change={(e) => writeKey('thinkEnabled', e.currentTarget.checked)}
          disabled={busy}
          class="m-0"
        />
        <span>Enable extended thinking (Anthropic)</span>
      </label>
      <label class="field">
        <span>Budget tokens (min 1024)</span>
        <input
          type="number"
          class="text-input"
          min="1024" step="256"
          value={display.thinkBudget}
          on:input={(e) => writeKey('thinkBudget', parseInt(e.currentTarget.value, 10) || 1024)}
          disabled={busy || !display.thinkEnabled}
        />
      </label>
      <p class="text-[0.7rem] text-fg-faint leading-snug">
        Thinking counts toward max_tokens and is only supported by Claude 3.7+ / 4.x. OpenAI ignores this setting.
      </p>
    </div>
  </section>

  </div>

  <!-- Agents tab -->
  <div use:melt={$tabsContent('agents')}>

  {#if mode === 'workspace'}
  <!-- Personas (workspace-scoped via Persona.workspaceId; Settings
       lists merge globals + active workspace's). Hidden in the global
       Settings dialog because they're per-workspace state. -->
  <section class="border-b border-border" use:melt={$persRoot}>
    <button class="section-head" use:melt={$persTrig}>
      <span class="caret">{$persOpen ? '▾' : '▸'}</span>
      <span>Personas</span>
      <span class="ml-1 text-fg-faint text-[0.72rem]">({$settings.personas.length})</span>
    </button>
    <div use:melt={$persContent} class="section-body">
      <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
        Tool-less roles. Pick one in the chat header to swap the system prompt.
      </p>
      <ul class="flex flex-col gap-1 mb-2">
        {#each $settings.personas as p (p.id)}
          <li class="flex items-start gap-2 text-[0.78rem] bg-input-bg border border-border rounded px-2 py-1.5">
            <div class="flex-1 min-w-0">
              <div class="font-semibold text-fg flex items-center gap-1">
                <span>{p.name}</span>
                {#if p.builtin}<span class="text-fg-faint text-[0.65rem] uppercase tracking-wider">builtin</span>{/if}
                <span class="text-fg-faint text-[0.65rem] uppercase tracking-wider">{scopeLabel(p.workspaceId, $settings.workspaces)}</span>
              </div>
              {#if p.description}
                <div class="text-fg-faint text-[0.7rem] leading-snug mt-0.5">{p.description}</div>
              {/if}
            </div>
            <div class="flex flex-col gap-1 shrink-0">
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

  <!-- Agents (workspace-scoped; same gating as Personas). -->
  <section class="border-b border-border" use:melt={$agtsRoot}>
    <button class="section-head" use:melt={$agtsTrig}>
      <span class="caret">{$agtsOpen ? '▾' : '▸'}</span>
      <span>Agents</span>
      <span class="ml-1 text-fg-faint text-[0.72rem]">({$settings.agents.length})</span>
    </button>
    <div use:melt={$agtsContent} class="section-body">
      <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
        Personas with a tool allowlist. Picking one in the chat header runs the next message through the agent loop with only those tools available.
      </p>
      <ul class="flex flex-col gap-1 mb-2">
        {#each $settings.agents as a (a.id)}
          {@const missing = agentMissingTools(a, availableTools)}
          <li class="flex items-start gap-2 text-[0.78rem] bg-input-bg border border-border rounded px-2 py-1.5">
            <div class="flex-1 min-w-0">
              <div class="font-semibold text-fg flex items-center gap-1 flex-wrap">
                <span>{a.name}</span>
                <span class="text-fg-faint text-[0.65rem] font-[family-name:var(--font-mono)]">[{a.tools.length}]</span>
                {#if a.builtin}<span class="text-fg-faint text-[0.65rem] uppercase tracking-wider">builtin</span>{/if}
                <span class="text-fg-faint text-[0.65rem] uppercase tracking-wider">{scopeLabel(a.workspaceId, $settings.workspaces)}</span>
                {#if missing.length > 0}
                  <span class="text-error-fg text-[0.65rem] uppercase tracking-wider">needs {missing.join(', ')}</span>
                {/if}
              </div>
              {#if a.description}
                <div class="text-fg-faint text-[0.7rem] leading-snug mt-0.5">{a.description}</div>
              {/if}
              <div class="text-fg-faint text-[0.7rem] font-[family-name:var(--font-mono)] mt-0.5">
                tools: {a.tools.join(', ')}
              </div>
            </div>
            <div class="flex flex-col gap-1 shrink-0">
              {#if a.builtin}
                <button class="mini-btn" on:click={() => openDuplicateAgent(a)}>Duplicate</button>
              {:else}
                <button class="mini-btn" on:click={() => openEditAgent(a)}>Edit</button>
                <button class="mini-btn" on:click={() => deleteAgent(a.id)}>Delete</button>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
      <button class="mini-btn" on:click={openCreateAgent} disabled={availableTools.length === 0}>+ New agent</button>
      {#if availableTools.length === 0}
        <div class="text-[0.7rem] text-fg-faint mt-1">No tools registered. New agents need at least one tool.</div>
      {/if}
    </div>
  </section>
  {/if}

  {#if mode === 'global'}
  <!-- Trusted tools — global user-trust state; not per-workspace.
       Lives in the global Settings dialog only. -->
  <section class="border-b border-border" use:melt={$trustRoot}>
    <button class="section-head" use:melt={$trustTrig}>
      <span class="caret">{$trustOpen ? '▾' : '▸'}</span>
      <span>Trusted tools</span>
      {#if ($settings.autoApproveTools ?? []).length > 0}
        <span class="ml-1 text-fg-faint text-[0.72rem]">({$settings.autoApproveTools.length})</span>
      {/if}
    </button>
    <div use:melt={$trustContent} class="section-body">
      <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
        Tools you've clicked <em>Always</em> on. These run without a permission prompt.
      </p>
      {#if ($settings.autoApproveTools ?? []).length === 0}
        <div class="text-[0.78rem] text-fg-faint italic">No trusted tools yet.</div>
      {:else}
        <ul class="flex flex-col gap-1 mb-2">
          {#each $settings.autoApproveTools as name (name)}
            <li class="flex items-center gap-2 text-[0.78rem] font-[family-name:var(--font-mono)] bg-input-bg border border-border rounded px-2 py-1">
              <span class="flex-1 break-all">{name}</span>
              <button
                class="mini-btn"
                title="Revoke; future calls will prompt again"
                on:click={() => revokeTool(name)}
              >Revoke</button>
            </li>
          {/each}
        </ul>
        <button class="mini-btn" on:click={revokeAllTools}>Revoke all</button>
      {/if}
    </div>
  </section>
  {/if}

  </div>

  <!-- General tab — only meaningful in global mode. The
       conditional wrapping the trigger above already prevents the
       tab from being selectable in workspace mode; this gate keeps
       the content out of the DOM for symmetry. -->
  {#if mode === 'global'}
  <div use:melt={$tabsContent('general')}>

  <!-- Appearance / Output / Reset — global user prefs, not
       workspace state. Live in the global Settings dialog only. -->
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
      <button
        class="mini-btn"
        on:click={openOutputDir}
        disabled={!outputDir}
      >Open in Finder</button>
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

  </div>
  {/if}
</div>

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
          <input
            type="text"
            class="text-input"
            bind:value={editing.name}
            placeholder="e.g. Concise Reviewer"
          />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Description (optional)</span>
          <input
            type="text"
            class="text-input"
            bind:value={editing.description}
            placeholder="Shown as the picker subtitle"
          />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>System prompt</span>
          <textarea
            class="text-input"
            rows="8"
            bind:value={editing.systemPrompt}
            placeholder="What this role does. The text here replaces the global system prompt when this persona is active."
          ></textarea>
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Scope</span>
          <select
            class="text-input"
            value={editing.workspaceId ?? ''}
            on:change={onPersonaScopeChange}
          >
            <option value="">Global (visible in all workspaces)</option>
            {#each $settings.workspaces as w (w.id)}
              <option value={w.id}>{w.name}</option>
            {/each}
          </select>
        </label>
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <button
          use:melt={$persDlgClose}
          class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
          on:click={cancelEdit}
        >Cancel</button>
        <button
          class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg font-semibold"
          on:click={commitEdit}
          disabled={!editing.name.trim() || !editing.systemPrompt.trim()}
        >Save</button>
      </div>
    </div>
  {/if}
</div>

<div use:melt={$agtDlgPortalled}>
  {#if $agtDlgOpen && editingAgent}
    <div use:melt={$agtDlgOverlay} class="fixed inset-0 z-40 bg-black/40"></div>
    <div
      use:melt={$agtDlgContent}
      class="fixed left-1/2 top-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-[min(34rem,92vw)] max-h-[90vh] overflow-y-auto rounded-md border border-border bg-bg-elev p-4 shadow-xl"
    >
      <h2 use:melt={$agtDlgTitle} class="text-[0.95rem] font-semibold text-fg">
        {$settings.agents.some(a => a.id === editingAgent?.id) ? 'Edit agent' : 'New agent'}
      </h2>
      <div class="mt-3 flex flex-col gap-2.5">
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Name</span>
          <input type="text" class="text-input" bind:value={editingAgent.name} placeholder="e.g. Quarto Author" />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Description (optional)</span>
          <input type="text" class="text-input" bind:value={editingAgent.description} placeholder="Shown as the picker subtitle" />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>System prompt</span>
          <textarea class="text-input" rows="6" bind:value={editingAgent.systemPrompt}
            placeholder="What this agent does. Mention the tools by name so the model knows when to call them."
          ></textarea>
        </label>
        <div class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Tools (allowlist — at least one required)</span>
          {#if availableTools.length === 0}
            <div class="text-fg-faint italic">No tools available.</div>
          {:else}
            <div class="flex flex-col gap-1 bg-input-bg border border-border rounded px-2 py-1.5">
              {#each availableTools as tool (tool)}
                <label class="flex items-center gap-2 text-[0.78rem] text-fg cursor-pointer">
                  <input
                    type="checkbox"
                    checked={editingAgent.tools.includes(tool)}
                    on:change={() => toggleAgentTool(tool)}
                  />
                  <span class="font-[family-name:var(--font-mono)]">{tool}</span>
                </label>
              {/each}
            </div>
          {/if}
        </div>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Scope</span>
          <select
            class="text-input"
            value={editingAgent.workspaceId ?? ''}
            on:change={onAgentScopeChange}
          >
            <option value="">Global (visible in all workspaces)</option>
            {#each $settings.workspaces as w (w.id)}
              <option value={w.id}>{w.name}</option>
            {/each}
          </select>
        </label>
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <button
          use:melt={$agtDlgClose}
          class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
          on:click={cancelAgent}
        >Cancel</button>
        <button
          class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg font-semibold"
          on:click={commitAgent}
          disabled={!editingAgent.name.trim() || !editingAgent.systemPrompt.trim() || editingAgent.tools.length === 0}
        >Save</button>
      </div>
    </div>
  {/if}
</div>

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
        <button
          use:melt={$resetDlgClose}
          class="px-3 py-1.5 text-[0.85rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
        >Cancel</button>
        <button
          class="px-3 py-1.5 text-[0.85rem] rounded border border-error-border bg-error-bg text-error-fg cursor-pointer hover:opacity-90 font-semibold"
          on:click={confirmReset}
        >Reset</button>
      </div>
    </div>
  {/if}
</div>

<style>
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
    gap: 0.6rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.8rem;
    color: var(--fg-muted);
  }

  .text-input {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.4rem 0.55rem;
    font-family: inherit;
    font-size: 0.85rem;
    outline: none;
    width: 100%;
    box-sizing: border-box;
    resize: vertical;
  }
  .text-input:focus { border-color: var(--border-strong); }
  .text-input:disabled { opacity: 0.5; }

  .select-trigger {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.4rem 0.55rem;
    font-size: 0.85rem;
    outline: none;
    font-family: inherit;
    text-align: left;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    cursor: pointer;
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

  .mini-btn {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.25rem 0.55rem;
    font-size: 0.75rem;
    cursor: pointer;
    font-family: inherit;
  }
  .mini-btn:hover:not(:disabled) { background: var(--hover-bg); }
  .mini-btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .tab-trigger {
    background: transparent;
    border: none;
    border-bottom: 2px solid transparent;
    padding: 0.45rem 0.7rem;
    font-size: 0.82rem;
    color: var(--fg-muted);
    cursor: pointer;
    font-family: inherit;
    margin-bottom: -1px; /* overlap parent's bottom border */
  }
  .tab-trigger:hover { color: var(--fg); }
  .tab-trigger[data-state="active"] {
    color: var(--fg);
    border-bottom-color: var(--fg);
    font-weight: 600;
  }
</style>
