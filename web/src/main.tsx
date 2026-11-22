import { QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { ErrorBoundary } from './components/atoms/ErrorBoundary'
import { ThemeProvider } from './contexts/ThemeContext'
import { isDesktop } from './lib/desktop'
import { queryClient } from './lib/queryClient'
import './i18n'
import './styles/index.css'

if (isDesktop()) {
  document.documentElement.classList.add('desktop')
}

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ThemeProvider>
          <ErrorBoundary>
            <App />
          </ErrorBoundary>
        </ThemeProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>,
)
