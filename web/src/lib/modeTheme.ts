import type { ComponentType } from 'react'
import { AgentsIcon, ChatBubbleIcon, GroupsIcon } from '../components/atoms/Icons'
import type { ChatMode } from './types'

export type ModeTheme = {
  id: ChatMode
  labelKey: string
  descKey: string
  Icon: ComponentType<{ size?: number; className?: string }>
  text: string
  dot: string
  softBg: string
  border: string
  chip: string
  tabActive: string
  selectedItem: string
  accentBar: string
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
    tabActive: 'bg-mode-chat/10 text-text-primary dark:text-mode-chat border-b-2 border-mode-chat',
    selectedItem:
      'bg-mode-chat/14 text-text-primary dark:text-mode-chat border border-mode-chat/40',
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
    tabActive: 'bg-mode-agent/10 text-text-primary border-b-2 border-mode-agent',
    selectedItem: 'bg-mode-agent/14 text-mode-agent border border-mode-agent/40',
    accentBar: 'bg-mode-agent/60',
    iconCircle: 'bg-mode-agent/10 text-mode-agent',
  },
  group: {
    id: 'group',
    labelKey: 'mode.group',
    descKey: 'mode.groupDescription',
    Icon: GroupsIcon,
    text: 'text-text-primary dark:text-mode-group',
    dot: 'bg-mode-group',
    softBg: 'bg-mode-group/10',
    border: 'border-mode-group/30',
    chip: 'bg-mode-group/10 border border-mode-group/30 text-text-primary dark:text-mode-group',
    tabActive:
      'bg-mode-group/10 text-text-primary dark:text-mode-group border-b-2 border-mode-group',
    selectedItem:
      'bg-mode-group/14 text-text-primary dark:text-mode-group border border-mode-group/40',
    accentBar: 'bg-mode-group/60',
    iconCircle: 'bg-mode-group/10 text-text-primary dark:text-mode-group',
  },
}

export function getModeTheme(mode: ChatMode | undefined | null): ModeTheme {
  return THEMES[mode ?? 'agent']
}

export const ALL_MODES: ChatMode[] = ['chat', 'agent', 'group']

export const MODE_LIST: ChatMode[] = ALL_MODES

export function getModeList(groupsEnabled: boolean): ChatMode[] {
  return groupsEnabled ? ALL_MODES : ALL_MODES.filter((m) => m !== 'group')
}
