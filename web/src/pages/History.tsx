import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, fmtBytes, fmtDate } from '../api/client'
import type { Job } from '../api/types'

const statusLabel: Record<string, string> = {
  awaiting_decision: 'aguardando',
  pending: 'na fila',
  copying: 'copiando',
  verifying: 'verificando',
  done: 'concluído',
  failed: 'falhou',
  cancelled: 'cancelado',
}

export default function History() {
  const [jobs, setJobs] = useState<Job[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const nav = useNavigate()
  const pageSize = 20

  useEffect(() => {
    api<{ jobs: Job[] | null; total: number }>(
      `/api/jobs?page=${page}&page_size=${pageSize}`,
    ).then((r) => {
      setJobs(r.jobs ?? [])
      setTotal(r.total)
    })
  }, [page])

  const pages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <>
      <h1>Histórico</h1>
      <div className="card">
        <table className="responsive">
          <thead>
            <tr>
              <th>#</th>
              <th>Cartão</th>
              <th>Status</th>
              <th>Arquivos</th>
              <th>Bytes</th>
              <th>Início</th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id} className="click" onClick={() => nav(`/jobs/${j.id}`)}>
                <td className="mono">{j.id}</td>
                <td>{j.card_label || j.volume_serial}</td>
                <td>
                  <span className={`badge ${j.status}`}>{statusLabel[j.status] ?? j.status}</span>
                </td>
                <td>
                  {j.files_copied}/{j.files_total}
                  {j.files_skipped > 0 && <span className="muted"> (+{j.files_skipped} dedup)</span>}
                </td>
                <td>{fmtBytes(j.bytes_copied)}</td>
                <td className="muted">{fmtDate(j.started_at)}</td>
              </tr>
            ))}
            {jobs.length === 0 && (
              <tr>
                <td colSpan={6} className="muted">
                  Nenhuma ingestão registrada.
                </td>
              </tr>
            )}
          </tbody>
        </table>
        <div className="row" style={{ marginTop: 10 }}>
          <button disabled={page <= 1} onClick={() => setPage(page - 1)}>
            ← Anterior
          </button>
          <span className="muted">
            página {page} de {pages}
          </span>
          <button disabled={page >= pages} onClick={() => setPage(page + 1)}>
            Próxima →
          </button>
        </div>
      </div>
    </>
  )
}
