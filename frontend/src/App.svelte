<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { createDialog, melt } from '@melt-ui/svelte';
  import { Providers, Models, Chat, StreamChat, StreamAgent, CancelStream, Tools, OutputDir, OpenPath, RespondPermission, StartupWorkspaceDir, SetActiveWorkspace, SaveAttachment, ExportChatMarkdown } from '../wailsjs/go/main/App.js';
  import { EventsOn, EventsOff, BrowserOpenURL } from '../wailsjs/runtime/runtime.js';
  import { mathjax } from './lib/mathjax';
  import { renderMarkdownStreaming, setHighlightTheme } from './lib/markdown';
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
    type Usage,
    type AgentStep
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

  // Pending image attachments for the next message. Each entry carries
  // already-base64-encoded bytes so the Wails IPC sees a clean string.
  // Cleared after send. Not persisted in chat history (see §7.4).
  type PendingAttachment = {
    id: string;
    name: string;
    mimeType: string;
    data: string;        // base64
    previewUrl: string;  // blob URL for the thumbnail; revoked on remove
    size: number;        // raw byte count for the size cap
  };
  let attachments: PendingAttachment[] = [];
  // Anthropic / OpenAI vision / OpenRouter VL models all accept JPEG/PNG/
  // WebP/GIF; this is the conservative intersection. PDFs ride on the
  // same path: Anthropic accepts them natively, OpenAI / OpenRouter get
  // a Go-side text extraction (§7.5).
  const IMAGE_MIME = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'];
  const DOCUMENT_MIME = ['application/pdf'];
  const ATTACHMENT_MIME_ACCEPT = [...IMAGE_MIME, ...DOCUMENT_MIME];
  const ATTACHMENT_MAX_IMAGE_BYTES = 10 * 1024 * 1024;     // 10 MB per image
  const ATTACHMENT_MAX_DOC_BYTES = 25 * 1024 * 1024;       // 25 MB per document
  let dragOver = false;
  let fileInputEl: HTMLInputElement | null = null;
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
    Models($effectiveSettings.provider).then(m => { models = m; });
  }

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
  $: ctxPct = ctxWindow > 0 ? Math.min(100, Math.round((ctxUsed / ctxWindow) * 100)) : 0;

  // Slash autocomplete (TODO §9.2). Suggestions populate from the
  // SLASH_COMMANDS catalog while the user is still typing the
  // command word; once they've moved past it ("/persona r…"), we
  // switch to argument-specific suggestions (persona slugs).
  type SlashSuggestion = { text: string; description: string };
  function computeSlashSuggestions(s: string, personas: { name: string }[]): SlashSuggestion[] {
    if (!s.startsWith('/')) return [];
    const sp = s.indexOf(' ');
    if (sp === -1) {
      // Still typing the command word — filter known commands by prefix.
      const pre = s.toLowerCase();
      return SLASH_COMMANDS
        .filter(c => c.command.startsWith(pre))
        .map(c => ({ text: c.command + ' ', description: c.description }));
    }
    const cmd = s.slice(0, sp).toLowerCase();
    const argPrefix = s.slice(sp + 1).toLowerCase();
    if (cmd === '/persona') {
      return personas
        .map(p => ({ slug: slugify(p.name), name: p.name }))
        .filter(p => p.slug.startsWith(argPrefix))
        .map(p => ({ text: `/persona ${p.slug}`, description: p.name }));
    }
    return [];
  }
  $: slashSuggestions = computeSlashSuggestions(input, $settings.personas);
  $: showSlashHint = input.startsWith('/') && slashSuggestions.length > 0;

  function applySuggestion(text: string) {
    input = text;
  }

  onMount(async () => {
    document.documentElement.classList.toggle('dark', $settings.theme === 'dark');
    setHighlightTheme($settings.theme);

    // Populate the model catalog before any UI consumer reads it. The
    // catalog is the source of truth for context-window math and the
    // multimodal allowlist (previously hand-mirrored in store.ts).
    void initModelCatalog();

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

  async function addFiles(files: FileList | File[] | null) {
    if (!files) return;
    for (const file of Array.from(files)) {
      if (!ATTACHMENT_MIME_ACCEPT.includes(file.type)) {
        error = `Unsupported attachment type: ${file.type || file.name}. Allowed: PNG, JPEG, WebP, GIF, PDF.`;
        continue;
      }
      const cap = DOCUMENT_MIME.includes(file.type) ? ATTACHMENT_MAX_DOC_BYTES : ATTACHMENT_MAX_IMAGE_BYTES;
      const capLabel = DOCUMENT_MIME.includes(file.type) ? '25 MB' : '10 MB';
      if (file.size > cap) {
        error = `Attachment "${file.name}" exceeds ${capLabel}.`;
        continue;
      }
      try {
        const data = await fileToBase64(file);
        attachments = [
          ...attachments,
          {
            id: `att-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
            name: file.name,
            mimeType: file.type,
            data,
            previewUrl: URL.createObjectURL(file),
            size: file.size,
          },
        ];
      } catch (e) {
        error = `Failed to read "${file.name}": ${String(e)}`;
      }
    }
  }

  function fileToBase64(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        const result = reader.result as string;
        // result is a "data:<mime>;base64,<payload>" URL — strip the prefix.
        const i = result.indexOf(',');
        resolve(i >= 0 ? result.slice(i + 1) : result);
      };
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    });
  }

  function removeAttachment(id: string) {
    const found = attachments.find(a => a.id === id);
    if (found) URL.revokeObjectURL(found.previewUrl);
    attachments = attachments.filter(a => a.id !== id);
  }

  function clearAttachments() {
    for (const a of attachments) URL.revokeObjectURL(a.previewUrl);
    attachments = [];
  }

  function onPaperclip() {
    fileInputEl?.click();
  }

  function onFileInputChange(ev: Event) {
    const target = ev.currentTarget as HTMLInputElement;
    addFiles(target.files);
    target.value = ''; // reset so the same file can be picked again later
  }

  function onComposerDragOver(ev: DragEvent) {
    if (!ev.dataTransfer || ev.dataTransfer.types.indexOf('Files') < 0) return;
    ev.preventDefault();
    dragOver = true;
  }
  function onComposerDragLeave() { dragOver = false; }
  function onComposerDrop(ev: DragEvent) {
    ev.preventDefault();
    dragOver = false;
    if (ev.dataTransfer?.files) addFiles(ev.dataTransfer.files);
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
    const sendAttachments = attachments.map(a => ({
      name: a.name, mimeType: a.mimeType, data: a.data,
    }));
    clearAttachments();

    if (!$settings.streaming) {
      try {
        const resp = await Chat($effectiveSettings.provider, systemPrompt, history, buildOptions(), sendAttachments);
        appendMessage(chat.id, {
          role: 'assistant',
          content: resp.text,
          thinking: resp.thinking || undefined,
          usage: resp.usage as Usage,
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

    appendMessage(chat.id, { role: 'assistant', content: '' });
    const id = newStreamId();
    activeStreamId = id;
    cancelling = false;
    const base = `margo:stream:${id}`;
    const targetChatId = chat.id;

    EventsOn(`${base}:chunk`, (payload: { kind: string; text?: string; name?: string; arguments?: string; result?: string; isError?: boolean; permissionId?: string; chunk?: string; hits?: Array<{ path: string; doc?: string; score: number; snippet?: string }> }) => {
      switch (payload.kind) {
        case 'thinking':
          appendThinkingToLast(targetChatId, payload.text ?? '');
          break;
        case 'tool_call':
          appendStepToLast(targetChatId, {
            kind: 'tool_call',
            name: payload.name ?? '',
            arguments: payload.arguments ?? '',
          });
          break;
        case 'tool_stream':
          appendStepStream(targetChatId, payload.name ?? '', payload.chunk ?? '');
          break;
        case 'tool_retrieve':
          setStepHits(targetChatId, payload.name ?? '', payload.hits ?? []);
          break;
        case 'tool_result':
          updateLastStepResult(
            targetChatId,
            payload.name ?? '',
            payload.result ?? '',
            !!payload.isError,
          );
          break;
        case 'permission':
          appendStepToLast(targetChatId, {
            kind: 'permission',
            name: payload.name ?? '',
            arguments: payload.arguments ?? '',
            permissionId: payload.permissionId,
            permissionStatus: 'pending',
          });
          break;
        case 'text':
        default:
          appendToLast(targetChatId, payload.text ?? '');
          break;
      }
      scrollToBottom();
    });
    EventsOn(`${base}:error`, (msg: string) => {
      error = msg;
      busy = false;
      activeStreamId = '';
      cancelling = false;
      EventsOff(`${base}:chunk`, `${base}:error`, `${base}:done`);
    });
    EventsOn(`${base}:done`, (payload: { usage: Usage | null }) => {
      if (payload?.usage) setLastUsage(targetChatId, payload.usage);
      busy = false;
      activeStreamId = '';
      cancelling = false;
      EventsOff(`${base}:chunk`, `${base}:error`, `${base}:done`);
      scrollToBottom();
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
      EventsOff(`${base}:chunk`, `${base}:error`, `${base}:done`);
    }
  }

  // Effective persona for the active chat — drives the dynamic
  // assistant-bubble label. When set, the message header reads the
  // persona's name (uppercased) in place of "ASSISTANT".
  $: activePersona = $activeChat
    ? findPersona($settings.personas, $activeChat.personaId)
    : undefined;

  function roleLabel(role: string): string {
    if (role === 'assistant' && activePersona) {
      return activePersona.name.toUpperCase();
    }
    return role.toUpperCase();
  }

  async function respondPermission(
    permissionId: string,
    toolName: string,
    decision: 'approve' | 'deny' | 'always',
  ) {
    const approved = decision !== 'deny';
    const always = decision === 'always';
    if (always) {
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

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  }

  function toggleLeft()  { settings.update(s => ({ ...s, showLeft:  !s.showLeft  })); }
  function toggleRight() { settings.update(s => ({ ...s, showRight: !s.showRight })); }

  function fmtTokSec(u: Usage): string {
    if (!u.totalMs) return '';
    const elapsed = (u.totalMs - u.firstTokenMs) / 1000;
    if (elapsed <= 0) return '';
    return `${(u.outputTokens / elapsed).toFixed(1)} tok/s`;
  }
  function fmtMs(ms: number): string {
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
  }
</script>

<div class="grid h-screen bg-bg text-fg {gridCols}">
  <aside class="overflow-hidden min-w-0" aria-hidden={!$settings.showLeft}>
    {#if $settings.showLeft}
      <ChatList {busy} />
    {/if}
  </aside>

  <main class="flex flex-col min-w-0 h-screen">
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
        {#if $effectiveSettings.thinkEnabled}<span class="badge badge-active">thinking</span>{/if}
        <button
          class="topbar-btn"
          on:click={exportActiveChat}
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

    <section class="flex-1 overflow-y-auto px-5 py-4 flex flex-col gap-4 max-w-[820px] w-full mx-auto box-border" bind:this={messagesEl}>
      {#each messages as m, i (i)}
        <div class="flex flex-col gap-1">
          <div class="text-[0.68rem] uppercase text-fg-faint tracking-wider">{roleLabel(m.role)}</div>
          <div
            class="leading-[1.55] px-4 py-3 rounded-lg text-[0.95rem] {m.role === 'user' ? 'bg-bubble-user' : 'bg-bubble-assistant'}"
          >
            {#if m.role === 'assistant' && m.thinking}
              <details class="thinking-block" open={busy && i === messages.length - 1}>
                <summary>thinking ({m.thinking.length} chars)</summary>
                <div class="thinking-body">{m.thinking}</div>
              </details>
            {/if}
            {#if m.role === 'assistant' && m.steps && m.steps.length > 0}
              <div class="flex flex-col gap-1.5 mb-2">
                {#each m.steps as step}
                  <div class="border border-border rounded-md bg-input-bg overflow-hidden text-[0.78rem] font-[family-name:var(--font-mono)]">
                    <div class="flex items-center gap-2 px-2.5 py-1 border-b border-border bg-bg-elev">
                      <span class="text-fg-muted">{step.kind === 'permission' ? '?' : '→'}</span>
                      <span class="font-semibold text-fg">{step.name}</span>
                      <span class="text-fg-faint truncate flex-1">{step.arguments || '{}'}</span>
                    </div>
                    {#if step.kind === 'permission'}
                      {#if step.permissionStatus === 'pending' && step.permissionId}
                        <div class="px-2.5 py-1.5 flex items-center gap-2 flex-wrap">
                          <span class="text-fg-muted">Allow this tool to run?</span>
                          <button
                            class="px-2 py-0.5 text-[0.75rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
                            on:click={() => respondPermission(step.permissionId ?? '', step.name, 'approve')}
                          >Approve</button>
                          <button
                            class="px-2 py-0.5 text-[0.75rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
                            on:click={() => respondPermission(step.permissionId ?? '', step.name, 'always')}
                            title="Auto-approve this tool in future runs"
                          >Always</button>
                          <button
                            class="px-2 py-0.5 text-[0.75rem] rounded border border-error-border bg-error-bg text-error-fg cursor-pointer hover:opacity-90"
                            on:click={() => respondPermission(step.permissionId ?? '', step.name, 'deny')}
                          >Deny</button>
                        </div>
                      {:else if step.permissionStatus === 'approved'}
                        <div class="px-2.5 py-1.5 text-fg-muted"><span class="text-fg-faint mr-1">✓</span>approved</div>
                      {:else if step.permissionStatus === 'denied'}
                        <div class="px-2.5 py-1.5 text-error-fg"><span class="text-fg-faint mr-1">✗</span>denied</div>
                      {/if}
                    {:else if step.hits && step.hits.length > 0}
                      <ul class="flex flex-col gap-1 px-2.5 py-1.5">
                        {#each step.hits as h, hi (hi)}
                          <li class="border border-border rounded bg-bg px-2 py-1.5">
                            <div class="flex items-baseline gap-2 text-[0.72rem]">
                              <span class="text-fg-faint">{hi + 1}.</span>
                              <a
                                href={`file://${h.path}`}
                                class="font-[family-name:var(--font-mono)] text-fg break-all hover:underline"
                                title="Open source"
                              >{h.doc || h.path}</a>
                              <span class="text-fg-faint ml-auto shrink-0">score {h.score.toFixed(3)}</span>
                            </div>
                            {#if h.snippet}
                              <div class="text-[0.72rem] text-fg-muted mt-0.5 leading-snug whitespace-pre-wrap break-words">{h.snippet}</div>
                            {/if}
                          </li>
                        {/each}
                      </ul>
                    {:else if step.result !== undefined}
                      <div class="px-2.5 py-1.5 {step.isError ? 'text-error-fg' : 'text-fg-muted'} whitespace-pre-wrap break-words">
                        <span class="text-fg-faint mr-1">←</span>{step.result}
                      </div>
                    {:else if step.stream !== undefined}
                      <div class="px-2.5 py-1.5 text-fg-muted whitespace-pre-wrap break-words font-[family-name:var(--font-mono)]">
                        <span class="text-fg-faint mr-1">…</span>{step.stream}{#if busy && i === messages.length - 1}<span class="cursor opacity-60">_</span>{/if}
                      </div>
                    {:else if busy && i === messages.length - 1}
                      <div class="px-2.5 py-1.5 text-fg-faint italic">running…</div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
            {#if m.role === 'user'}
              <div class="md whitespace-pre-wrap">{m.content}</div>
              {#if m.attachments && m.attachments.length > 0}
                <div class="flex flex-wrap gap-2 mt-2">
                  {#each m.attachments as a (a.path)}
                    <AttachmentThumb {a} />
                  {/each}
                </div>
              {:else if m.attachmentCount}
                <div class="text-fg-faint text-[0.72rem] mt-1">
                  📎 {m.attachmentCount} {m.attachmentCount === 1 ? 'attachment' : 'attachments'}
                </div>
              {/if}
            {:else}
              <div class="md" use:mathjax={m.content}>{@html renderMarkdownStreaming(m.content, busy && i === messages.length - 1)}</div>
            {/if}
            {#if busy && i === messages.length - 1 && m.role === 'assistant'}<span class="cursor opacity-60">_</span>{/if}
          </div>
          {#if m.role === 'assistant' && m.usage}
            <div class="msg-footer">
              {#if fmtTokSec(m.usage)}<span>{fmtTokSec(m.usage)}</span>{/if}
              <span>{m.usage.outputTokens} tokens</span>
              {#if m.usage.firstTokenMs > 0}<span>ttft {fmtMs(m.usage.firstTokenMs)}</span>{/if}
              {#if m.usage.totalMs > 0}<span>total {fmtMs(m.usage.totalMs)}</span>{/if}
            </div>
          {/if}
        </div>
      {/each}
      {#if messages.length === 0}
        <div class="m-auto text-center text-fg-faint p-8">
          <div class="text-base text-fg-muted mb-2">Start a conversation</div>
          <div class="text-[0.85rem] max-w-[480px] leading-[1.5]">
            Markdown, code blocks (with syntax highlighting), and math like $\int_0^1 x^2\,dx$ or $$e^{'{i\\pi}'} + 1 = 0$$ all render after the response completes.
          </div>
        </div>
      {/if}
    </section>

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

    <footer
      class="flex flex-col gap-2 px-5 pt-3.5 pb-4 border-t border-border max-w-[820px] w-full mx-auto box-border {dragOver ? 'bg-bubble-user/40' : ''}"
      on:dragover={onComposerDragOver}
      on:dragleave={onComposerDragLeave}
      on:drop={onComposerDrop}
    >
      {#if attachments.length > 0}
        <div class="flex flex-wrap gap-2">
          {#each attachments as a (a.id)}
            {#if a.mimeType.startsWith('image/')}
              <div class="relative group" title="{a.name} ({Math.round(a.size / 1024)} KB)">
                <img src={a.previewUrl} alt={a.name} class="h-14 w-14 object-cover rounded border border-border" />
                <button
                  class="absolute -top-1 -right-1 bg-bg-elev border border-border rounded-full w-4 h-4 flex items-center justify-center text-[0.7rem] leading-none cursor-pointer hover:bg-hover-bg"
                  on:click={() => removeAttachment(a.id)}
                  aria-label="remove attachment"
                >×</button>
              </div>
            {:else}
              <div class="relative group flex items-center gap-2 px-2 py-1.5 border border-border bg-input-bg rounded text-[0.74rem] text-fg-muted" title="{a.name} ({Math.round(a.size / 1024)} KB)">
                <span class="font-[family-name:var(--font-mono)]">📄</span>
                <span class="truncate max-w-[140px]">{a.name}</span>
                <button
                  class="absolute -top-1 -right-1 bg-bg-elev border border-border rounded-full w-4 h-4 flex items-center justify-center text-[0.7rem] leading-none cursor-pointer hover:bg-hover-bg"
                  on:click={() => removeAttachment(a.id)}
                  aria-label="remove attachment"
                >×</button>
              </div>
            {/if}
          {/each}
        </div>
      {/if}
      {#if dragOver}
        <div class="text-[0.74rem] text-fg-muted italic">Drop image to attach…</div>
      {/if}
      {#if attachmentsBlocked}
        <div class="text-[0.74rem] text-error-fg">
          <strong>{$effectiveSettings.model}</strong> doesn't accept images. Switch to a vision-capable model (e.g. claude-sonnet-4-6, gpt-5.4) or remove the attachments to send.
        </div>
      {/if}
      <input
        type="file"
        accept={ATTACHMENT_MIME_ACCEPT.join(',')}
        multiple
        bind:this={fileInputEl}
        on:change={onFileInputChange}
        class="hidden"
      />
      {#if showSlashHint}
        <div class="border border-border rounded-md bg-bg-elev p-1 flex flex-col gap-0.5 text-[0.78rem]">
          {#each slashSuggestions as s (s.text)}
            <button
              type="button"
              class="text-left px-2 py-1 rounded hover:bg-hover-bg flex items-center gap-2 bg-bubble-user"
              on:click={() => applySuggestion(s.text)}
              on:mousedown|preventDefault
            >
              <code class="font-[family-name:var(--font-mono)] text-fg">{s.text.trim()}</code>
              <span class="text-fg-muted truncate">{s.description}</span>
            </button>
          {/each}
        </div>
      {/if}
      <div class="flex items-end gap-2">
      <button
        class="topbar-btn h-9 w-9 flex items-center justify-center"
        on:click={onPaperclip}
        title="Attach image"
        disabled={busy || !$effectiveSettings.provider}
        aria-label="attach image"
      >📎</button>
      <textarea
        class="flex-1 bg-input-bg text-fg border border-border rounded-md px-3 py-2.5 font-[inherit] text-[0.9rem] resize-none outline-none focus:border-border-strong disabled:opacity-50 disabled:cursor-not-allowed"
        placeholder={$effectiveSettings.provider ? "Send a message, or type / for commands…" : "Configure a provider in the settings panel..."}
        bind:value={input}
        on:keydown={onKey}
        disabled={busy || !$effectiveSettings.provider}
        rows="2"
      ></textarea>
      <div class="flex flex-col items-center gap-1">
        <div
          class="ctx-ring"
          title="{ctxUsed.toLocaleString()} / {ctxWindow.toLocaleString()} tokens"
          style="--pct: {ctxPct}"
        >
          <span>{ctxPct}%</span>
        </div>
        {#if busy && activeStreamId}
          <button
            class="composer-btn cancel-btn"
            on:click={cancel}
            disabled={cancelling}
          >{cancelling ? 'cancelling…' : 'cancel'}</button>
        {:else}
          <button
            class="composer-btn send-btn"
            on:click={send}
            disabled={busy || !$effectiveSettings.provider || attachmentsBlocked || (!input.trim() && attachments.length === 0)}
          >{busy ? '...' : 'send'}</button>
        {/if}
      </div>
      </div>
    </footer>
  </main>

  <aside class="overflow-hidden min-w-0" aria-hidden={!$settings.showRight}>
    {#if $settings.showRight}
      <SettingsPanel mode="workspace" {providers} {models} {busy} {outputDir} onReset={resetApp} />
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
        <SettingsPanel mode="global" {providers} {models} {busy} {outputDir} onReset={resetApp} />
      </div>
    </div>
  {/if}
</div>

<style>
  .topbar-btn {
    background: transparent;
    color: var(--fg-muted);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.2rem 0.5rem;
    font-size: 0.85rem;
    line-height: 1;
    cursor: pointer;
    font-family: inherit;
  }
  .topbar-btn:hover { background: var(--hover-bg); color: var(--fg); }

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

  .composer-btn {
    padding: 0 1.1rem;
    min-width: 80px;
    height: 2rem;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .send-btn:hover:not(:disabled) { background: var(--hover-bg); }
  .send-btn:disabled { opacity: 0.4; cursor: not-allowed; }
  .cancel-btn {
    background: var(--error-bg);
    color: var(--error-fg);
    border-color: var(--error-border);
  }
  .cancel-btn:hover { filter: brightness(1.05); }

  .ctx-ring {
    width: 2rem;
    height: 2rem;
    border-radius: 50%;
    background:
      conic-gradient(var(--fg-muted) calc(var(--pct) * 1%), var(--input-bg) 0);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.6rem;
    color: var(--fg-muted);
    font-variant-numeric: tabular-nums;
    position: relative;
  }
  .ctx-ring::before {
    content: '';
    position: absolute;
    inset: 3px;
    background: var(--bg);
    border-radius: 50%;
  }
  .ctx-ring span { position: relative; z-index: 1; }

  .msg-footer {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
    font-size: 0.7rem;
    color: var(--fg-faint);
    padding-top: 0.25rem;
    font-variant-numeric: tabular-nums;
  }

  .thinking-block {
    border: 1px solid var(--border);
    border-radius: 4px;
    margin-bottom: 0.5rem;
    background: var(--input-bg);
  }
  .thinking-block summary {
    cursor: pointer;
    padding: 0.4rem 0.6rem;
    font-size: 0.75rem;
    color: var(--fg-muted);
    user-select: none;
    list-style: none;
  }
  .thinking-block summary::-webkit-details-marker { display: none; }
  .thinking-block summary::before {
    content: '▸ ';
    display: inline-block;
    transition: transform 100ms;
  }
  .thinking-block[open] summary::before {
    content: '▾ ';
  }
  .thinking-body {
    padding: 0 0.6rem 0.5rem;
    font-size: 0.82rem;
    color: var(--fg-muted);
    white-space: pre-wrap;
    line-height: 1.45;
    border-top: 1px solid var(--border);
    padding-top: 0.5rem;
  }

  .cursor { animation: blink 1s steps(2) infinite; }
  @keyframes blink { 50% { opacity: 0; } }

  .md { font-family: var(--font-serif); }
  .md :global(p) { margin: 0.4em 0; }
  .md :global(p:first-child) { margin-top: 0; }
  .md :global(p:last-child) { margin-bottom: 0; }
  .md :global(h1), .md :global(h2), .md :global(h3),
  .md :global(h4), .md :global(h5), .md :global(h6) {
    margin: 0.8em 0 0.4em;
    line-height: 1.25;
  }
  .md :global(h1) { font-size: 1.35em; }
  .md :global(h2) { font-size: 1.2em; }
  .md :global(h3) { font-size: 1.08em; }
  .md :global(ul), .md :global(ol) { margin: 0.4em 0; padding-left: 1.5em; }
  .md :global(li) { margin: 0.15em 0; }
  .md :global(blockquote) {
    border-left: 3px solid var(--border-strong);
    margin: 0.5em 0;
    padding: 0.2em 0.8em;
    color: var(--fg-muted);
  }
  .md :global(a) { color: #3578d1; text-decoration: underline; }
  .md :global(hr) { border: none; border-top: 1px solid var(--border); margin: 1em 0; }
  .md :global(table) { border-collapse: collapse; margin: 0.5em 0; }
  .md :global(th), .md :global(td) { border: 1px solid var(--border); padding: 0.3em 0.6em; }
  .md :global(th) { background: var(--input-bg); }
  .md :global(code) {
    font-family: var(--font-mono);
    font-size: 0.88em;
    background: var(--input-bg);
    padding: 0.1em 0.35em;
    border-radius: 3px;
  }
  .md :global(pre) {
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.7em 0.9em;
    overflow-x: auto;
    margin: 0.5em 0;
  }
  .md :global(pre code) {
    background: transparent;
    padding: 0;
    border-radius: 0;
    font-size: 0.85em;
    line-height: 1.45;
  }
</style>
