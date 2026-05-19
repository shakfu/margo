<script lang="ts">
  // MCP servers section. Lists every server margo's mcp.Manager knows
  // about, with status, tools count, and a stderr tail for failed
  // entries. Polls every 2 seconds while the section is open — cheap
  // enough (the Wails IPC round-trip is sub-millisecond) and avoids
  // wiring a push channel in the MVP. A future slice can replace the
  // poll with an EventsOn subscription if the latency matters.
  //
  // Add / Remove operations write a row through the App's Wails
  // bindings. The config file (mcp.json) is not synced back here —
  // doing so is a follow-up (the MVP relies on the user editing
  // mcp.json directly for persistent additions; AddMCPServer only adds
  // for the current session).
  import { createCollapsible, melt } from '@melt-ui/svelte';
  import { onDestroy, onMount } from 'svelte';
  import { MCPServers, AddMCPServer, RemoveMCPServer } from '../../../wailsjs/go/main/App.js';

  // Mirror of main.MCPServerInfo from app.go. Inlining (rather than
  // importing from wailsjs/go/models.ts) keeps the import surface to
  // just the runtime methods above — the generated models are types
  // only with no runtime counterpart at the .js path Vite serves.
  interface ServerInfo {
    name: string;
    command?: string;
    args?: string[];
    status: string;
    error?: string;
    tools?: string[];
    stderrTail?: string[];
  }

  const { elements: { root: mcpRoot, trigger: mcpTrig, content: mcpContent }, states: { open: mcpOpen } } =
    createCollapsible({ defaultOpen: false });

  let servers: ServerInfo[] = [];
  let pollHandle: ReturnType<typeof setInterval> | null = null;
  let error = '';

  // Add-server form state. Hidden behind a toggle so the section stays
  // compact at rest. Args are entered as a space-separated string and
  // split on commit; quoting is not supported in MVP (community
  // server commands rarely need it: `npx -y package /path`).
  let showAdd = false;
  let addName = '';
  let addCommand = '';
  let addArgsText = '';
  let addEnvText = ''; // KEY=value lines, one per line

  async function refresh() {
    try {
      servers = await MCPServers();
      error = '';
    } catch (e) {
      error = String(e);
    }
  }

  onMount(refresh);

  // Poll only while the section is expanded. Stop on collapse and on
  // destroy. The reactive block reads $mcpOpen and (re)arms the timer.
  $: {
    if ($mcpOpen) {
      void refresh();
      if (!pollHandle) {
        pollHandle = setInterval(refresh, 2000);
      }
    } else if (pollHandle) {
      clearInterval(pollHandle);
      pollHandle = null;
    }
  }
  onDestroy(() => { if (pollHandle) clearInterval(pollHandle); });

  function parseEnv(text: string): Record<string, string> {
    const out: Record<string, string> = {};
    for (const line of text.split('\n')) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;
      const eq = trimmed.indexOf('=');
      if (eq <= 0) continue;
      out[trimmed.slice(0, eq).trim()] = trimmed.slice(eq + 1);
    }
    return out;
  }

  async function commitAdd() {
    if (!addName.trim() || !addCommand.trim()) {
      error = 'name and command are required';
      return;
    }
    const spec = {
      command: addCommand.trim(),
      args: addArgsText.trim() ? addArgsText.trim().split(/\s+/) : [],
      env: parseEnv(addEnvText),
    };
    try {
      // Wails accepts the plain JSON shape; the generated `mcp.ServerSpec`
      // class is sugar we don't need here.
      await AddMCPServer(addName.trim(), spec as any);
      addName = '';
      addCommand = '';
      addArgsText = '';
      addEnvText = '';
      showAdd = false;
      await refresh();
    } catch (e) {
      error = String(e);
    }
  }

  async function remove(name: string) {
    try {
      await RemoveMCPServer(name);
      await refresh();
    } catch (e) {
      error = String(e);
    }
  }

  // statusColor maps the mcp.ServerStatus string to a small badge tone.
  function statusColor(status: string): string {
    switch (status) {
      case 'ready':    return 'bg-bubble-user/40';
      case 'starting': return 'bg-bubble-assistant';
      case 'failed':   return 'bg-error-bg text-error-fg';
      case 'stopped':  return 'bg-input-bg text-fg-faint';
      default:         return 'bg-input-bg';
    }
  }
</script>

<section class="border-b border-border" use:melt={$mcpRoot}>
  <button class="section-head" use:melt={$mcpTrig}>
    <span class="caret">{$mcpOpen ? '▾' : '▸'}</span>
    <span>MCP servers</span>
    <span class="ml-1 text-fg-faint text-[0.72rem]">({servers.length})</span>
  </button>
  <div use:melt={$mcpContent} class="section-body">
    <p class="text-[0.78rem] text-fg-muted leading-snug mb-2">
      Subprocess MCP servers margo launched at startup. Edit
      <code class="font-[family-name:var(--font-mono)] text-[0.72rem]">~/Library/Application Support/Margo/mcp.json</code>
      to make additions persist across restarts (Claude-Desktop-compatible). Each ready server's tools appear in
      the Tools section prefixed <code class="font-[family-name:var(--font-mono)] text-[0.72rem]">mcp:&lt;name&gt;:</code>.
    </p>

    {#if servers.length === 0}
      <div class="text-[0.78rem] text-fg-faint italic mb-2">No MCP servers configured.</div>
    {:else}
      <ul class="flex flex-col gap-1 mb-2">
        {#each servers as s (s.name)}
          <li class="flex flex-col gap-1 text-[0.78rem] bg-input-bg border border-border rounded px-2 py-1.5">
            <div class="flex items-start gap-2">
              <div class="flex-1 min-w-0">
                <div class="font-semibold text-fg flex items-center gap-1 flex-wrap">
                  <span>{s.name}</span>
                  <span class="text-[0.62rem] uppercase tracking-wider px-1 rounded {statusColor(s.status)}">{s.status}</span>
                  {#if s.tools && s.tools.length > 0}
                    <span class="text-fg-faint text-[0.62rem] uppercase tracking-wider">{s.tools.length} tool{s.tools.length === 1 ? '' : 's'}</span>
                  {/if}
                </div>
                {#if s.error}
                  <div class="text-error-fg text-[0.7rem] leading-snug mt-0.5 break-words">{s.error}</div>
                {/if}
              </div>
              <button class="mini-btn" on:click={() => remove(s.name)} title="Stop and unregister this server">Remove</button>
            </div>
            {#if s.status === 'failed' && s.stderrTail && s.stderrTail.length > 0}
              <details class="text-[0.7rem] text-fg-faint">
                <summary class="cursor-pointer">stderr tail ({s.stderrTail.length} lines)</summary>
                <pre class="mt-1 max-h-32 overflow-auto whitespace-pre-wrap font-[family-name:var(--font-mono)] text-[0.65rem] bg-bg p-1 rounded border border-border">{s.stderrTail.join('\n')}</pre>
              </details>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}

    {#if showAdd}
      <div class="flex flex-col gap-2 p-2 border border-border rounded bg-bg">
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Name</span>
          <input type="text" class="text-input" bind:value={addName} placeholder="e.g. filesystem" />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Command</span>
          <input type="text" class="text-input" bind:value={addCommand} placeholder="npx" />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Args (space-separated)</span>
          <input type="text" class="text-input" bind:value={addArgsText} placeholder="-y @modelcontextprotocol/server-filesystem /Users/me/notes" />
        </label>
        <label class="flex flex-col gap-1 text-[0.78rem] text-fg-muted">
          <span>Environment (one KEY=value per line)</span>
          <textarea class="text-input" rows="3" bind:value={addEnvText} placeholder="GITHUB_TOKEN=ghp_..."></textarea>
        </label>
        <div class="flex justify-end gap-2">
          <button class="mini-btn" on:click={() => (showAdd = false)}>Cancel</button>
          <button class="mini-btn" on:click={commitAdd} disabled={!addName.trim() || !addCommand.trim()}>Add</button>
        </div>
        <div class="text-[0.7rem] text-fg-faint">Note: session-only. Edit mcp.json to persist.</div>
      </div>
    {:else}
      <button class="mini-btn" on:click={() => (showAdd = true)}>+ Add server</button>
    {/if}

    {#if error}<div class="text-[0.7rem] text-error-fg mt-1 break-words">{error}</div>{/if}
  </div>
</section>
