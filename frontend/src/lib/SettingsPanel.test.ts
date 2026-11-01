// Renders the real panel to check tier gating and open-tab recovery.
import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { get } from 'svelte/store';
import SettingsPanel from './SettingsPanel.svelte';
import { settings } from './store';

vi.mock('../../wailsjs/go/main/App.js', () => new Proxy({}, {
  get: (_t, name) => (name === 'then' ? undefined : vi.fn(async () => [])),
  has: () => true,
}));

let panel: Record<string, unknown> | undefined;

function render(mode: 'workspace' | 'global') {
  panel = mount(SettingsPanel, { target: document.body, props: { mode } });
  flushSync();
}
const tabs = () => [...document.querySelectorAll('.tab-trigger')].map(t => t.textContent);
const heads = () => [...document.querySelectorAll('.section-head')].map(h => h.textContent?.replace(/[^\x20-\x7e]/g, '').trim());
const activeTab = () => document.querySelector('.tab-trigger[data-state="active"]')?.textContent;
const openTab = (name: string) => {
  const tab = [...document.querySelectorAll<HTMLElement>('.tab-trigger')].find(t => t.textContent === name)!;
  tab.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  flushSync();
};
const setTier = (uiTier: 'novice' | 'standard' | 'expert', autoApproveTools: string[] = []) => {
  settings.update(s => ({ ...s, uiTier, autoApproveTools }));
  flushSync();
};

beforeEach(() => setTier('expert'));
afterEach(() => {
  if (panel) unmount(panel);
  panel = undefined;
  document.body.innerHTML = '';
});

describe('SettingsPanel tier gates', () => {
  test('expert shows every section and tab', () => {
    render('workspace');
    expect(tabs()).toEqual(['Models', 'Roles', 'MCP']);
    expect(heads()).toEqual(expect.arrayContaining(['Sampling', 'Thinking']));
    expect(document.body.textContent).toContain('Stop sequences');
  });

  test('standard hides stop sequences and the MCP tab', () => {
    setTier('standard');
    render('workspace');
    expect(tabs()).toEqual(['Models', 'Roles']);
    expect(heads()).toContain('Sampling');
    expect(document.body.textContent).not.toContain('Stop sequences');
  });

  test('novice hides sampling and thinking', () => {
    setTier('novice');
    render('workspace');
    expect(heads()).not.toContain('Sampling');
    expect(heads()).not.toContain('Thinking');
    expect(heads()).toContain('System Prompt');
  });

  test('novice global panel drops the Roles tab when nothing is trusted', () => {
    setTier('novice');
    render('global');
    expect(tabs()).toEqual(['Models', 'General']);
  });

  test('novice can still revoke existing trusted tools', () => {
    setTier('novice', ['shell']);
    render('global');
    expect(tabs()).toEqual(['Models', 'Roles', 'General']);
    // The section head also carries the entry count.
    expect(heads().some(h => h?.startsWith('Trusted tools'))).toBe(true);
  });

  test('the General tab select changes the tier', () => {
    render('global');
    openTab('General');
    const select = document.querySelector('select')!;
    select.value = 'novice';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(get(settings).uiTier).toBe('novice');
    expect(tabs()).toEqual(['Models', 'General']);
  });

  test('losing the open MCP tab falls back to Models', () => {
    render('workspace');
    openTab('MCP');
    expect(activeTab()).toBe('MCP');
    setTier('standard');
    expect(activeTab()).toBe('Models');
  });

  test('revoking the last trusted tool as novice leaves the Roles tab', () => {
    setTier('novice', ['shell']);
    render('global');
    openTab('Roles');
    expect(activeTab()).toBe('Roles');
    settings.update(s => ({ ...s, autoApproveTools: [] }));
    flushSync();
    expect(activeTab()).toBe('Models');
    expect(get(settings).uiTier).toBe('novice');
  });
});
