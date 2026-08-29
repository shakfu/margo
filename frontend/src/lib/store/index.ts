// Chat and settings state for the frontend.
//
// This index is the module boundary: components import from `lib/store`,
// never from the submodules, so the internal layout can change without
// touching every consumer.
//
// Load order matters and is enforced by the import graph:
//
//   types -> builtins -> persistence -> stores -> {chats, personas, workspaces}
//
// `./stores` is the only module with import-time side effects (it runs
// the legacy-chat migration, loads settings, and wires the write-back
// subscriptions), so that work happens exactly once and before any
// mutation module can touch a store.
//
// `./persistence` is re-exported selectively. Its keys, id generator,
// and migration helpers need `export` for cross-module use but are not
// part of the contract; only `loadSettings` (for store.test.ts) and
// `chatsKey` cross this boundary.

export * from './types';
export * from './builtins';
export * from './catalog';
export * from './cost';
export * from './stores';
export * from './chats';
export * from './personas';
export * from './workspaces';

export { loadSettings, chatsKey } from './persistence';
