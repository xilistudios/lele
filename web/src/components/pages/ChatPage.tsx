import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { ChatPageProvider } from '../../contexts/ChatPageContext'
import { ErrorBanner } from '../atoms/ErrorBanner'
import { ChatComposer } from '../molecules/ChatComposer'
import { ChatHeader } from '../organisms/ChatHeader'
import { DiagnosticsPanel } from '../organisms/DiagnosticsPanel'
import { MessageList } from '../organisms/MessageList'
import { Sidebar } from '../organisms/Sidebar'

export function ChatPage() {
  const { error, diagnosticsOpen, sidebarOpen, onToggleSidebar } = useAppLogicContext()

  return (
    <ChatPageProvider>
      <div className="flex h-screen overflow-hidden bg-[#1a1a1a] text-[#e0e0e0]">
        <Sidebar
          collapsed={!sidebarOpen}
          mobileOpen={sidebarOpen}
          onClose={() => onToggleSidebar()}
        />

        <main className="flex flex-1 flex-col overflow-hidden">
          <div className="flex items-center gap-3 border-b border-[#2e2e2e] px-6 py-3 md:hidden">
            <button
              type="button"
              onClick={onToggleSidebar}
              className="text-[#888] transition-colors hover:text-white"
              aria-label="Toggle sidebar"
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
              >
                <line x1="3" y1="6" x2="21" y2="6" />
                <line x1="3" y1="12" x2="21" y2="12" />
                <line x1="3" y1="18" x2="21" y2="18" />
              </svg>
            </button>
          </div>
          <ChatHeader />

          {error && <ErrorBanner message={error} />}
          {diagnosticsOpen && <DiagnosticsPanel />}

          <div className="flex-1 overflow-y-auto px-6 py-4">
            <MessageList />
          </div>

          <div className="border-t border-[#2e2e2e] px-6 py-4">
            <div className="mx-auto max-w-3xl">
              <ChatComposer />
            </div>
          </div>
        </main>
      </div>
    </ChatPageProvider>
  )
}
