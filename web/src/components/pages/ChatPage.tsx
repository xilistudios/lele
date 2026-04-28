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
      <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
        <Sidebar
          collapsed={!sidebarOpen}
          mobileOpen={sidebarOpen}
          onClose={() => onToggleSidebar()}
        />

        <main className="flex flex-1 flex-col overflow-hidden">
          <ChatHeader />

          {error && <ErrorBanner message={error} />}
          {diagnosticsOpen && <DiagnosticsPanel />}

          <div className="flex-1 overflow-y-auto px-6 py-4">
            <MessageList />
          </div>

          <div className="border-t border-border px-6 py-4">
            <div className="mx-auto max-w-3xl">
              <ChatComposer />
            </div>
          </div>
        </main>
      </div>
    </ChatPageProvider>
  )
}
