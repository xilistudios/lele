import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { ChatPageProvider } from '../../contexts/ChatPageContext'
import { ErrorBanner } from '../atoms/ErrorBanner'
import { ChatComposer } from '../molecules/ChatComposer'
import { GroupComposer } from '../molecules/GroupComposer'
import { ChatHeader } from '../organisms/ChatHeader'
import { DiagnosticsPanel } from '../organisms/DiagnosticsPanel'
import { MessageList } from '../organisms/MessageList'
import { Sidebar } from '../organisms/Sidebar'

export function ChatPage() {
  const {
    error,
    diagnosticsOpen,
    sidebarOpen,
    mobileSidebarOpen,
    chatMode,
    parentSessionKey,
    onCloseMobileSidebar,
  } = useAppLogicContext()

  return (
    <ChatPageProvider>
      <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
        <Sidebar
          collapsed={!sidebarOpen}
          mobileOpen={mobileSidebarOpen}
          onClose={() => onCloseMobileSidebar()}
        />

        <main className="flex flex-1 flex-col overflow-hidden">
          <ChatHeader />

          {error && <ErrorBanner message={error} />}
          {diagnosticsOpen && <DiagnosticsPanel />}

          <div className="flex-1 overflow-hidden px-4 py-3 md:px-6 md:py-4">
            <MessageList />
          </div>

          {!parentSessionKey && (
            <div className="border-t border-border px-4 py-3 md:px-6 md:py-4">
              <div className="mx-auto max-w-3xl">
                {chatMode === 'group' ? <GroupComposer /> : <ChatComposer />}
              </div>
            </div>
          )}
        </main>
      </div>
    </ChatPageProvider>
  )
}
