import { useEffect, useState } from 'react'
import { api, fmtBytes } from '../api/client'
import type { DestCandidate } from '../api/types'

const MANUAL = '__manual__'

function optionLabel(c: DestCandidate): string {
  const name = [c.drive_letter, c.label || '(sem rótulo)'].filter(Boolean).join(' — ')
  const size =
    c.total_bytes > 0 ? ` (${fmtBytes(c.free_bytes)} livres de ${fmtBytes(c.total_bytes)})` : ''
  const sys = c.system ? ' — disco do Windows' : ''
  return `${name}${size}${sys}`
}

/**
 * Dropdown of fixed disks that can serve as the ingest destination, with a
 * free-text fallback for disks that don't enumerate as fixed (USB enclosures)
 * and for the fake dev platform.
 */
export default function DestPicker({
  value,
  onChange,
}: {
  value: string
  onChange: (guid: string) => void
}) {
  const [candidates, setCandidates] = useState<DestCandidate[] | null>(null)
  const [manual, setManual] = useState(false)

  useEffect(() => {
    api<{ candidates: DestCandidate[] | null }>('/api/volumes/dest-candidates')
      .then((r) => setCandidates(r.candidates ?? []))
      .catch(() => setCandidates([]))
  }, [])

  if (candidates === null) {
    return <p className="muted">Procurando discos…</p>
  }

  const known = candidates.some((c) => c.volume_guid === value)

  if (manual || (candidates.length === 0 && value === '')) {
    return (
      <>
        <input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={'\\\\?\\Volume{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}\\'}
          autoFocus={manual}
        />
        <p className="muted">
          {candidates.length === 0 &&
            'Nenhum disco fixo detectado — insira o identificador manualmente. '}
          No Windows: <code className="mono">Get-Volume | Select FriendlyName, Path</code> no
          PowerShell. No modo de desenvolvimento (fake), use{' '}
          <code className="mono">fake-dest</code>.
          {candidates.length > 0 && (
            <>
              {' '}
              <a
                href="#"
                onClick={(e) => {
                  e.preventDefault()
                  setManual(false)
                }}
              >
                voltar à lista de discos
              </a>
            </>
          )}
        </p>
      </>
    )
  }

  return (
    <select
      value={value}
      onChange={(e) => {
        if (e.target.value === MANUAL) {
          setManual(true)
          return
        }
        onChange(e.target.value)
      }}
    >
      <option value="">— escolha o disco de destino —</option>
      {candidates.map((c) => (
        <option key={c.volume_guid} value={c.volume_guid}>
          {optionLabel(c)}
        </option>
      ))}
      {value !== '' && !known && (
        <option value={value}>valor atual (não detectado): {value}</option>
      )}
      <option value={MANUAL}>inserir manualmente…</option>
    </select>
  )
}
