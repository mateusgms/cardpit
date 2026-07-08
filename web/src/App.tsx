import { useEffect, useState } from 'react'
import { NavLink, Route, Routes } from 'react-router-dom'
import Dashboard from './pages/Dashboard'
import History from './pages/History'
import JobDetail from './pages/JobDetail'
import Cards from './pages/Cards'
import Slots from './pages/Slots'
import SettingsPage from './pages/Settings'
import TokenPage from './pages/Token'
import DiagnosticsPage from './pages/Diagnostics'
import { api } from './api/client'
import type { Status } from './api/types'

const tabs = [
  { to: '/', label: 'Painel' },
  { to: '/historico', label: 'Histórico' },
  { to: '/cartoes', label: 'Cartões' },
  { to: '/slots', label: 'Slots' },
  { to: '/config', label: 'Configurações' },
  { to: '/diagnostico', label: 'Diagnóstico' },
]

export default function App() {
  const [version, setVersion] = useState('')

  useEffect(() => {
    api<Status>('/api/status').then((s) => setVersion(s.version)).catch(() => {})
  }, [])

  return (
    <div className="layout">
      <nav className="top">
        <span className="brand">🗂 cardpit</span>
        {tabs.map((t) => (
          <NavLink
            key={t.to}
            to={t.to}
            end={t.to === '/'}
            className={({ isActive }) => 'tab' + (isActive ? ' active' : '')}
          >
            {t.label}
          </NavLink>
        ))}
        {version && <span className="version-badge">{version}</span>}
      </nav>
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/historico" element={<History />} />
        <Route path="/jobs/:id" element={<JobDetail />} />
        <Route path="/cartoes" element={<Cards />} />
        <Route path="/slots" element={<Slots />} />
        <Route path="/config" element={<SettingsPage />} />
        <Route path="/diagnostico" element={<DiagnosticsPage />} />
        <Route path="/token" element={<TokenPage />} />
      </Routes>
    </div>
  )
}
