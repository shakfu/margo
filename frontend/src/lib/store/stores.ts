// The store singletons and their persistence subscriptions.
//
// This is the only module in the store package with import-time side
// effects: it runs the legacy-chat migration, loads settings, and
// wires the write-back subscriptions. Keeping them in one module keeps
// initialisation order deterministic no matter which consumer imports
// first.
//
// Split out of the former monolithic lib/store.ts. Import from
// `lib/store` (the index), not from these modules directly — the index
// is the module boundary consumers depend on.

import { writable, derived, get } from 'svelte/store';
import { migrateLegacyChats, loadSettings, loadChatsForWorkspace, chatsKey, SETTINGS_KEY, uuid } from './persistence';
import { OVERRIDABLE_KEYS, DEFAULT_WORKSPACE_ID, type Chat, type Settings, type Workspace, type WorkspaceOverrides } from './types';

// Settings is loaded first so the chats store can scope to the active
// workspace. Legacy single-workspace chats migrate into the Default
// workspace's key on first run.
migrateLegacyChats();
const initialSettings = loadSettings();

export const settings = writable<Settings>(initialSettings);

// Module-scoped mirror of the active workspace id so the chats
// subscription writes to the correct key without `get(settings)` calls.
// Updated by the settings subscription below.
let currentActiveWorkspaceId = initialSettings.activeWorkspaceId;

// Suppression flag for chats writes during a workspace swap: when
// setActiveWorkspace replaces the chats store contents wholesale, the
// subscription would otherwise overwrite the *new* workspace's stored
// chats with the freshly-loaded list (a no-op write but conceptually
// wrong) — and worse, if the swap raced with a pending update, could
// stomp the wrong key. Set true around the swap; chats subscription
// returns early.
let suppressChatsWrite = false;

export const chats = writable<Chat[]>(loadChatsForWorkspace(currentActiveWorkspaceId));
chats.subscribe(cs => {
  if (suppressChatsWrite) return;
  try { localStorage.setItem(chatsKey(currentActiveWorkspaceId), JSON.stringify(cs)); } catch (_) {}
});

settings.subscribe(s => {
  currentActiveWorkspaceId = s.activeWorkspaceId;
  try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(s)); } catch (_) {}
});

export const activeChatId = writable<string>('');

export const activeChat = derived(
  [chats, activeChatId],
  ([$chats, $id]) => $chats.find(c => c.id === $id) ?? null
);

export const activeWorkspace = derived(
  settings,
  $s => $s.workspaces.find(w => w.id === $s.activeWorkspaceId) ?? $s.workspaces[0],
);

// sessionOverrides is the in-memory override layer used while the
// Default workspace is active. Edits made to settings inputs in the
// right sidebar (Provider/Model/System/Sampling/Thinking) write here
// instead of mutating global Settings. Cleared on app reload — that
// "transient" property is the user-visible signal that you're in
// experiment mode. Non-Default workspaces ignore this layer entirely
// and use Workspace.overrides on disk. (Workspace UX flip.)
export const sessionOverrides = writable<WorkspaceOverrides>({});

// effectiveSettings projects per-scope overrides onto the global
// Settings record:
//   - Default workspace active  → global + sessionOverrides (transient)
//   - Other workspace active    → global + workspace.overrides   (sticky)
// Components that should respect overrides (App.svelte's send(),
// topbar badges, the workspace-mode SettingsPanel) read from this
// store. The Cmd+, dialog (mode='global') keeps reading raw `settings`
// to edit the true global defaults.
export const effectiveSettings = derived(
  [settings, activeWorkspace, sessionOverrides],
  ([$s, $ws, $sess]) => {
    const o: WorkspaceOverrides | undefined =
      $ws?.id === DEFAULT_WORKSPACE_ID ? $sess : $ws?.overrides;
    if (!o || Object.keys(o).length === 0) return $s;
    const out: Settings = { ...$s };
    for (const k of OVERRIDABLE_KEYS) {
      // hasOwnProperty is the right test: an override that is
      // explicitly null/0/'' is still an override (clears the global).
      // Missing key = no override.
      if (Object.prototype.hasOwnProperty.call(o, k)) {
        // TS can't narrow k against Settings union here without a
        // per-key switch; the cast is safe because OVERRIDABLE_KEYS
        // is statically typed as keyof WorkspaceOverrides ⊂ keyof Settings.
        (out as any)[k] = (o as any)[k];
      }
    }
    return out;
  },
);

// setEffectiveOverride routes a write to the right scope: Default
// workspace → sessionOverrides (transient); other workspace →
// Workspace.overrides on disk. Pass undefined to clear (parity with

// activeWorkspaceIdNow reads the module-private mirror of the active
// workspace id. Exposed as a getter, not as a mutable export, so the
// write path stays inside the settings subscription above.
export function activeWorkspaceIdNow(): string {
  return currentActiveWorkspaceId;
}

// withSuppressedChatsWrite runs fn with the chats write-back
// subscription disabled.
//
// setActiveWorkspace replaces the chats store wholesale; without the
// suppression the subscription would write the incoming workspace's
// chats back under the outgoing workspace's key. The flag is restored
// in a finally so a throwing fn cannot leave writes disabled.
export function withSuppressedChatsWrite(fn: () => void): void {
  suppressChatsWrite = true;
  try {
    fn();
  } finally {
    suppressChatsWrite = false;
  }
}
