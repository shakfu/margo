<script lang="ts">
  // The conversation transcript: message bubbles, thinking blocks,
  // agent step cards (tool calls, retrieval hits, permission prompts),
  // attachments, and the per-turn usage footer.
  //
  // Extracted from App.svelte. Presentational: it renders what it is
  // given and reports the one interaction it cannot resolve itself, the
  // permission decision, back to the parent that owns the run.
  import { createEventDispatcher } from 'svelte';
  import { mathjax } from './mathjax';
  import { renderMarkdownStreaming } from './markdown';
  import AttachmentThumb from './AttachmentThumb.svelte';
  import type { Message, Usage } from './store';

  export let messages: Message[] = [];
  export let busy = false;
  export let personaLabel = '';
  export let canAlwaysApprove: (name: string) => boolean = () => true;

  // Bound out so the parent can scroll the transcript after a send.
  export let scrollEl: HTMLElement | undefined = undefined;

  const dispatch = createEventDispatcher<{
    permission: { id: string; name: string; decision: 'approve' | 'deny' | 'always' };
  }>();

  // The assistant bubble is labelled with the active persona when one is
  // set, so a chat in a named voice reads as that voice.
  function roleLabel(role: string): string {
    if (role === 'assistant' && personaLabel) return personaLabel.toUpperCase();
    return role.toUpperCase();
  }

  function fmtTokSec(u: Usage): string {
    if (!u.totalMs || !u.outputTokens) return '';
    const tps = (u.outputTokens / (u.totalMs / 1000)).toFixed(1);
    return `${tps} tok/s`;
  }

  function fmtMs(ms: number): string {
    return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
  }
</script>

<section class="flex-1 overflow-y-auto px-5 py-4 flex flex-col gap-4 max-w-[820px] w-full mx-auto box-border" bind:this={scrollEl}>
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
                        on:click={() => dispatch("permission", { id: step.permissionId ?? "", name: step.name, decision: "approve" })}
                      >Approve</button>
                      {#if canAlwaysApprove(step.name)}
                        <button
                          class="px-2 py-0.5 text-[0.75rem] rounded border border-border bg-bg text-fg cursor-pointer hover:bg-hover-bg"
                          on:click={() => dispatch("permission", { id: step.permissionId ?? "", name: step.name, decision: "always" })}
                          title="Auto-approve this tool in future runs"
                        >Always</button>
                      {:else}
                        <span class="text-fg-faint text-[0.72rem]" title="This tool writes files or runs code, so each call is approved on its own.">approved per call</span>
                      {/if}
                      <button
                        class="px-2 py-0.5 text-[0.75rem] rounded border border-error-border bg-error-bg text-error-fg cursor-pointer hover:opacity-90"
                        on:click={() => dispatch("permission", { id: step.permissionId ?? "", name: step.name, decision: "deny" })}
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

<style>

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
