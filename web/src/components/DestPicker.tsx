import { useEffect, useState } from 'react'
import { api, fmtBytes } from '../api/client'
import type { DestCandidate } from '../api/types'

const MANUAL = '__manual__'

function candidateLabel(c: DestCandidate): string {
  const name = c.label || '(sem nome)'
  const parts: string[] = []
  if (c.drive_letter) parts.push(c.drive_letter)
  parts.push(name)
  let text = parts.join(' — ')
  if (c.total_bytes > 0) {
    text += ` (${fmtBytes(c.free_bytes)} livres de ${fmtBytes(c.total_bytes)})`
  }
  if (c.system) text += ' — disco do Windows'
  return text
}

// DestPicker lists the fixed disks reported by the backend so the user picks
// the destination by drive letter instead of pasting a volume GUID. A manual
// mode keeps the old free-text entry for dev (fake-dest) and exotic drives.
export default function DestPicker({
  value,
  onChange,
}: {
  value: string
  onChange: (guid: string) => void
}) {
  const [cands, setCands] = useState<DestCandidate[]>([])
  const [loaded, setLoaded] = useState(false)
  const [manual, setManual] = useState(false)

  useEffect(() => {
    api<{ candidates: DestCandidate[] | null }>('/api/volumes/dest-candidates')
      .then((r) => setCands(r.candidates ?? []))
      .catch(() => {})
      .finally(() => setLoaded(true))
  }, [])

  const known = cands.some((c) => c.volume_guid === value)
  const showManual = manual || (loaded && cands.length === 0)

  return (
    <>
      {!showManual && (
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
          {!value && <option value="">— selecione o disco de destino —</option>}
          {value && !known && (
            <option value={value}>valor atual (não detectado): {value}</option>
          )}
          {cands.map((c) => (
            <option key={c.volume_guid} value={c.volume_guid}>
              {candidateLabel(c)}
            </option>
          ))}
          <option value={MANUAL}>inserir manualmente…</option>
        </select>
      )}
      {showManual && (
        <>
          <input
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={'\\\\?\\Volume{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}\\'}
          />
          <p className="muted">
            {loaded && cands.length === 0 && (
              <>Nenhum disco fixo detectado — insira o caminho manualmente. </>
            )}
            No Windows: <code className="mono">Get-Volume | Select FriendlyName, Path</code> no
            PowerShell. No modo de desenvolvimento (fake), use{' '}
            <code className="mono">fake-dest</code>.
            {manual && cands.length > 0 && (
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
      )}
    </>
  )
}
