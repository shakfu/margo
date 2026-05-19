<script lang="ts">
  // Tools section of the Roles tab. Workspace-mode only. The catalog
  // includes both builtins and MCP tools — Go-side core.Session.Tools()
  // merges them — and the workspace's enabled set narrows the palette
  // passed to the agent runner.
  import { settings, activeWorkspace, setWorkspaceToolEnabled, isToolEnabledForWorkspace } from '../store';
  import { createCollapsible, melt } from '@melt-ui/svelte';
  import { ToolsMetadata } from '../../../wailsjs/go/main/App.js';
  import { onMount } from 'svelte';

  type ToolMeta = { name: string; description: string; isReadOnly: boolean; isStreamable: boolean };
  let toolsCatalog: ToolMeta[] = [];

  const { elements: { root: toolsRoot, trigger: toolsTrig, content: toolsContent }, states: { open: toolsOpen } } =
    createCollapsible({ defaultOpen: false });

  async function refresh() {
    try {
      toolsCatalog = (await ToolsMetadata()) as ToolMeta[];
    } catch (_) {
      toolsCatalog = [];
    }
  }
  onMount(refresh);

  // Refresh whenever the active workspace changes — the per-workspace
  // checkbox state derives from $activeWorkspace, but also re-pull the
  // catalog in case it grew (e.g. an MCP server just turned ready).
  $: { void $settings.activeWorkspaceId; refresh(); }

  function onToggle(name: string, ev: Event) {
    const checked = (ev.currentTarget as HTMLInputElement).checked;
    setWorkspaceToolEnabled(
      $settings.activeWorkspaceId,
      name,
      checked,
      toolsCatalog.map(t => t.name),
    );
  }

  // origin labels a tool as builtin vs MCP for the UI badge.
  function origin(name: string): 'builtin' | 'mcp' {
    return name.startsWith('mcp:') ? 'mcp' : 'builtin';
  }
</script>

<section class="border-b border-border" use:melt={$toolsRoot}>
  <button class="section-head" use:melt={$toolsTrig}>
    <span class="caret">{$toolsOpen ? '▾' : '▸'}</span>
    <span>Tools</span>
    <span class="ml-1 text-fg-faint text-[0.72rem]">({toolsCatalog.length})</span>
  </button>
  <div use:melt={$toolsContent} class="section-body">
    <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
      Tools available to the agent runner in this workspace. Unchecking removes a tool from the palette for new agent runs.
      Tools prefixed <code class="font-[family-name:var(--font-mono)] text-[0.72rem]">mcp:&lt;server&gt;:</code> come from configured MCP servers.
    </p>
    {#if toolsCatalog.length === 0}
      <div class="text-[0.78rem] text-fg-faint italic">No tools registered.</div>
    {:else}
      <ul class="flex flex-col gap-1">
        {#each toolsCatalog as t (t.name)}
          <li class="flex items-start gap-2 text-[0.78rem] bg-input-bg border border-border rounded px-2 py-1.5">
            <input
              type="checkbox" class="mt-0.5"
              checked={isToolEnabledForWorkspace($activeWorkspace, t.name)}
              on:change={(ev) => onToggle(t.name, ev)}
              aria-label={`Enable ${t.name}`}
            />
            <div class="flex-1 min-w-0">
              <div class="font-[family-name:var(--font-mono)] text-fg flex items-center gap-1 flex-wrap">
                <span>{t.name}</span>
                {#if origin(t.name) === 'mcp'}<span class="text-fg-faint text-[0.62rem] uppercase tracking-wider">mcp</span>{/if}
                {#if t.isReadOnly}<span class="text-fg-faint text-[0.62rem] uppercase tracking-wider">read-only</span>{/if}
                {#if t.isStreamable}<span class="text-fg-faint text-[0.62rem] uppercase tracking-wider">streamable</span>{/if}
              </div>
              {#if t.description}
                <div class="text-fg-faint text-[0.7rem] leading-snug mt-0.5">{t.description}</div>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</section>
