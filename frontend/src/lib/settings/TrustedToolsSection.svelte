<script lang="ts">
  // Trusted tools (global user-trust list, not per-workspace). Lives in
  // the global Settings dialog only; the right-pane workspace mode
  // hides this whole tab section. Stores the list of tool names the
  // user has clicked "Always" on; those run without a permission prompt.
  import { settings } from '../store';
  import { createCollapsible, melt } from '@melt-ui/svelte';

  const { elements: { root: trustRoot, trigger: trustTrig, content: trustContent }, states: { open: trustOpen } } =
    createCollapsible({ defaultOpen: false });

  function revokeTool(name: string) {
    settings.update(s => ({
      ...s,
      autoApproveTools: (s.autoApproveTools ?? []).filter(t => t !== name),
    }));
  }
  function revokeAllTools() {
    settings.update(s => ({ ...s, autoApproveTools: [] }));
  }
</script>

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
            <button class="mini-btn" title="Revoke; future calls will prompt again" on:click={() => revokeTool(name)}>Revoke</button>
          </li>
        {/each}
      </ul>
      <button class="mini-btn" on:click={revokeAllTools}>Revoke all</button>
    {/if}
  </div>
</section>
