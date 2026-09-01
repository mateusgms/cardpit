import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, fmtBytes } from '../api/client'
import { useEvents } from '../hooks/useEvents'
import type { Job, JobEventPayload, Status } from '../api/types'
import DestPicker from '../components/DestPicker'

const statusLabel: Record<string, string> = {
  awaiting_decision: 'aguardando decisão',
  pending: 'na fila',
  copying: 'copiando',
  verifying: 'verificando',
  done: 'concluído',
  failed: 'falhou',
  cancelled: 'cancelado',
}

function fmtDuration(seconds: number): string {
  const rounded = Math.max(0, Math.round(seconds))
  const hours = Math.floor(rounded / 3600)
  const minutes = Math.floor((rounded % 3600) / 60)
  const secs = rounded % 60
  if (hours > 0) return `${hours}h ${minutes}min`
  if (minutes > 0) return `${minutes}min ${secs}s`
  return `${secs}s`
}

export default function Dashboard() {
  const [status, setStatus] = useState<Status | null>(null)
  const [live, setLive] = useState<Record<number, JobEventPayload>>({})
  const [err, setErr] = useState('')
  const [destPick, setDestPick] = useState('')
  const [savingDest, setSavingDest] = useState(false)

  const refresh = useCallback(async () => {
    try {
      setStatus(await api<Status>('/api/status'))
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    }
  }, [])

  useEffect(() => {
    refresh()
    // Fixed USB disks do not emit source-volume SSE events. Poll so a
    // reconnected destination is reflected without reloading the page.
    const timer = window.setInterval(refresh, 5000)
    return () => window.clearInterval(timer)
  }, [refresh])

  useEvents((topic, data) => {
    if (topic === 'job.progress') {
      const p = data as JobEventPayload
      setLive((m) => ({ ...m, [p.job_id]: p }))
      return
    }
    // Lifecycle events change lists → refetch the aggregate state.
    if (
      ['job.started', 'job.completed', 'job.failed', 'volume.attached',
        'volume.detached', 'card.unknown', 'card.decision', 'dest.missing',
        'slot.calibrated', 'hello'].includes(topic)
    ) {
      refresh()
    }
  })

  const activeJobs = status?.active_jobs ?? []
  const slots = status?.slots ?? []
  const volumes = status?.volumes ?? []
  const blockedIds = status?.blocked_job_ids ?? []
  const destMounted = status?.dest_mounted ?? true

  // A pending job with no usable destination sits in the queue forever;
  // surface that instead of a bare "na fila".
  const waitingDest = useCallback(
    (j: Job) => j.status === 'pending' && (blockedIds.includes(j.id) || !destMounted),
    [blockedIds, destMounted],
  )

  // Slot tile state: join calibrated slots with live volumes and jobs.
  const tiles = useMemo(() => {
    return slots.map((s) => {
      const job = activeJobs.find(
        (j) => j.slot_location === s.location_path && j.slot_lun === s.lun,
      )
      const vol = volumes.find(
        (v) => v.location_path === s.location_path && v.lun === s.lun && v.attached,
      )
      let cls = ''
      let state = 'vazio'
      if (job) {
        state = statusLabel[job.status] ?? job.status
        cls = job.status === 'copying' || job.status === 'verifying' ? 'copying' : ''
        if (job.status === 'failed') cls = 'error'
        if (waitingDest(job)) {
          state = 'aguardando destino'
          cls = 'error'
        }
      } else if (vol) {
        state = `cartão ${vol.label || vol.serial}`
      }
      return { slot: s, job, vol, cls, state }
    })
  }, [slots, activeJobs, volumes, waitingDest])

  const awaiting = activeJobs.filter((j) => j.status === 'awaiting_decision')

  const decide = async (serial: string, action: string) => {
    await api('/api/cards/decision', {
      method: 'POST',
      body: JSON.stringify({ serial, action }),
    })
    refresh()
  }

  const saveDest = async () => {
    setSavingDest(true)
    try {
      await api('/api/settings', {
        method: 'PUT',
        body: JSON.stringify({ dest_volume_guid: destPick.trim() }),
      })
      await refresh()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSavingDest(false)
    }
  }

  const togglePause = async () => {
    if (!status) return
    await api('/api/settings', {
      method: 'PUT',
      body: JSON.stringify({ watcher_paused: status.watcher_paused ? 'false' : 'true' }),
    })
    refresh()
  }

  return (
    <>
      <div className="row">
        <h1>Painel</h1>
        <div className="spacer" />
        <button onClick={togglePause}>
          {status?.watcher_paused ? '▶ Retomar detecção' : '⏸ Pausar detecção'}
        </button>
      </div>

      {err && <div className="banner err">Erro: {err}</div>}
      {status && !status.dest_guid && (
        <div className="card">
          <h2 style={{ marginTop: 0 }}>Configure o SSD de destino</h2>
          <p className="muted">
            Sem um destino configurado, os cartões ficam na fila e nada é copiado. Escolha
            o disco onde os arquivos serão gravados:
          </p>
          <div className="row" style={{ flexWrap: 'wrap', gap: 8 }}>
            <DestPicker value={destPick} onChange={setDestPick} />
            <button
              className="primary"
              disabled={!destPick.trim() || savingDest}
              onClick={saveDest}
            >
              {savingDest ? 'Salvando…' : 'Salvar destino'}
            </button>
          </div>
        </div>
      )}
      {status && status.dest_guid && !status.dest_mounted && (
        <div className="banner warn">
          ⚠ SSD de destino ausente — nenhuma cópia será iniciada. Conecte o disco
          configurado ou troque o destino em Configurações.
        </div>
      )}
      {status?.watcher_paused && (
        <div className="banner info">Detecção pausada — novos cartões serão ignorados até retomar.</div>
      )}

      {awaiting.map((j) => (
        <div className="banner warn" key={j.id}>
          <div className="row">
            <span>
              Cartão desconhecido <b>{j.card_label || j.volume_serial}</b> — o que fazer?
            </span>
            <div className="spacer" />
            <button className="primary" onClick={() => decide(j.volume_serial, 'copy')}>
              Copiar
            </button>
            <button onClick={() => decide(j.volume_serial, 'ignore')}>Ignorar</button>
            <button className="danger" onClick={() => decide(j.volume_serial, 'always_ignore')}>
              Ignorar sempre
            </button>
          </div>
        </div>
      ))}

      <h2>Slots calibrados</h2>
      {tiles.length === 0 && (
        <div className="card muted">
          Nenhum slot calibrado ainda. Use o assistente na aba <b>Slots</b>.
        </div>
      )}
      <div className="grid">
        {tiles.map(({ slot, cls, state }) => (
          <div className={`tile ${cls}`} key={slot.id}>
            <div className="name">{slot.alias}</div>
            <div className="state">{state}</div>
          </div>
        ))}
      </div>

      <h2>Jobs ativos</h2>
      {activeJobs.length === 0 && <div className="card muted">Nenhuma ingestão em andamento.</div>}
      {activeJobs.map((j) => (
        <JobCard
          key={j.id}
          job={j}
          live={live[j.id]}
          onCancel={refresh}
          waitingDest={waitingDest(j)}
          hasDestGuid={!!status?.dest_guid}
        />
      ))}
    </>
  )
}

function JobCard({
  job,
  live,
  onCancel,
  waitingDest,
  hasDestGuid,
}: {
  job: Job
  live?: JobEventPayload
  onCancel: () => void
  waitingDest: boolean
  hasDestGuid: boolean
}) {
  const copied = live?.files_copied ?? job.files_copied
  const bytesCopied = live?.bytes_copied ?? job.bytes_copied
  const total = live?.files_total ?? job.files_total
  const bytesTotal = live?.bytes_total ?? job.bytes_total
  const pct = bytesTotal > 0 ? Math.min(100, (bytesCopied / bytesTotal) * 100) : 0
  const bytesPerSecond = live?.bytes_per_second ?? 0
  const etaSeconds = live?.eta_seconds ?? 0

  const cancel = async () => {
    try {
      await api(`/api/jobs/${job.id}/cancel`, { method: 'POST' })
    } finally {
      onCancel()
    }
  }

  return (
    <div className="card">
      <div className="row">
        <b>{live?.card_alias || job.card_label || job.volume_serial}</b>
        <span className="badge">{live?.slot_alias || job.slot_location || 'slot desconhecido'}</span>
        <span className={`badge ${job.status}`}>{statusLabel[job.status] ?? job.status}</span>
        <div className="spacer" />
        {['pending', 'copying', 'verifying'].includes(job.status) && (
          <button className="danger" onClick={cancel}>
            Cancelar
          </button>
        )}
      </div>
      {waitingDest && (
        <div className="muted">
          aguardando SSD de destino —{' '}
          {hasDestGuid
            ? 'conecte o disco configurado; a cópia retoma sozinha.'
            : 'configure o destino acima ou em Configurações.'}
        </div>
      )}
      {(job.status === 'copying' || job.status === 'verifying') && (
        <>
          <div className="progress">
            <div style={{ width: `${pct}%` }} />
          </div>
          <div className="muted">
            {copied}/{total} arquivos · {fmtBytes(bytesCopied)} de {fmtBytes(bytesTotal)}
            {job.files_skipped > 0 && ` · ${job.files_skipped} pulados (dedup)`}
            {' · '}
            {etaSeconds > 0 && bytesPerSecond > 0
              ? `${fmtBytes(bytesPerSecond)}/s · tempo restante ${fmtDuration(etaSeconds)}`
              : 'estimativa calculando…'}
          </div>
        </>
      )}
    </div>
  )
}
