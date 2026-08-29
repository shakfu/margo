<script lang="ts">
  // Chat header: title, provider/model badges, the cost meter and its
  // per-model popover, export, and the two sidebar toggles.
  //
  // Extracted from App.svelte. Reads the stores it needs directly
  // rather than taking them as props — they are module singletons, so
  // threading them through would add a prop per badge and nothing else.
  // Export stays in the parent because its failure surfaces in the
  // parent's error banner.
  import { createPopover, melt } from '@melt-ui/svelte';
  import { createEventDispatcher } from 'svelte';
  import {
    settings,
    activeChat,
    effectiveSettings,
    modelCatalog,
    costBreakdown,
    formatCost,
  } from './store';

  const dispatch = createEventDispatcher<{ export: void }>();

  // Cost breakdown popover. The header carries the total; the split by
  // model sits behind a click because most chats use one model and have
  // nothing to show.
  const {
    elements: { trigger: costTrigger, content: costContent },
    states: { open: costOpen },
  } = createPopover({ positioning: { placement: 'bottom' } });

  // Priced per turn against the model that produced it, so switching
  // model mid-chat does not reprice the history.
  $: costs = costBreakdown($activeChat?.messages ?? [], $modelCatalog);
  $: showCost = costs.entries.length > 0 || costs.unrecordedTurns > 0;
  $: providerCount = new Set(costs.entries.map((e) => e.provider)).size;

  function toggleLeft() {
    settings.update((s) => ({ ...s, showLeft: !s.showLeft }));
  }
  function toggleRight() {
    settings.update((s) => ({ ...s, showRight: !s.showRight }));
  }
</script>

<header class="flex items-center gap-2 px-3.5 py-2 border-b border-border bg-bg">
  <button
    class="topbar-btn"
    on:click={toggleLeft}
    title={$settings.showLeft ? 'Hide chats' : 'Show chats'}
  >{$settings.showLeft ? '⟨' : '⟩'}</button>
  <div class="flex-1 text-center text-[0.9rem] font-medium text-fg-muted overflow-hidden text-ellipsis whitespace-nowrap">
    {$activeChat?.title ?? ''}
  </div>
  <div class="flex items-center gap-2">
    <span class="badge">{$effectiveSettings.provider || 'no provider'}</span>
    {#if $effectiveSettings.model}<span class="badge">{$effectiveSettings.model}</span>{/if}
    {#if showCost}
      <button
        class="badge cursor-pointer hover:bg-hover-bg"
        use:melt={$costTrigger}
        title="Approximate USD cost for this chat, priced per turn against the model that produced it. Excludes prompt-cache discounts and assumes the uncached rate, so it overestimates rather than underestimates. Click for the per-model split."
      >
        {formatCost(costs.total)}{costs.partial ? '+' : ''}
        {#if costs.multiProvider}
          <span class="text-fg-faint ml-1">{costs.entries.length} models, {providerCount} providers</span>
        {:else if costs.multiModel}
          <span class="text-fg-faint ml-1">{costs.entries.length} models</span>
        {/if}
      </button>
      {#if $costOpen}
        <div use:melt={$costContent} class="z-50 rounded border border-border bg-bg-elev shadow-lg p-2 min-w-[19rem] text-[0.78rem]">
          <div class="font-semibold mb-1.5">Cost by model</div>
          {#if costs.entries.length === 0}
            <div class="text-fg-muted">No priced turns yet.</div>
          {/if}
          {#each costs.entries as e (e.provider + e.model)}
            <div class="flex items-baseline gap-2 py-0.5">
              <span class="flex-1 font-[family-name:var(--font-mono)] break-all">
                {e.model}
                {#if costs.multiProvider && e.provider}
                  <span class="text-fg-faint">({e.provider})</span>
                {/if}
              </span>
              <span class="text-fg-faint whitespace-nowrap">
                {e.tokensIn.toLocaleString()} in / {e.tokensOut.toLocaleString()} out
              </span>
              <span class="w-16 text-right whitespace-nowrap">
                {#if e.priced}{formatCost(e.cost)}{:else}<span class="text-fg-faint" title="No rates declared for this model in the catalog">no rate</span>{/if}
              </span>
            </div>
          {/each}
          {#if costs.unrecordedTurns > 0}
            <div class="text-fg-faint mt-1.5 pt-1.5 border-t border-border">
              {costs.unrecordedTurns} earlier turn{costs.unrecordedTurns === 1 ? '' : 's'} predate per-model tracking and cannot be priced.
            </div>
          {/if}
          {#if costs.partial}
            <div class="text-fg-faint mt-1">Total is a floor: some turns have no rate.</div>
          {/if}
        </div>
      {/if}
    {/if}
    {#if $effectiveSettings.thinkEnabled}<span class="badge badge-active">thinking</span>{/if}
    <button
      class="topbar-btn"
      on:click={() => dispatch('export')}
      disabled={!$activeChat || ($activeChat.messages?.length ?? 0) === 0}
      title="Export chat as markdown"
    >↓ md</button>
    <button
      class="topbar-btn"
      on:click={toggleRight}
      title={$settings.showRight ? 'Hide settings' : 'Show settings'}
    >{$settings.showRight ? '⟩' : '⟨'}</button>
  </div>
</header>

<style>

  .badge {
    font-size: 0.7rem;
    color: var(--fg-faint);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    background: var(--input-bg);
    padding: 0.2rem 0.5rem;
    border-radius: 3px;
    border: 1px solid var(--border);
  }
  .badge-active {
    color: var(--fg);
    background: var(--accent);
    border-color: transparent;
  }
</style>
