import type { ComponentType } from 'react'
import { AgentsIcon, ChatBubbleIcon, GroupsIcon } from '../components/atoms/Icons'
import type { ChatMode } from './types'

export type ModeTheme = {
  id: ChatMode
  labelKey: string
  descKey: string
  Icon: ComponentType<{ size?: number; className?: string }>
  /** identity text color, e.g. 'text-mode-chat' (§2.4) */
  text: string
  /** bg for dots/indicators, e.g. 'bg-mode-chat' (§2.4) */
  dot: string
  /** soft translucent bg, e.g. 'bg-mode-chat/14' (§2.4) */
  softBg: string
  /** translucent border, e.g. 'border-mode-chat/30' (§2.4) */
  border: string
  /** full class string for the header mode chip */
  chip: string
  /** active mode tab: tint+text+underline, never solid fill (§2.4/§5.7) */
  tabActive: string
  /** full class string for a selected session item in the sidebar */
  selectedItem: string
  /** full class string for the composer top accent bar */
  accentBar: string
  /** full class string for the empty-state icon circle */
  iconCircle: string
}

const THEMES: Record<ChatMode, ModeTheme> = {
  chat: {
    id: 'chat',
    labelKey: 'mode.chat',
    descKey: 'mode.chatDescription',
    Icon: ChatBubbleIcon,
    text: 'text-mode-chat',
    dot: 'bg-mode-chat',
    softBg: 'bg-mode-chat/10',
    border: 'border-mode-chat/30',
    chip: 'bg-mode-chat/10 border border-mode-chat/30 text-mode-chat',
    tabActive: 'text-mode-chat border-b-2 border-mode-chat',
    // §2.4 excepción extendida (F1): chat claro sobre tinte/14 = 4.498 < 4.5 → texto primary en claro
    selectedItem: 'bg-mode-chat/14 text-text-primary dark:text-mode-chat border border-mode-chat/40',
    accentBar: 'bg-mode-chat/60',
    iconCircle: 'bg-mode-chat/10 text-mode-chat',
  },
  agent: {
    id: 'agent',
    labelKey: 'mode.agent',
    descKey: 'mode.agentDescription',
    Icon: AgentsIcon,
    text: 'text-mode-agent',
    dot: 'bg-mode-agent',
    softBg: 'bg-mode-agent/10',
    border: 'border-mode-agent/30',
    chip: 'bg-mode-agent/10 border border-mode-agent/30 text-mode-agent',
    tabActive: 'text-mode-agent border-b-2 border-mode-agent',
    selectedItem: 'bg-mode-agent/14 text-mode-agent border border-mode-agent/40',
    accentBar: 'bg-mode-agent/60',
    iconCircle: 'bg-mode-agent/10 text-mode-agent',
  },
  group: {
    id: 'group',
    labelKey: 'mode.group',
    descKey: 'mode.groupDescription',
    Icon: GroupsIcon,
    text: 'text-mode-group',
    dot: 'bg-mode-group',
    softBg: 'bg-mode-group/10',
    border: 'border-mode-group/30',
    // §2.4 group-claro: 4.48 sobre tinte/10 → texto primary en claro, color en borde
    chip: 'bg-mode-group/10 border border-mode-group/30 text-text-primary dark:text-mode-group',
    tabActive: 'text-mode-group border-b-2 border-mode-group',
    // §2.4 excepción group-claro: #C2410C sobre su tinte = 4.48 (<4.5) → el texto del
    // item seleccionado usa text-primary en claro; el color queda en dot/borde.
    selectedItem: 'bg-mode-group/14 text-text-primary dark:text-mode-group border border-mode-group/40',
    accentBar: 'bg-mode-group/60',
    iconCircle: 'bg-mode-group/10 text-text-primary dark:text-mode-group',
  },
}

export function getModeTheme(mode: ChatMode | undefined | null): ModeTheme {
  return THEMES[mode ?? 'agent']
}

export const ALL_MODES: ChatMode[] = ['chat', 'agent', 'group']

/** @deprecated Use getModeList() instead for feature-flag-aware filtering */
export const MODE_LIST: ChatMode[] = ALL_MODES

export function getModeList(groupsEnabled: boolean): ChatMode[] {
  return groupsEnabled ? ALL_MODES : ALL_MODES.filter((m) => m !== 'group')
}
