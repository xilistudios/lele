import type { ComponentType } from 'react'
import { AgentsIcon, ChatBubbleIcon, GroupsIcon } from '../components/atoms/Icons'
import type { ChatMode } from './types'

export type ModeTheme = {
  id: ChatMode
  labelKey: string
  descKey: string
  Icon: ComponentType<{ size?: number; className?: string }>
  /** solid text color, e.g. 'text-brand-turquesa' */
  text: string
  /** solid bg for dots/indicators, e.g. 'bg-brand-turquesa' */
  dot: string
  /** soft translucent bg, e.g. 'bg-brand-turquesa/10' */
  softBg: string
  /** translucent border, e.g. 'border-brand-turquesa/30' */
  border: string
  /** full class string for the header mode chip */
  chip: string
  /** full class string for an active mode tab */
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
    text: 'text-brand-turquesa',
    dot: 'bg-brand-turquesa',
    softBg: 'bg-brand-turquesa/10',
    border: 'border-brand-turquesa/30',
    chip: 'bg-brand-turquesa/10 border border-brand-turquesa/30 text-brand-turquesa',
    tabActive: 'bg-brand-turquesa/15 text-brand-turquesa shadow-sm',
    selectedItem: 'bg-surface-selected text-brand-turquesa border border-brand-turquesa/30',
    accentBar: 'bg-brand-turquesa/60',
    iconCircle: 'bg-brand-turquesa/10 text-brand-turquesa',
  },
  agent: {
    id: 'agent',
    labelKey: 'mode.agent',
    descKey: 'mode.agentDescription',
    Icon: AgentsIcon,
    text: 'text-brand-morado',
    dot: 'bg-brand-morado',
    softBg: 'bg-brand-morado/10',
    border: 'border-brand-morado/30',
    chip: 'bg-brand-morado/10 border border-brand-morado/30 text-brand-morado',
    tabActive: 'bg-brand-morado/15 text-brand-morado shadow-sm',
    selectedItem: 'bg-surface-selected text-brand-morado border border-brand-morado/30',
    accentBar: 'bg-brand-morado/60',
    iconCircle: 'bg-brand-morado/10 text-brand-morado',
  },
  group: {
    id: 'group',
    labelKey: 'mode.group',
    descKey: 'mode.groupDescription',
    Icon: GroupsIcon,
    text: 'text-brand-naranja',
    dot: 'bg-brand-naranja',
    softBg: 'bg-brand-naranja/10',
    border: 'border-brand-naranja/30',
    chip: 'bg-brand-naranja/10 border border-brand-naranja/30 text-brand-naranja',
    tabActive: 'bg-brand-naranja/15 text-brand-naranja shadow-sm',
    selectedItem: 'bg-surface-selected text-brand-naranja border border-brand-naranja/30',
    accentBar: 'bg-brand-naranja/60',
    iconCircle: 'bg-brand-naranja/10 text-brand-naranja',
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
