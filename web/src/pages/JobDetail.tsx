import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, fmtBytes, fmtDate } from '../api/client'
import type { IngestedFile, Job } from '../api/types'

export default function JobDetail() {
  const { id } = useParams()
  const [job, setJob] = useState<Job | null>(null)
  const [files, setFiles] = useState<IngestedFile[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const pageSize = 100

  useEffect(() => {
    // There is no GET /api/jobs/{id}; the list endpoint carries everything
    // needed and history pages are small.
    api<{ jobs: Job[] | null }>(`/api/jobs?page=1&page_size=200`).then((r) =>
      setJob((r.jobs ?? []).find((j) => j.id === Number(id)) ?? null),
    )
  }, [id])

  useEffect(() => {
    api<{ files: IngestedFile[] | null; total: number }>(
      `/api/jobs/${id}/files?page=${page}&page_size=${pageSize}`,
    ).then((r) => {
      setFiles(r.files ?? [])
      setTotal(r.total)
    })
  }, [id, page])

  const pages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <>
      <p>
        <Link to="/historico">← voltar ao histórico</Link>
      </p>
      <h1>Ingestão #{id}</h1>
      {job && (
        <div className="card">
          <div className="row">
            <b>{job.card_label || job.volume_serial}</b>
            <span className={`badge ${job.status}`}>{job.status}</span>
            {job.error && <span className="muted">{job.error}</span>}
          </div>
          <div className="muted" style={{ marginTop: 6 }}>
            {job.files_copied} copiados · {job.files_skipped} pulados (dedup) ·{' '}
            {job.files_failed} falharam · {fmtBytes(job.bytes_copied)} ·{' '}
            {fmtDate(job.started_at)} → {fmtDate(job.finished_at ?? '')}
          </div>
        </div>
      )}
      <div className="card">
        <table className="responsive">
          <thead>
            <tr>
              <th>Origem</th>
              <th>Destino</th>
              <th>Tamanho</th>
              <th>Tipo</th>
            </tr>
          </thead>
          <tbody>
            {files.map((f) => (
              <tr key={f.id}>
                <td className="mono">{f.src_path}</td>
                <td className="mono">{f.dst_path}</td>
                <td>{fmtBytes(f.size)}</td>
                <td>{f.media_type}</td>
              </tr>
            ))}
            {files.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  Nenhum arquivo registrado para este job.
                </td>
              </tr>
            )}
          </tbody>
        </table>
        {pages > 1 && (
          <div className="row" style={{ marginTop: 10 }}>
            <button disabled={page <= 1} onClick={() => setPage(page - 1)}>
              ← Anterior
            </button>
            <span className="muted">
              página {page} de {pages} ({total} arquivos)
            </span>
            <button disabled={page >= pages} onClick={() => setPage(page + 1)}>
              Próxima →
            </button>
          </div>
        )}
      </div>
    </>
  )
}
