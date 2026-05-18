import { useMemo, useState } from 'react'
import type { ChatSession } from '../lib/types'

export type SortMode = 'recent' | 'name' | 'messages'

export type TimeGroup = {
  key: string
  label: string
  sessions: ChatSession[]
}

function safeDate(value: string | number | undefined): Date | null {
  if (!value) return null
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return null
  if (d.getFullYear() < 1970) return null
  return d
}

function formatMonthYear(d: Date): string {
  const month = d.toLocaleDateString(undefined, { month: 'long' })
  return `${month} ${d.getFullYear()}`
}

export function useChatFilters(allSessions: ChatSession[]) {
  const [query, setQuery] = useState('')
  const [sortMode, setSortMode] = useState<SortMode>('recent')

  const filteredSessions = useMemo(() => {
    // Defensive: drop sessions with invalid dates or zero messages
    let list = allSessions.filter((s) => {
      const d = safeDate(s.updated)
      return d !== null && s.message_count > 0
    })

    if (query.trim()) {
      const q = query.toLowerCase()
      list = list.filter(
        (s) => (s.name ?? '').toLowerCase().includes(q) || s.key.toLowerCase().includes(q),
      )
    }

    switch (sortMode) {
      case 'name':
        list.sort((a, b) => (a.name ?? a.key).localeCompare(b.name ?? b.key))
        break
      case 'messages':
        list.sort((a, b) => b.message_count - a.message_count)
        break
      default:
        list.sort(
          (a, b) => (safeDate(b.updated)?.getTime() ?? 0) - (safeDate(a.updated)?.getTime() ?? 0),
        )
        break
    }

    return list
  }, [allSessions, query, sortMode])

  const grouped: TimeGroup[] = useMemo(() => {
    if (!filteredSessions.length) return []

    const groups = new Map<string, { key: string; label: string; sessions: ChatSession[] }>()
    const now = new Date()
    const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate())
    const yesterdayStart = new Date(todayStart)
    yesterdayStart.setDate(yesterdayStart.getDate() - 1)
    const weekStart = new Date(todayStart)
    weekStart.setDate(weekStart.getDate() - ((weekStart.getDay() + 6) % 7)) // Monday
    const monthStart = new Date(now.getFullYear(), now.getMonth(), 1)

    for (const session of filteredSessions) {
      const updated = safeDate(session.updated)
      if (!updated) continue

      let key: string
      let label: string

      if (updated >= todayStart) {
        key = 'today'
        label = 'chat.today'
      } else if (updated >= yesterdayStart) {
        key = 'yesterday'
        label = 'chat.yesterday'
      } else if (updated >= weekStart) {
        key = 'thisWeek'
        label = 'chat.thisWeek'
      } else if (updated >= monthStart) {
        key = 'thisMonth'
        label = 'chat.thisMonth'
      } else {
        key = `${updated.getFullYear()}-${String(updated.getMonth() + 1).padStart(2, '0')}`
        label = formatMonthYear(updated)
      }

      if (!groups.has(key)) {
        groups.set(key, { key, label, sessions: [] })
      }
      const g = groups.get(key)
      if (g) {
        g.sessions.push(session)
      }
    }

    // Order: today, yesterday, thisWeek, thisMonth, then month keys desc
    const order = ['today', 'yesterday', 'thisWeek', 'thisMonth']
    const result: TimeGroup[] = []

    for (const key of order) {
      const item = groups.get(key)
      if (item) {
        result.push(item)
      }
    }

    const monthKeys = Array.from(groups.keys())
      .filter((k) => !order.includes(k))
      .sort((a, b) => b.localeCompare(a))

    for (const key of monthKeys) {
      const item = groups.get(key)
      if (item) {
        result.push(item)
      }
    }

    return result
  }, [filteredSessions])

  return {
    query,
    setQuery,
    sortMode,
    setSortMode,
    filteredSessions,
    grouped,
  }
}
