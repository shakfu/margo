<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { createDialog, melt } from '@melt-ui/svelte';
  import { Providers, Models, Chat, StreamChat, StreamAgent, CancelStream, Tools, ToolsMetadata, OutputDir, OpenPath, RespondPermission, StartupWorkspaceDir, SetActiveWorkspace, SaveAttachment, LoadAttachment, ExportChatMarkdown } from '../wailsjs/go/main/App.js';
  import { EventsOn, EventsOff, BrowserOpenURL } from '../wailsjs/runtime/runtime.js';
  import { mathjax } from './lib/mathjax';
  import { renderMarkdownStreaming, setHighlightTheme } from './lib/markdown';
  import { subscribeStream } from './lib/stream';
  import Composer from './lib/Composer.svelte';
  import { revokePreviews, selectPriorAttachments, type PendingAttachment } from './lib/attachments';
  import Topbar from './lib/Topbar.svelte';
  import MessageList from './lib/MessageList.svelte';

  import {
    chats,
    activeChat,
    activeChatId,
    settings,
    effectiveSettings,
    contextWindowFor,
    modelCatalog,
    initModelCatalog,
    newChat,
    appendMessage,
    appendToLast,
    appendThinkingToLast,
    appendStepToLast,
    appendStepStream,
    setStepHits,
    updateLastStepResult,
    resolvePermissionStep,
    setLastUsage,
    setChatPersona,
    findPersona,
    addWorkspace,
    activeWorkspace,
    setActiveWorkspace,
    isMultimodal,
    modelToRestore,
    setEffectiveOverride,
    type Usage,
    type AgentStep,
    type Message,
    type StoredAttachment
  } from './lib/store';
  import ChatList from './lib/ChatList.svelte';
  import SettingsPanel from './lib/SettingsPanel.svelte';
  import AttachmentThumb from './lib/AttachmentThumb.svelte';
  import { parseSlash, slugify, SLASH_COMMANDS } from './lib/slash';

  let providers: string[] = [];
  let models: string[] = [];
  let availableTools: string[] = [];
  let outputDir = '';
  let input = '';
  let busy = false;

  // Settings dialog opened from the Margo › Settings… menu (Cmd+,).
  // Renders a second instance of SettingsPanel; the right-pane stays
  // available. Independent collapsible state per instance, but both
  // bind to the same global $settings store.
  const {
    elements: {
      overlay: settingsDlgOverlay,
      content: settingsDlgContent,
      title: settingsDlgTitle,
      close: settingsDlgClose,
      portalled: settingsDlgPortalled,
    },
    states: { open: settingsDlgOpen },
  } = createDialog({ role: 'dialog' });

  let attachments: PendingAttachment[] = [];
  let error = '';
  let activeStreamId = '';
  let cancelling = false;
  let messagesEl: HTMLElement;

  $: messages = $activeChat?.messages ?? [];

  // Push the active workspace id to Go so the search_knowledge tool can
  // resolve which collection to query at invoke time. The reactive
  // statement fires on any $settings change; the lastPushedWorkspaceId
  // guard de-dupes so we only hit IPC on actual workspace switches.
  let lastPushedWorkspaceId = '';
  $: if ($settings.activeWorkspaceId && $settings.activeWorkspaceId !== lastPushedWorkspaceId) {
    lastPushedWorkspaceId = $settings.activeWorkspaceId;
    SetActiveWorkspace(lastPushedWorkspaceId).catch(() => {});
  }

  $: gridCols =
    $settings.showLeft && $settings.showRight ? 'grid-cols-[280px_1fr_320px]' :
    $settings.showLeft && !$settings.showRight ? 'grid-cols-[280px_1fr_0]' :
    !$settings.showLeft && $settings.showRight ? 'grid-cols-[0_1fr_320px]' :
    'grid-cols-[0_1fr_0]';

  // Refresh model list when the *effective* provider changes — the
  // active workspace may override it. The Models picker in
  // SettingsPanel still binds to the global provider; this fetch is
  // about the list shown for outbound chat.
  $: if ($effectiveSettings.provider) {
    reloadModels($effectiveSettings.provider);
  }

  function reloadModels(provider: string) {
    if (!provider) return;
    Models(provider).then(m => { models = m; });
  }

  // Re-apply the remembered model once the provider's catalog is known.
  //
  // Recording the pick is not enough: in the Default workspace model
  // changes are session-scoped by design, so they never reach persisted
  // settings, and the only other reader was a repair branch that fires
  // solely when the current model has vanished from the catalog. The
  // result was a memory written on every pick and read on none.
  //
  // Latched per provider so this runs once when the catalog arrives and
  // never contends with a later in-session pick — and a pick updates the
  // memory anyway, which makes the restore a no-op.
  const restoredProviders = new Set<string>();
  $: {
    const p = $effectiveSettings.provider;
    if (p && !restoredProviders.has(p)) {
      const want = modelToRestore(p, models, $settings.lastModelByProvider, $effectiveSettings.model);
      if (want) {
        restoredProviders.add(p);
        setEffectiveOverride('model', want);
      }
    }
  }

  // Tools whose approval may not be made permanent — quarto_render and
  // anything else the Go side marks allowsAlways=false. The prompt
  // hides "Always" for these; Go enforces the same rule regardless.
  let noAlwaysTools = new Set<string>();
  $: canAlwaysApprove = (name: string) => !noAlwaysTools.has(name);

  // Context usage for the active chat. Uses the *effective* model so a
  // workspace override of the model picks the right context window.
  $: ctxWindow = contextWindowFor($effectiveSettings.model, $modelCatalog);
  $: ctxUsed = ($activeChat?.tokensIn ?? 0) + ($activeChat?.tokensOut ?? 0);
  // Gate: attachments are pending but the *effective* model isn't on
  // the multimodal allowlist. Disables send + surfaces an inline warning.
  // Only image attachments need a multimodal-capable model. PDFs and
  // other documents reach the model either natively (Anthropic) or via
  // Go-side text extraction (OpenAI / OpenRouter), so they work
  // regardless of vision support. (§7.5)
  $: hasImageAttachment = attachments.some(a => a.mimeType.startsWith('image/'));
  $: attachmentsBlocked = hasImageAttachment && !!$effectiveSettings.model && !isMultimodal($effectiveSettings.model, $modelCatalog);

  onMount(async () => {
    document.documentElement.classList.toggle('dark', $settings.theme === 'dark');
    setHighlightTheme($settings.theme);

    // Populate the model catalog before any UI consumer reads it. The
    // catalog is the source of truth for context-window math and the
    // multimodal allowlist (previously hand-mirrored in store.ts).
    void initModelCatalog();
    // Reconcile persisted "Always" grants against current policy: a
    // tool that has since become ineligible must prompt again.
    ToolsMetadata().then(metas => {
      noAlwaysTools = new Set(metas.filter(m => !m.allowsAlways).map(m => m.name));
      settings.update(s => ({
        ...s,
        autoApproveTools: (s.autoApproveTools ?? []).filter(t => !noAlwaysTools.has(t)),
      }));
    }).catch(() => {});

    try {
      providers = await Providers();
      if (providers.length > 0 && !$settings.provider) {
        settings.update(s => ({ ...s, provider: providers[0] }));
      } else if (providers.length === 0) {
        error = 'No providers configured. Set ANTHROPIC_API_KEY or OPENAI_API_KEY in .env and restart.';
      }
      availableTools = await Tools();
      outputDir = await OutputDir();
    } catch (e) {
      error = String(e);
    }

    if ($chats.length > 0 && !$activeChatId) {
      activeChatId.set($chats[0].id);
    }

    // 7.1.e: honour the -workspace CLI flag. If main.go captured a
    // directory, find a workspace that already binds to it (case-
    // insensitive match on macOS-style paths is intentionally skipped
    // — paths from filepath.Abs are canonicalised) or create one whose
    // name is the directory's basename.
    try {
      const startupDir = await StartupWorkspaceDir();
      if (startupDir) applyStartupWorkspace(startupDir);
    } catch (_) {}

    document.addEventListener('click', handleExternalLinkClick, true);
    // The Go side warms provider catalogs in the background at startup;
    // this fires once they settle so the picker and the cost meter pick
    // up anything the embedded seed did not have.
    EventsOn('margo:models:refreshed', () => {
      initModelCatalog();
      reloadModels($effectiveSettings.provider);
    });

    // Listen for the Margo › Settings… menu item (Cmd+,). Wails fires
    // the event from app.go::openSettings.
    EventsOn('margo:menu:settings', () => {
      settingsDlgOpen.set(true);
    });
  });

  function applyStartupWorkspace(dir: string) {
    const existing = $settings.workspaces.find(w => w.dir === dir);
    if (existing) {
      if (existing.id !== $settings.activeWorkspaceId) setActiveWorkspace(existing.id);
      return;
    }
    // Derive a sensible name from the directory's basename; fall back
    // to the literal path if the path is bare (e.g. "/").
    const parts = dir.split(/[\\/]/).filter(Boolean);
    const name = parts.length > 0 ? parts[parts.length - 1] : dir;
    const id = addWorkspace(name, dir);
    setActiveWorkspace(id);
  }

  function handleExternalLinkClick(ev: MouseEvent) {
    const target = ev.target;
    if (!(target instanceof Element)) return;
    const a = target.closest('a');
    if (!a) return;
    const href = a.getAttribute('href') ?? '';
    if (/^file:/i.test(href)) {
      // Wails BrowserOpenURL rejects non-http schemes ("Invalid URL scheme
      // not allowed"), so route file:// through a Go-side OpenPath that
      // shells out to the OS-native opener.
      ev.preventDefault();
      const path = decodeURI(href.replace(/^file:\/\//i, ''));
      OpenPath(path);
      return;
    }
    if (/^(https?:|mailto:)/i.test(href)) {
      ev.preventDefault();
      BrowserOpenURL(href);
    }
  }

  // ---- attachments ----

  // loadPriorAttachments reads back the attachments an earlier turn
  // carried, so a follow-up question still has the document in front of
  // it. Which ones to re-send is decided by selectPriorAttachments;
  // this only performs the reads, skipping any blob that has gone (the
  // user cleared storage, or moved the profile).
  async function loadPriorAttachments(
    priorMessages: Message[],
  ): Promise<{ name: string; mimeType: string; data: string }[]> {
    const out: { name: string; mimeType: string; data: string }[] = [];
    for (const a of selectPriorAttachments(priorMessages)) {
      try {
        out.push({ name: a.name, mimeType: a.mimeType, data: await LoadAttachment(a.path) });
      } catch (_) {
        // Blob gone; the turn proceeds without it.
      }
    }
    return out;
  }

  // clearAttachments drops the pending strip and releases its blob URLs.
  function clearAttachments() {
    revokePreviews(attachments);
    attachments = [];
  }

  function newStreamId(): string {
    const c = (window as any).crypto;
    if (c?.randomUUID) return c.randomUUID();
    return `s-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  }

  async function scrollToBottom() {
    await tick();
    if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
  }

  function buildOptions() {
    // Read all sampling/model state from effective settings so workspace
    // overrides (model, temperature, thinking, etc.) apply at send time.
    const s = $effectiveSettings;
    return {
      model: s.model,
      maxTokens: s.maxTokens,
      temperature: s.temperature ?? undefined,
      topP: s.topP ?? undefined,
      stopSequences: s.stopSequences,
      thinkEnabled: s.thinkEnabled,
      thinkBudget: s.thinkBudget,
    } as any;
  }

  async function send() {
    const raw = input.trim();
    if ((!raw && attachments.length === 0) || busy || !$effectiveSettings.provider) return;
    if (attachmentsBlocked) {
      error = `${$effectiveSettings.model} doesn't accept images. Switch to a vision-capable model or remove the attachments.`;
      return;
    }

    // Slash-command pre-processing (TODO §9.2). State commands
    // (/persona, /clear) update the chat and return without sending a
    // turn. /agent commands fall through with `forcedRunner` set so
    // the StreamAgent path runs with the picked runner type and the
    // task as the message body.
    let messageText = raw;
    let forcedRunner: string | undefined;
    const slash = parseSlash(raw);
    if (slash) {
      if (slash.kind === 'unknown') {
        const known = SLASH_COMMANDS.map(c => c.command).join(', ');
        error = `Unknown command /${slash.word}. Try one of: ${known}.`;
        return;
      }
      if (slash.kind === 'persona') {
        if (!$activeChat) newChat();
        const target = $activeChat;
        if (!target) return;
        if (slash.slug === '') {
          setChatPersona(target.id, undefined);
        } else {
          const p = $settings.personas.find(p => slugify(p.name) === slash.slug.toLowerCase());
          if (!p) {
            error = `No persona named "${slash.slug}". Available: ${$settings.personas.map(p => slugify(p.name)).join(', ') || 'none'}.`;
            return;
          }
          setChatPersona(target.id, p.id);
        }
        input = '';
        error = '';
        return;
      }
      if (slash.kind === 'clear') {
        if ($activeChat) {
          // setChatPersona(undefined) clears both personaId and
          // agentId — same shape as the role picker's "Default".
          setChatPersona($activeChat.id, undefined);
        }
        input = '';
        error = '';
        return;
      }
      // slash.kind === 'agent'
      if (!slash.task && attachments.length === 0) {
        error = `/${slash.runnerType === 'react' ? 'agent' : 'agent-' + slash.runnerType} needs a task. Try \`/agent <what you want done>\`.`;
        return;
      }
      if (!$settings.streaming) {
        error = `Agent runs require streaming. Enable Streaming in Settings → Sampling and try again.`;
        return;
      }
      forcedRunner = slash.runnerType;
      messageText = slash.task;
    }

    if (!$activeChat) newChat();
    const chat = $activeChat;
    if (!chat) return;

    input = '';
    error = '';
    busy = true;

    // Persist attachments to disk before recording the message so the
    // chat history survives a reload (§7.4). Failure to save any
    // single attachment aborts the send — partial persistence would
    // leave the user with a "half-attached" message that re-sending
    // can't reproduce.
    let stored: { path: string; name: string; mimeType: string; size: number }[] = [];
    try {
      for (const a of attachments) {
        const s = await SaveAttachment(chat.id, a.name, a.mimeType, a.data);
        stored.push({ path: s.path, name: s.name, mimeType: s.mimeType, size: s.size });
      }
    } catch (e) {
      error = `Couldn't save attachment: ${String(e)}`;
      busy = false;
      return;
    }

    appendMessage(chat.id, {
      role: 'user',
      content: messageText,
      attachments: stored.length > 0 ? stored : undefined,
    });
    const history = ($activeChat?.messages ?? []).map(m => ({
      role: m.role,
      content: m.content
    }));
    scrollToBottom();

    // Re-read attachments from earlier turns off disk. Without this the
    // model saw a document exactly once: history carries role+content
    // only, so a follow-up question about an attached PDF arrived with
    // no PDF and read as the model having forgotten it.
    //
    // Failures are per-file and non-fatal — a blob the user deleted from
    // disk should not block the turn, and the note tells the model why
    // the document is missing rather than letting it hallucinate one.
    const priorAttachments = await loadPriorAttachments(
      ($activeChat?.messages ?? []).slice(0, -1),
    );

    // §9.4 retired bundled Agent records. The active role is now just
    // a persona (voice) plus, optionally, a slash-command runner
    // (behavior) — picked per-turn in the parseSlash step above.
    const persona = findPersona($settings.personas, chat.personaId);
    const systemPrompt = persona ? persona.systemPrompt : $effectiveSettings.system;
    const useAgentRoute = !!forcedRunner;
    // Workspace-scoped tool palette (§9.3): undefined enabledTools
    // means "all available enabled" so existing workspaces behave
    // like today until the user narrows the palette in the Tools tab.
    const ws = $activeWorkspace;
    const agentTools = (ws && ws.enabledTools !== undefined)
      ? availableTools.filter(t => ws.enabledTools!.includes(t))
      : availableTools;
    // Snapshot pending attachments and clear immediately — re-using the
    // same array after a send would leak across messages.
    const sendAttachments = [
      ...priorAttachments,
      ...attachments.map(a => ({ name: a.name, mimeType: a.mimeType, data: a.data })),
    ];
    clearAttachments();

    if (!$settings.streaming) {
      try {
        const resp = await Chat($effectiveSettings.provider, systemPrompt, history, buildOptions(), sendAttachments);
        appendMessage(chat.id, {
          role: 'assistant',
          content: resp.text,
          thinking: resp.thinking || undefined,
          usage: resp.usage as Usage,
          // resp.model is what the provider actually served, which can
          // differ from what we asked for (OpenRouter routes).
          provider: $effectiveSettings.provider,
          model: resp.model || $effectiveSettings.model,
        });
        if (resp.usage) setLastUsage(chat.id, resp.usage as Usage);
      } catch (e) {
        error = String(e);
      } finally {
        busy = false;
        scrollToBottom();
      }
      return;
    }

    appendMessage(chat.id, {
      role: 'assistant',
      content: '',
      provider: $effectiveSettings.provider,
      model: $effectiveSettings.model,
    });
    const id = newStreamId();
    activeStreamId = id;
    cancelling = false;
    const targetChatId = chat.id;

    const teardown = subscribeStream(id, targetChatId, {
      on: EventsOn,
      off: (...topics: string[]) => EventsOff(topics[0], ...topics.slice(1)),
      actions: {
        appendText: appendToLast,
        appendThinking: appendThinkingToLast,
        appendStep: appendStepToLast,
        appendStepStream,
        setStepHits,
        updateStepResult: updateLastStepResult,
        setUsage: setLastUsage,
      },
      onChunk: scrollToBottom,
      onError: (msg) => {
        error = msg;
        busy = false;
        activeStreamId = '';
        cancelling = false;
      },
      onDone: () => {
        busy = false;
        activeStreamId = '';
        cancelling = false;
        scrollToBottom();
      },
    });

    try {
      if (useAgentRoute) {
        // Empty runnerType defaults to ReAct on the Go side, so the
        // legacy role-picker path stays unchanged.
        await StreamAgent(id, $effectiveSettings.provider, systemPrompt, history, buildOptions(), agentTools, $settings.autoApproveTools, sendAttachments, forcedRunner ?? '');
      } else {
        await StreamChat(id, $effectiveSettings.provider, systemPrompt, history, buildOptions(), sendAttachments);
      }
    } catch (e) {
      error = String(e);
      busy = false;
      activeStreamId = '';
      cancelling = false;
      teardown();
    }
  }

  // Effective persona for the active chat — drives the dynamic
  // assistant-bubble label. When set, the message header reads the
  // persona's name (uppercased) in place of "ASSISTANT".
  $: activePersona = $activeChat
    ? findPersona($settings.personas, $activeChat.personaId)
    : undefined;

  async function respondPermission(
    permissionId: string,
    toolName: string,
    decision: 'approve' | 'deny' | 'always',
  ) {
    const approved = decision !== 'deny';
    const always = decision === 'always';
    if (always && canAlwaysApprove(toolName)) {
      const cur = $settings.autoApproveTools ?? [];
      if (!cur.includes(toolName)) {
        settings.update(s => ({ ...s, autoApproveTools: [...cur, toolName] }));
      }
    }
    try { await RespondPermission(permissionId, approved, always); } catch (_) {}
    if (activeChatId) {
      resolvePermissionStep($activeChatId, permissionId, approved ? 'approved' : 'denied');
    }
  }

  // Export the active chat as a markdown file. Builds the ChatExport
  // shape from the in-memory Chat, dispatches to Go (which renders +
  // saves), and shows a brief result. Cancelled save dialog is a
  // silent no-op (Go returns "").
  async function exportActiveChat() {
    const chat = $activeChat;
    if (!chat) return;
    const persona = chat.personaId ? findPersona($settings.personas, chat.personaId) : undefined;
    const agent = chat.agentId ? ($settings.agents ?? []).find(a => a.id === chat.agentId) : undefined;
    const payload = {
      title: chat.title,
      provider: $effectiveSettings.provider ?? '',
      model: $effectiveSettings.model ?? '',
      personaName: persona?.name ?? '',
      agentName: agent?.name ?? '',
      createdAt: new Date(chat.createdAt).toISOString(),
      updatedAt: new Date(chat.updatedAt).toISOString(),
      tokensIn: chat.tokensIn ?? 0,
      tokensOut: chat.tokensOut ?? 0,
      messages: chat.messages.map(m => ({
        role: m.role,
        content: m.content,
        thinking: m.thinking ?? '',
        attachments: (m.attachments ?? []).map(a => ({
          name: a.name, mimeType: a.mimeType, size: a.size,
        })),
        steps: (m.steps ?? []).map(s => ({
          kind: s.kind, name: s.name,
          arguments: s.arguments ?? '',
          result: s.result ?? '',
          isError: !!s.isError,
        })),
      })),
    };
    try {
      const path = await ExportChatMarkdown(payload as any);
      if (path) { error = ''; }
    } catch (e) {
      error = `Export failed: ${e}`;
    }
  }

  async function cancel() {
    if (!activeStreamId || cancelling) return;
    cancelling = true;
    try { await CancelStream(activeStreamId); } catch (_) {}
  }

  // Full reset: cancel any in-flight stream so the Go-side goroutine exits,
  // wipe persisted chats + settings from localStorage, then reload the
  // frontend. The Wails Go process keeps running across the reload — only
  // the webview reinitialises — so we must cancel first or the prior
  // stream's events would land in a fresh, surprised UI.
  async function resetApp() {
    if (activeStreamId) {
      try { await CancelStream(activeStreamId); } catch (_) {}
    }
    // Wipe all margo:* keys. Pre-7.1.a there were two ('margo:chats:v1'
    // + 'margo:settings:v1'); 7.1.a introduced per-workspace chat keys
    // ('margo:chats:<workspaceId>:v1') so we iterate to catch them all
    // without having to enumerate the workspaces we're about to discard.
    try {
      const toRemove: string[] = [];
      for (let i = 0; i < localStorage.length; i++) {
        const k = localStorage.key(i);
        if (k && k.startsWith('margo:')) toRemove.push(k);
      }
      for (const k of toRemove) localStorage.removeItem(k);
    } catch (_) {}
    location.reload();
  }



</script>

<div class="grid h-screen bg-bg text-fg {gridCols}">
  <aside class="overflow-hidden min-w-0" aria-hidden={!$settings.showLeft}>
    {#if $settings.showLeft}
      <ChatList {busy} />
    {/if}
  </aside>

  <main class="flex flex-col min-w-0 h-screen">
    <Topbar on:export={exportActiveChat} />

    <MessageList
      {messages}
      {busy}
      {canAlwaysApprove}
      personaLabel={activePersona?.name ?? ''}
      bind:scrollEl={messagesEl}
      on:permission={(e) => respondPermission(e.detail.id, e.detail.name, e.detail.decision)}
    />

    {#if error}
      <div class="bg-error-bg text-error-fg border border-error-border px-3 py-2 rounded mx-5 mb-2 text-[0.85rem] flex items-start gap-2">
        <span class="flex-1 break-words">{error}</span>
        <button
          class="text-error-fg/70 hover:text-error-fg cursor-pointer leading-none"
          aria-label="dismiss error"
          on:click={() => error = ''}
        >×</button>
      </div>
    {/if}

    <Composer
      bind:input
      bind:attachments
      {busy}
      streaming={!!activeStreamId}
      {cancelling}
      {attachmentsBlocked}
      {ctxUsed}
      {ctxWindow}
      on:send={send}
      on:cancel={cancel}
      on:error={(e) => (error = e.detail)}
    />
  </main>

  <aside class="overflow-hidden min-w-0" aria-hidden={!$settings.showRight}>
    {#if $settings.showRight}
      <SettingsPanel mode="workspace" {providers} {models} {busy} {outputDir} onReset={resetApp} on:modelsRefreshed={(e) => reloadModels(e.detail.provider)} />
    {/if}
  </aside>
</div>

<!--
  Settings dialog — opened by the Margo › Settings… menu (Cmd+,).
  Renders a second SettingsPanel instance; both write to the same
  global $settings store, so changes propagate between the right-pane
  and the dialog instantly.
-->
<div use:melt={$settingsDlgPortalled}>
  {#if $settingsDlgOpen}
    <div use:melt={$settingsDlgOverlay} class="fixed inset-0 z-40 bg-black/40"></div>
    <div
      use:melt={$settingsDlgContent}
      class="fixed left-1/2 top-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-[min(40rem,92vw)] max-h-[85vh] flex flex-col rounded-md border border-border bg-bg shadow-xl overflow-hidden"
    >
      <div class="flex items-center justify-between px-4 py-2.5 border-b border-border">
        <h2 use:melt={$settingsDlgTitle} class="text-[0.95rem] font-semibold text-fg">Settings</h2>
        <button
          use:melt={$settingsDlgClose}
          class="text-fg-muted hover:text-fg cursor-pointer leading-none text-lg"
          aria-label="close settings"
        >×</button>
      </div>
      <div class="flex-1 overflow-y-auto">
        <SettingsPanel mode="global" {providers} {models} {busy} {outputDir} onReset={resetApp} on:modelsRefreshed={(e) => reloadModels(e.detail.provider)} />
      </div>
    </div>
  {/if}
</div>
