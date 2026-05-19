<script lang="ts">
  // Models tab content: Provider + Model selects, System Prompt,
  // Sampling parameters, Thinking. Extracted from SettingsPanel.svelte
  // so the parent shrinks to a thin tabs shell. Owns its own collapsible
  // section state; reads/writes route through writeKey based on `mode`.
  import { settings, effectiveSettings, type WorkspaceOverrides } from '../store';
  import { createSelect, createCollapsible, melt } from '@melt-ui/svelte';
  import { get } from 'svelte/store';
  import { writeKey, type SettingsScope } from './writeKey';

  export let providers: string[] = [];
  export let models: string[] = [];
  export let busy: boolean = false;
  export let mode: SettingsScope = 'global';

  $: display = mode === 'workspace' ? $effectiveSettings : $settings;

  function write<K extends keyof WorkspaceOverrides>(key: K, value: WorkspaceOverrides[K]) {
    writeKey(mode, key, value);
  }

  // Provider select. Reads / writes route through `display` / write
  // so the same component works in both modes.
  const initialProvider = mode === 'workspace' ? get(effectiveSettings).provider : get(settings).provider;
  const {
    elements: { trigger: provSelTrig, menu: provSelMenu, option: provSelOpt },
    states: { selectedLabel: provLabel, open: provOpen, selected: provSelected },
    helpers: { isSelected: isProvSelected }
  } = createSelect<string>({
    positioning: { placement: 'bottom', sameWidth: true },
    defaultSelected: initialProvider ? { value: initialProvider, label: initialProvider } : undefined
  });

  provSelected.subscribe(s => {
    if (!s) return;
    const cur = mode === 'workspace' ? get(effectiveSettings).provider : get(settings).provider;
    if (s.value === cur) return;
    write('provider', s.value);
    // Provider change invalidates model since model lists are per-provider.
    write('model', '');
  });
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
    defaultSelected: initialModel ? { value: initialModel, label: initialModel } : undefined
  });
  modSelected.subscribe(s => {
    if (!s) return;
    const cur = mode === 'workspace' ? get(effectiveSettings).model : get(settings).model;
    if (s.value === cur) return;
    write('model', s.value);
  });
  const modSource = mode === 'workspace' ? effectiveSettings : settings;
  modSource.subscribe(s => {
    const cur = get(modSelected);
    if (s.model && (!cur || cur.value !== s.model)) {
      modSelected.set({ value: s.model, label: s.model });
    }
  });

  // When the models prop arrives, ensure the effective model is still
  // valid; otherwise reset to the provider's default.
  $: if (models.length > 0 && display && !models.includes(display.model)) {
    write('model', models[0]);
  }

  // Collapsible sections.
  const mk = (open: boolean) => createCollapsible({ defaultOpen: open });
  const { elements: { root: provRoot, trigger: provTrig, content: provContent }, states: { open: provSectOpen } } = mk(true);
  const { elements: { root: modRoot, trigger: modTrig, content: modContent }, states: { open: modSectOpen } } = mk(true);
  const { elements: { root: sysRoot, trigger: sysTrig, content: sysContent }, states: { open: sysOpen } } = mk(false);
  const { elements: { root: sampRoot, trigger: sampTrig, content: sampContent }, states: { open: sampOpen } } = mk(false);
  const { elements: { root: thinkRoot, trigger: thinkTrig, content: thinkContent }, states: { open: thinkSectOpen } } = mk(false);

  // Stop sequences edited as comma-separated text. Initialised empty;
  // the reactive seed below sets the value before first paint.
  let stopText = '';
  $: stopText = (display?.stopSequences ?? []).join(', ');
  function commitStopSequences() {
    const arr = stopText.split(',').map(s => s.trim()).filter(s => s.length > 0);
    write('stopSequences', arr);
  }
</script>

<!-- Provider -->
<section class="border-b border-border" use:melt={$provRoot}>
  <button class="section-head" use:melt={$provTrig}>
    <span class="caret">{$provSectOpen ? '▾' : '▸'}</span>
    <span>Provider</span>
  </button>
  <div use:melt={$provContent} class="section-body">
    <button class="select-trigger" disabled={busy || providers.length === 0} use:melt={$provSelTrig}>
      <span>{$provLabel || 'select provider'}</span>
      <span class="text-fg-faint text-[0.7rem]">{$provOpen ? '▴' : '▾'}</span>
    </button>
    {#if $provOpen}
      <div class="select-menu" use:melt={$provSelMenu}>
        {#each providers as p}
          <div class="select-item {$isProvSelected(p) ? 'bg-accent' : ''}" use:melt={$provSelOpt({ value: p, label: p })}>{p}</div>
        {/each}
      </div>
    {/if}
    {#if mode === 'global'}
      <!-- streaming is global-only (not workspace-overridable) -->
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
    <button class="select-trigger" disabled={busy || models.length === 0} use:melt={$modSelTrig}>
      <span>{$modLabel || 'select model'}</span>
      <span class="text-fg-faint text-[0.7rem]">{$modOpen ? '▴' : '▾'}</span>
    </button>
    {#if $modOpen}
      <div class="select-menu" use:melt={$modSelMenu}>
        {#each models as m}
          <div class="select-item {$isModSelected(m) ? 'bg-accent' : ''}" use:melt={$modSelOpt({ value: m, label: m })}>{m}</div>
        {/each}
      </div>
    {/if}
  </div>
</section>

<!-- System Prompt -->
<section class="border-b border-border" use:melt={$sysRoot}>
  <button class="section-head" use:melt={$sysTrig}>
    <span class="caret">{$sysOpen ? '▾' : '▸'}</span>
    <span>System Prompt</span>
  </button>
  <div use:melt={$sysContent} class="section-body">
    <textarea
      class="text-input"
      value={display.system}
      on:input={(e) => write('system', e.currentTarget.value)}
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
          type="range" min="0" max="2" step="0.05"
          value={display.temperature ?? 1}
          on:input={(e) => write('temperature', parseFloat(e.currentTarget.value))}
          disabled={busy} class="flex-1"
        />
        <button class="mini-btn" on:click={() => write('temperature', null)} disabled={busy} title="Use provider default">reset</button>
      </div>
    </label>

    <label class="field">
      <div class="flex justify-between">
        <span>Top-p</span>
        <span class="text-fg-faint">{display.topP ?? 'default'}</span>
      </div>
      <div class="flex items-center gap-2">
        <input
          type="range" min="0" max="1" step="0.01"
          value={display.topP ?? 1}
          on:input={(e) => write('topP', parseFloat(e.currentTarget.value))}
          disabled={busy} class="flex-1"
        />
        <button class="mini-btn" on:click={() => write('topP', null)} disabled={busy}>reset</button>
      </div>
    </label>

    <label class="field">
      <span>Max tokens</span>
      <input
        type="number" class="text-input" min="1" step="1"
        value={display.maxTokens}
        on:input={(e) => write('maxTokens', parseInt(e.currentTarget.value, 10) || 0)}
        disabled={busy}
      />
    </label>

    <label class="field">
      <span>Stop sequences (comma-separated)</span>
      <input type="text" class="text-input" bind:value={stopText} on:blur={commitStopSequences} disabled={busy} />
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
        type="checkbox" checked={display.thinkEnabled}
        on:change={(e) => write('thinkEnabled', e.currentTarget.checked)}
        disabled={busy} class="m-0"
      />
      <span>Enable extended thinking (Anthropic)</span>
    </label>
    <label class="field">
      <span>Budget tokens (min 1024)</span>
      <input
        type="number" class="text-input" min="1024" step="256"
        value={display.thinkBudget}
        on:input={(e) => write('thinkBudget', parseInt(e.currentTarget.value, 10) || 1024)}
        disabled={busy || !display.thinkEnabled}
      />
    </label>
    <p class="text-[0.7rem] text-fg-faint leading-snug">
      Thinking counts toward max_tokens and is only supported by Claude 3.7+ / 4.x. OpenAI ignores this setting.
    </p>
  </div>
</section>
