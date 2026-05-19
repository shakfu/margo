<script lang="ts">
  // SettingsPanel is the right-pane settings + Cmd+, modal shell.
  // It owns the top-level Tabs (Models / Roles / General / MCP) and
  // delegates each tab's content to a focused subcomponent under
  // ./settings/. Compositional split replaces the prior 1100-LoC
  // monolith; see lib/settings/*.svelte for the section-level code.
  //
  // mode controls scope:
  //   - 'workspace' (right sidebar): inputs read effectiveSettings and
  //     write via setEffectiveOverride. Personas / Knowledge / Tools /
  //     MCP servers tabs only render in this mode.
  //   - 'global' (Cmd+, dialog): inputs read/write the raw global
  //     settings. Trusted-tools and General tabs only render here.
  import { createTabs, melt } from '@melt-ui/svelte';
  import ProviderSettings from './settings/ProviderSettings.svelte';
  import PersonasSection from './settings/PersonasSection.svelte';
  import KnowledgeSection from './settings/KnowledgeSection.svelte';
  import ToolsSection from './settings/ToolsSection.svelte';
  import TrustedToolsSection from './settings/TrustedToolsSection.svelte';
  import MCPServersSection from './settings/MCPServersSection.svelte';
  import GeneralSettings from './settings/GeneralSettings.svelte';

  export let providers: string[] = [];
  export let models: string[] = [];
  export let busy: boolean = false;
  export let outputDir: string = '';
  export let onReset: () => void = () => {};
  export let mode: 'workspace' | 'global' = 'global';

  // "models" — provider / model / system / sampling / thinking.
  // "agents" — personas, knowledge, tools (workspace) OR trusted (global).
  // "mcp"    — MCP server lifecycle. Workspace-only (server attach
  //            scoping is global today, but the tab still makes sense in
  //            the right pane since it directly affects the tools the
  //            active workspace can use; future per-workspace server
  //            scoping will land naturally here).
  // "general" — appearance / output / reset. Global only.
  const {
    elements: { root: tabsRoot, list: tabsList, trigger: tabsTrigger, content: tabsContent },
  } = createTabs({ defaultValue: 'models' });
</script>

<div class="flex flex-col h-full bg-bg-elev border-l border-border overflow-y-auto" use:melt={$tabsRoot}>
  <div class="px-3.5 pt-3.5 pb-2 font-semibold text-[0.9rem]">Settings</div>
  <div use:melt={$tabsList} class="flex border-b border-border px-2 gap-0.5" aria-label="Settings tabs">
    <button class="tab-trigger" use:melt={$tabsTrigger('models')}>Models</button>
    <button class="tab-trigger" use:melt={$tabsTrigger('agents')}>Roles</button>
    {#if mode === 'workspace'}
      <button class="tab-trigger" use:melt={$tabsTrigger('mcp')}>MCP</button>
    {/if}
    {#if mode === 'global'}
      <button class="tab-trigger" use:melt={$tabsTrigger('general')}>General</button>
    {/if}
  </div>

  <!-- Models tab -->
  <div use:melt={$tabsContent('models')}>
    <ProviderSettings {providers} {models} {busy} {mode} />
  </div>

  <!-- Roles tab -->
  <div use:melt={$tabsContent('agents')}>
    {#if mode === 'workspace'}
      <PersonasSection />
      <KnowledgeSection />
      <ToolsSection />
    {/if}
    {#if mode === 'global'}
      <TrustedToolsSection />
    {/if}
  </div>

  <!-- MCP tab — workspace mode only (status is global, but the user
       reaches this UI from the workspace sidebar). -->
  {#if mode === 'workspace'}
    <div use:melt={$tabsContent('mcp')}>
      <MCPServersSection />
    </div>
  {/if}

  <!-- General tab — global mode only -->
  {#if mode === 'global'}
    <div use:melt={$tabsContent('general')}>
      <GeneralSettings {outputDir} {onReset} />
    </div>
  {/if}
</div>
