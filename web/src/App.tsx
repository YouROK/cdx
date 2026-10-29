import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { Button, Text } from '@mantine/core'
import { clearToken, getStatus } from './api/client'
import { setLanguage, t } from './i18n'
import Sidebar from './components/sidebar'
import GeneralPage from './pages/GeneralPage'
import ModelsPage from './pages/ModelsPage'
import PresetsPage from './pages/PresetsPage'
import SessionsPage from './pages/SessionsPage'
import SessionPage from './pages/SessionPage'
import LogsPage from './pages/LogsPage'
import PluginsPage from './pages/PluginsPage'
import MCPPage from './pages/MCPPage'

// App wires up navigation and routes.
export default function App() {
  const status = useQuery({ queryKey: ['status'], queryFn: getStatus, retry: false })
  const [, forceRender] = useState(0)

  // The language comes from the daemon response so the UI and CLI agree.
  useEffect(() => {
    if (status.data?.language) {
      setLanguage(status.data.language)
      forceRender((n) => n + 1)
    }
  }, [status.data?.language])

  // A new link from cdx config only changes the hash, so the tab does not
  // reload by itself. The token in the hash wins over the stored one, so
  // the request has to be repeated.
  useEffect(() => {
    const onHashChange = () => {
      if (window.location.hash.includes('token=')) {
        window.location.reload()
      }
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  if (status.isError) {
    const message = (status.error as Error).message
    return (
      <div className="page" style={{ padding: 24 }}>
        <h1>{t('common.error')}</h1>
        <p style={{ color: 'var(--danger)' }}>{message}</p>
        <Text c="dimmed" size="sm">
          {t('token.hint')}
        </Text>
        <div style={{ marginTop: 16 }}>
          <Button
            onClick={() => {
              clearToken()
              window.location.reload()
            }}
          >
            {t('token.clear')}
          </Button>
        </div>
        <Text c="dimmed" size="sm" mt="md">
          {t('token.manual')}
        </Text>
      </div>
    )
  }

  return (
    <BrowserRouter>
      <div className="app-shell">
        <Sidebar />
        <main style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
          <Routes>
            <Route path="/general" element={<GeneralPage />} />
            <Route path="/models" element={<ModelsPage />} />
            <Route path="/presets" element={<PresetsPage />} />
            <Route path="/sessions" element={<SessionsPage />} />
            <Route path="/sessions/:id" element={<SessionPage />} />
            <Route path="/plugins" element={<PluginsPage />} />
            <Route path="/mcp" element={<MCPPage />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="*" element={<Navigate to="/general" replace />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
