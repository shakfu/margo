<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Providers, Chat, StreamChat, CancelStream } from '../wailsjs/go/main/App.js';
  import { EventsOn, EventsOff } from '../wailsjs/runtime/runtime.js';
  import { mathjax } from './lib/mathjax';
  import { renderMarkdown, setHighlightTheme } from './lib/markdown';
  import {
    chats,
    activeChat,
    activeChatId,
    settings,
    newChat,
    appendMessage,
    appendToLast
  } from './lib/store';
  import ChatList from './lib/ChatList.svelte';
  import SettingsPanel from './lib/SettingsPanel.svelte';

  let providers: string[] = [];
  let input = '';
  let busy = false;
  let error = '';
  let activeStreamId = '';
  let messagesEl: HTMLElement;

  $: messages = $activeChat?.messages ?? [];

  onMount(async () => {
    document.documentElement.classList.toggle('dark', $settings.theme === 'dark');
    setHighlightTheme($settings.theme);

    try {
      providers = await Providers();
      if (providers.length > 0 && !$settings.provider) {
        settings.update(s => ({ ...s, provider: providers[0] }));
      } else if (providers.length === 0) {
        error = 'No providers configured. Set ANTHROPIC_API_KEY or OPENAI_API_KEY in .env and restart.';
      }
    } catch (e) {
      error = String(e);
    }

    if ($chats.length > 0 && !$activeChatId) {
      activeChatId.set($chats[0].id);
    }
  });

  function newStreamId(): string {
    const c = (window as any).crypto;
    if (c?.randomUUID) return c.randomUUID();
    return `s-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  }

  async function scrollToBottom() {
    await tick();
    if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
  }

  async function send() {
    const text = input.trim();
    if (!text || busy || !$settings.provider) return;

    if (!$activeChat) newChat();
    const chat = $activeChat;
    if (!chat) return;

    input = '';
    error = '';
    busy = true;

    appendMessage(chat.id, { role: 'user', content: text });
    const history = ($activeChat?.messages ?? []).map(m => ({
      role: m.role,
      content: m.content
    }));
    scrollToBottom();

    if (!$settings.streaming) {
      try {
        const reply = await Chat($settings.provider, $settings.system, history);
        appendMessage(chat.id, { role: 'assistant', content: reply });
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
    const base = `margo:stream:${id}`;
    const targetChatId = chat.id;

    EventsOn(`${base}:chunk`, (delta: string) => {
      appendToLast(targetChatId, delta);
      scrollToBottom();
    });
    EventsOn(`${base}:error`, (msg: string) => {
      error = msg;
      busy = false;
      activeStreamId = '';
      EventsOff(`${base}:chunk`, `${base}:error`, `${base}:done`);
    });
    EventsOn(`${base}:done`, () => {
      busy = false;
      activeStreamId = '';
      EventsOff(`${base}:chunk`, `${base}:error`, `${base}:done`);
      scrollToBottom();
    });

    try {
      await StreamChat(id, $settings.provider, $settings.system, history);
    } catch (e) {
      error = String(e);
      busy = false;
      activeStreamId = '';
      EventsOff(`${base}:chunk`, `${base}:error`, `${base}:done`);
    }
  }

  async function cancel() {
    if (!activeStreamId) return;
    try { await CancelStream(activeStreamId); } catch (_) {}
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  }

  function toggleLeft()  { settings.update(s => ({ ...s, showLeft:  !s.showLeft  })); }
  function toggleRight() { settings.update(s => ({ ...s, showRight: !s.showRight })); }
</script>

<div class="app" class:left-hidden={!$settings.showLeft} class:right-hidden={!$settings.showRight}>
  {#if $settings.showLeft}
    <aside class="left">
      <ChatList {busy} />
    </aside>
  {/if}

  <main class="center">
    <header class="topbar">
      <button class="toggle" on:click={toggleLeft} title={$settings.showLeft ? 'Hide chats' : 'Show chats'}>
        {$settings.showLeft ? '⟨' : '⟩'}
      </button>
      <div class="title">
        {$activeChat?.title ?? 'margo'}
      </div>
      <div class="topbar-right">
        <span class="provider-badge">{$settings.provider || 'no provider'}</span>
        <button class="toggle" on:click={toggleRight} title={$settings.showRight ? 'Hide settings' : 'Show settings'}>
          {$settings.showRight ? '⟩' : '⟨'}
        </button>
      </div>
    </header>

    <section class="messages" bind:this={messagesEl}>
      {#each messages as m, i (i)}
        <div class="msg msg-{m.role}">
          <div class="role">{m.role}</div>
          <div class="content">
            {#if m.role === 'user'}
              <div class="md plain">{m.content}</div>
            {:else}
              <div class="md" use:mathjax={m.content}>{@html renderMarkdown(m.content)}</div>
            {/if}
            {#if busy && i === messages.length - 1 && m.role === 'assistant'}<span class="cursor">_</span>{/if}
          </div>
        </div>
      {/each}
      {#if messages.length === 0}
        <div class="empty">
          <div class="empty-title">Start a conversation</div>
          <div class="empty-sub">
            Markdown, code blocks (with syntax highlighting), and math like $\int_0^1 x^2\,dx$ or $$e^{'{i\\pi}'} + 1 = 0$$ all render after the response completes.
          </div>
        </div>
      {/if}
    </section>

    {#if error}<div class="error">{error}</div>{/if}

    <footer class="composer">
      <textarea
        class="input"
        placeholder={$settings.provider ? "Send a message... (Enter to send, Shift+Enter for newline)" : "Configure a provider in the settings panel..."}
        bind:value={input}
        on:keydown={onKey}
        disabled={busy || !$settings.provider}
        rows="2"
      ></textarea>
      {#if busy && activeStreamId}
        <button class="btn-cancel" on:click={cancel}>cancel</button>
      {:else}
        <button class="btn-send" on:click={send} disabled={busy || !$settings.provider || !input.trim()}>
          {busy ? '...' : 'send'}
        </button>
      {/if}
    </footer>
  </main>

  {#if $settings.showRight}
    <aside class="right">
      <SettingsPanel {providers} {busy} />
    </aside>
  {/if}
</div>

<style>
  .app {
    display: grid;
    grid-template-columns: 280px 1fr 300px;
    height: 100vh;
    background: var(--bg);
    color: var(--fg);
  }
  .app.left-hidden  { grid-template-columns: 0 1fr 300px; }
  .app.right-hidden { grid-template-columns: 280px 1fr 0; }
  .app.left-hidden.right-hidden { grid-template-columns: 0 1fr 0; }

  aside.left, aside.right {
    overflow: hidden;
    min-width: 0;
  }

  .center {
    display: flex;
    flex-direction: column;
    min-width: 0;
    height: 100vh;
  }

  .topbar {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.85rem;
    border-bottom: 1px solid var(--border);
    background: var(--bg);
  }
  .topbar .title {
    flex: 1;
    text-align: center;
    font-size: 0.9rem;
    font-weight: 500;
    color: var(--fg-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .topbar-right {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .provider-badge {
    font-size: 0.72rem;
    color: var(--fg-faint);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    background: var(--input-bg);
    padding: 0.2rem 0.5rem;
    border-radius: 3px;
    border: 1px solid var(--border);
  }
  .toggle {
    background: transparent;
    color: var(--fg-muted);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.2rem 0.5rem;
    font-size: 0.85rem;
    cursor: pointer;
    font-family: inherit;
    line-height: 1;
  }
  .toggle:hover { background: var(--hover-bg); color: var(--fg); }

  .messages {
    flex: 1;
    overflow-y: auto;
    padding: 1rem 1.2rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
    max-width: 820px;
    width: 100%;
    margin: 0 auto;
    box-sizing: border-box;
  }

  .msg { display: flex; flex-direction: column; gap: 0.25rem; }
  .role {
    font-size: 0.68rem;
    text-transform: uppercase;
    color: var(--fg-faint);
    letter-spacing: 0.05em;
  }
  .content {
    line-height: 1.55;
    padding: 0.7rem 0.95rem;
    border-radius: 8px;
    font-size: 0.95rem;
  }
  .msg-user .content { background: var(--bubble-user); }
  .msg-assistant .content { background: var(--bubble-assistant); }
  .cursor { animation: blink 1s steps(2) infinite; opacity: 0.6; }
  @keyframes blink { 50% { opacity: 0; } }

  .md.plain { white-space: pre-wrap; }
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
    font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
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

  .empty {
    margin: auto;
    text-align: center;
    color: var(--fg-faint);
    padding: 2rem;
  }
  .empty-title { font-size: 1rem; color: var(--fg-muted); margin-bottom: 0.5rem; }
  .empty-sub { font-size: 0.85rem; max-width: 480px; line-height: 1.5; }

  .error {
    background: var(--error-bg);
    color: var(--error-fg);
    border: 1px solid var(--error-border);
    padding: 0.5rem 0.75rem;
    border-radius: 4px;
    margin: 0 1.2rem 0.5rem;
    font-size: 0.85rem;
  }

  .composer {
    display: flex;
    gap: 0.5rem;
    padding: 0.85rem 1.2rem 1rem;
    border-top: 1px solid var(--border);
    max-width: 820px;
    width: 100%;
    margin: 0 auto;
    box-sizing: border-box;
  }
  .composer .input {
    flex: 1;
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.6rem 0.75rem;
    font-family: inherit;
    font-size: 0.9rem;
    resize: none;
    outline: none;
  }
  .composer .input:focus { border-color: var(--border-strong); }
  .composer .input:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-send, .btn-cancel {
    padding: 0 1.3rem;
    min-width: 90px;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .btn-send:hover:not(:disabled) { background: var(--hover-bg); }
  .btn-send:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-cancel {
    background: var(--error-bg);
    color: var(--error-fg);
    border-color: var(--error-border);
  }
  .btn-cancel:hover { filter: brightness(1.05); }
</style>
