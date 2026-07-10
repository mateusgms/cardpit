import { useEffect, useMemo, useState } from 'react'
import { api, fmtDate } from '../api/client'
import type { Diagnostics, LogRecord } from '../api/types'

const LEVELS = ['debug', 'info', 'warn', 'error'] as const
type Level = (typeof LEVELS)[number]

const rank: Record<string, number> = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 }
const badgeClass: Record<string, string> = {
  DEBUG: 'cancelled',
  INFO: 'copying',
  WARN: 'pending',
  ERROR: 'failed',
}

function attrsText(attrs?: Record<string, unknown>): string {
  if (!attrs) return ''
  return Object.entries(attrs)
    .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
    .join('  ')
}

export default function DiagnosticsPage() {
  const [diag, setDiag] = useState<Diagnostics | null>(null)
  const [logs, setLogs] = useState<LogRecord[]>([])
  const [filter, setFilter] = useState<Level>('info')
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')

  const debugOn = diag?.log_level === 'debug'

  const loadDiag = () => api<Diagnostics>('/api/diagnostics').then(setDiag).catch(() => {})
  const loadLogs = () =>
    api<{ records: LogRecord[] | null }>('/api/logs?limit=1000')
      .then((r) => setLogs(r.records ?? []))
      .catch(() => {})

  useEffect(() => {
    loadDiag()
    loadLogs()
    const t = setInterval(loadLogs, 2000)
    return () => clearInterval(t)
  }, [])

  const shown = useMemo(
    () => logs.filter((l) => (rank[l.level] ?? 1) >= (rank[filter.toUpperCase()] ?? 1)),
    [logs, filter],
  )

  const toggleDebug = async () => {
    setMsg('')
    setErr('')
    try {
      await api('/api/logs/level', {
        method: 'POST',
        body: JSON.stringify({ level: debugOn ? 'info' : 'debug' }),
      })
      await loadDiag()
      // Follow the toggle with the table filter, or the new lines stay hidden.
      setFilter(debugOn ? 'info' : 'debug')
      setMsg(
        debugOn
          ? 'Modo debug desativado.'
          : 'Modo debug ativado — as linhas de debug já aparecem na tabela abaixo.',
      )
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const report = () => {
    const header = diag
      ? [
          `cardpit — relatório de diagnóstico`,
          `gerado em: ${new Date().toISOString()}`,
          ``,
          `versão:      ${diag.version}`,
          `plataforma:  ${diag.platform}`,
          `SO/arch:     ${diag.os}/${diag.arch}`,
          `Go:          ${diag.go_version}`,
          `endereço:    ${diag.listen}`,
          `banco:       ${diag.db_path}`,
          `arquivo log: ${diag.log_path || '(sem arquivo — memória)'}`,
          `uptime:      ${diag.uptime_seconds}s`,
          `nível de log:${diag.log_level}`,
          `UI embutida: ${diag.ui_placeholder ? 'placeholder' : 'real'}`,
          ``,
          `--- logs (${logs.length}) ---`,
        ].join('\n')
      : 'cardpit — relatório de diagnóstico\n'
    const body = logs
      .map((l) => `${l.time} ${l.level.padEnd(5)} ${l.msg}${attrsText(l.attrs) ? '  ' + attrsText(l.attrs) : ''}`)
      .join('\n')
    const blob = new Blob([header + '\n' + body + '\n'], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `cardpit-relatorio-${new Date().toISOString().replace(/[:.]/g, '-')}.txt`
    a.click()
    URL.revokeObjectURL(url)
  }

  const copyReport = async () => {
    setMsg('')
    setErr('')
    const text = logs
      .map((l) => `${l.time} ${l.level} ${l.msg}${attrsText(l.attrs) ? '  ' + attrsText(l.attrs) : ''}`)
      .join('\n')
    try {
      await navigator.clipboard.writeText(text)
      setMsg('Logs copiados para a área de transferência.')
    } catch {
      setErr('Não foi possível copiar — use "Baixar relatório".')
    }
  }

  const shutdown = async () => {
    if (!confirm('Encerrar o cardpit? A cópia de cartões vai parar até você abrir o programa de novo.')) return
    setMsg('')
    setErr('')
    try {
      await api('/api/shutdown', { method: 'POST' })
      setMsg('cardpit está encerrando. Você pode fechar esta aba.')
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <>
      <h1>Diagnóstico</h1>

      <div className="card">
        <h2 style={{ marginTop: 0 }}>Sistema</h2>
        {diag ? (
          <table className="responsive">
            <tbody>
              <tr><td className="muted">Versão</td><td className="mono">{diag.version}</td></tr>
              <tr><td className="muted">Plataforma</td><td className="mono">{diag.platform}</td></tr>
              <tr><td className="muted">SO / arquitetura</td><td className="mono">{diag.os} / {diag.arch}</td></tr>
              <tr><td className="muted">Go</td><td className="mono">{diag.go_version}</td></tr>
              <tr><td className="muted">Endereço</td><td className="mono">{diag.listen}</td></tr>
              <tr><td className="muted">Tempo ligado</td><td className="mono">{diag.uptime_seconds}s</td></tr>
              <tr><td className="muted">Nível de log</td><td className="mono">{diag.log_level}</td></tr>
              <tr>
                <td className="muted">Arquivo de log</td>
                <td className="mono">
                  {diag.log_path || '— sem arquivo (logs apenas em memória; use "Baixar relatório")'}
                </td>
              </tr>
            </tbody>
          </table>
        ) : (
          <p className="muted">Carregando…</p>
        )}
        <div className="row" style={{ marginTop: 10, flexWrap: 'wrap' }}>
          <button onClick={report}>Baixar relatório</button>
          <button onClick={copyReport}>Copiar logs</button>
          <button onClick={toggleDebug}>{debugOn ? 'Desativar modo debug' : 'Ativar modo debug'}</button>
          {diag?.can_shutdown && (
            <button onClick={shutdown} style={{ marginLeft: 'auto', color: 'var(--err)' }}>
              Encerrar cardpit
            </button>
          )}
        </div>
        {msg && <div className="banner info">{msg}</div>}
        {err && <div className="banner err">{err}</div>}
      </div>

      <div className="card">
        <div className="row" style={{ marginBottom: 10 }}>
          <h2 style={{ margin: 0 }}>Logs</h2>
          <label className="row" style={{ marginLeft: 'auto', gap: 6 }}>
            <span className="muted">Nível mínimo</span>
            <select value={filter} onChange={(e) => setFilter(e.target.value as Level)}>
              {LEVELS.map((l) => (
                <option key={l} value={l}>{l}</option>
              ))}
            </select>
          </label>
        </div>
        <div style={{ maxHeight: 460, overflow: 'auto' }}>
          <table className="responsive">
            <thead>
              <tr><th>Hora</th><th>Nível</th><th>Mensagem</th></tr>
            </thead>
            <tbody>
              {shown
                .slice()
                .reverse()
                .map((l, i) => (
                  <tr key={i}>
                    <td className="muted mono" style={{ whiteSpace: 'nowrap' }}>{fmtDate(l.time)}</td>
                    <td><span className={`badge ${badgeClass[l.level] ?? 'cancelled'}`}>{l.level}</span></td>
                    <td>
                      {l.msg}
                      {attrsText(l.attrs) && (
                        <div className="muted mono" style={{ fontSize: 12 }}>{attrsText(l.attrs)}</div>
                      )}
                    </td>
                  </tr>
                ))}
              {shown.length === 0 && (
                <tr><td colSpan={3} className="muted">Nenhum log neste nível.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </>
  )
}
