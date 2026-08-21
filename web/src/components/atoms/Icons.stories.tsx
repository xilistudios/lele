import type { Meta, StoryObj } from '@storybook/react'
import {
  ChatBubbleIcon,
  CheckIcon,
  ChevronLeftIcon,
  ClockIcon,
  CloseIcon,
  CodeIcon,
  CopyIcon,
  DatabaseIcon,
  EditIcon,
  EyeIcon,
  FilterIcon,
  GroupsIcon,
  HistoryIcon,
  LockIcon,
  LogoutIcon,
  MoreIcon,
  PlusCircleIcon,
  PlusIcon,
  ProvidersIcon,
  SearchIcon,
  ServerIcon,
  SettingsIcon,
  SidebarToggleIcon,
  SkillsIcon,
  SubagentsIcon,
  TerminalIcon,
  TrashIcon,
  UserIcon,
} from './Icons'

const meta = {
  title: 'Atoms/Icons',
  tags: ['autodocs'],
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta

export default meta
type Story = StoryObj<typeof meta>

const ALL_ICONS = [
  { name: 'CloseIcon', Icon: CloseIcon },
  { name: 'SubagentsIcon', Icon: SubagentsIcon },
  { name: 'GroupsIcon', Icon: GroupsIcon },
  { name: 'ServerIcon', Icon: ServerIcon },
  { name: 'SidebarToggleIcon', Icon: SidebarToggleIcon },
  { name: 'ChevronLeftIcon', Icon: ChevronLeftIcon },
  { name: 'PlusIcon', Icon: PlusIcon },
  { name: 'SettingsIcon', Icon: SettingsIcon },
  { name: 'LogoutIcon', Icon: LogoutIcon },
  { name: 'EditIcon', Icon: EditIcon },
  { name: 'ChatBubbleIcon', Icon: ChatBubbleIcon },
  { name: 'TrashIcon', Icon: TrashIcon },
  { name: 'DatabaseIcon', Icon: DatabaseIcon },
  { name: 'AgentsIcon', Icon: SubagentsIcon },
  { name: 'ProvidersIcon', Icon: ProvidersIcon },
  { name: 'SkillsIcon', Icon: SkillsIcon },
  { name: 'PlusCircleIcon', Icon: PlusCircleIcon },
  { name: 'MoreIcon', Icon: MoreIcon },
  { name: 'HistoryIcon', Icon: HistoryIcon },
  { name: 'FilterIcon', Icon: FilterIcon },
  { name: 'SearchIcon', Icon: SearchIcon },
  { name: 'UserIcon', Icon: UserIcon },
  { name: 'CopyIcon', Icon: CopyIcon },
  { name: 'CheckIcon', Icon: CheckIcon },
  { name: 'CodeIcon', Icon: CodeIcon },
  { name: 'TerminalIcon', Icon: TerminalIcon },
  { name: 'ClockIcon', Icon: ClockIcon },
  { name: 'LockIcon', Icon: LockIcon },
  { name: 'EyeIcon', Icon: EyeIcon },
]

export const Gallery: Story = {
  args: {},
  render: () => (
    <div className="p-8">
      <h1 className="mb-6 text-lg font-semibold text-text-primary">Icon Gallery</h1>
      <div className="grid grid-cols-4 gap-4 sm:grid-cols-6 md:grid-cols-8">
        {ALL_ICONS.map(({ name, Icon }) => (
          <div
            key={name}
            className="flex flex-col items-center gap-2 rounded-lg border border-border bg-background-secondary p-4"
          >
            <Icon size={24} />
            <span className="text-[10px] text-text-tertiary">{name}</span>
          </div>
        ))}
      </div>
    </div>
  ),
}

export const Sizes: Story = {
  args: {},
  render: () => (
    <div className="flex flex-col gap-4 p-8">
      <h1 className="text-lg font-semibold text-text-primary">Icon Sizes</h1>
      <div className="flex items-center gap-6">
        {[14, 16, 20, 24, 32, 48].map((size) => (
          <div key={size} className="flex flex-col items-center gap-2">
            <PlusIcon size={size} />
            <span className="text-[10px] text-text-tertiary">{size}px</span>
          </div>
        ))}
      </div>
    </div>
  ),
}
