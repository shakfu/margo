// Shared write-routing helper for settings subcomponents.
//
// Settings inputs render in two scopes:
//   - 'workspace' (right sidebar): writes route via setEffectiveOverride
//     so the active workspace's overrides table picks them up.
//   - 'global' (Cmd+, dialog): writes route directly to the settings
//     store, which is the baseline new workspaces seed from.
//
// Subcomponents close over `mode` at the top of their script and pass it
// to writeKey() for each input. Keeping this in one module instead of
// duplicating per file means a future scope ('per-chat') is a one-place
// change.
import { settings, setEffectiveOverride, type WorkspaceOverrides } from '../store';

export type SettingsScope = 'workspace' | 'global';

export function writeKey<K extends keyof WorkspaceOverrides>(
  mode: SettingsScope,
  key: K,
  value: WorkspaceOverrides[K],
): void {
  if (mode === 'workspace') {
    setEffectiveOverride(key, value);
  } else {
    settings.update((s) => ({ ...s, [key]: value }));
  }
}
