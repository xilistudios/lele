import { QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { queryClient } from './lib/queryClient'
import './i18n'
import './styles/index.css'

if ('serviceWorker' in navigator && import.meta.env.PROD) {
  /**window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => undefined)
})**/
}

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>,
)
