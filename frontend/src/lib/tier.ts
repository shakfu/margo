// Tier visibility table for `Settings.uiTier`. A variant changes what
// margo shows by shipping a different default tier or editing this table.
import type { UiTier } from './store/types';

const RANK: Record<UiTier, number> = { novice: 0, standard: 1, expert: 2 };

// Lowest tier that shows each gated control.
export const MIN_TIER = {
  sampling: 'standard',
  thinking: 'standard',
  trustedTools: 'standard',
  stopSequences: 'expert',
  mcpServers: 'expert',
  agentRunners: 'expert',
} as const satisfies Record<string, UiTier>;

export type GatedControl = keyof typeof MIN_TIER;

export function tierAllows(tier: UiTier, control: GatedControl): boolean {
  return RANK[tier] >= RANK[MIN_TIER[control]];
}
