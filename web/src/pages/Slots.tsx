import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { useEvents } from '../hooks/useEvents'
import type { Slot, SlotNameEntry, Status } from '../api/types'

export default function Slots() {
  const [slots, setSlots] = useState<Slot[]>([])
  const [history, setHistory] = useState<SlotNameEntry[]>([])
  const [wizAlias, setWizAlias] = useState('')
  const [waiting, setWaiting] = useState(false)
  const [doneMsg, setDoneMsg] = useState('')

  const load = async () => {
    const r = await api<{ slots: Slot[] | null }>('/api/slots')
    setSlots(r.slots ?? [])
    const h = await api<{ history: SlotNameEntry[] | null }>('/api/slots/history')
    setHistory(h.history ?? [])
    const st = await api<Status>('/api/status')
    setWaiting(Boolean(st.calibrating))
  }

  useEffect(() => {
    load()
  }, [])

  useEvents((topic, data) => {
    if (topic === 'slot.calibrated') {
      const d = data as { alias: string }
      setWaiting(false)
      setDoneMsg(`Slot "${d.alias}" calibrado com sucesso ✔`)
      setWizAlias('')
      load()
      setTimeout(() => setDoneMsg(''), 6000)
    }
    if (topic === 'slot.autonamed') {
      const d = data as { alias: string }
      setDoneMsg(`Leitor novo identificado como "${d.alias}" — etiquete o leitor físico 🏷️`)
      load()
      setTimeout(() => setDoneMsg(''), 10000)
    }
  })

  const arm = async () => {
    setDoneMsg('')
    await api('/api/slots/calibrate', {
      method: 'POST',
      body: JSON.stringify({ alias: wizAlias.trim() }),
    })
    setWaiting(true)
  }

  const cancelArm = async () => {
    await api('/api/slots/calibrate', { method: 'DELETE' })
    setWaiting(false)
  }

  const rename = async (s: Slot, alias: string) => {
    await api(`/api/slots/${s.id}`, { method: 'PUT', body: JSON.stringify({ alias }) })
    load()
  }

  const del = async (s: Slot) => {
    if (!confirm(`Remover a calibração do slot "${s.alias}"?`)) return
    await api(`/api/slots/${s.id}`, { method: 'DELETE' })
    load()
  }

  return (
    <>
      <h1>Slots</h1>
      <p className="muted">
        Um slot é identificado pela porta USB física (location path + LUN). Slots
        novos ganham um nome fixo automaticamente na primeira vez que um cartão é
        inserido — etiquete o leitor físico com o nome atribuído. O assistente
        abaixo serve para renomear manualmente, se quiser.
      </p>

      <h2>Assistente de calibração</h2>
      <div className="card">
        {!waiting ? (
          <div className="row">
            <input
              placeholder='Apelido do slot (ex.: "Leitor esquerdo")'
              value={wizAlias}
              onChange={(e) => setWizAlias(e.target.value)}
              style={{ maxWidth: 320 }}
            />
            <button className="primary" onClick={arm} disabled={!wizAlias.trim()}>
              Iniciar calibração
            </button>
          </div>
        ) : (
          <div className="row">
            <span>
              ⏳ Aguardando… <b>insira um cartão no slot que deseja nomear</b> (expira em 2 min)
            </span>
            <div className="spacer" />
            <button onClick={cancelArm}>Cancelar</button>
          </div>
        )}
        {doneMsg && <div className="banner info" style={{ marginTop: 10 }}>{doneMsg}</div>}
      </div>

      <h2>Slots identificados</h2>
      <div className="card">
        <table className="responsive">
          <thead>
            <tr>
              <th>Apelido</th>
              <th>Location path</th>
              <th>LUN</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {slots.map((s) => (
              <SlotRow key={s.id} slot={s} onSave={rename} onDelete={del} />
            ))}
            {slots.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  Nenhum slot identificado ainda — insira um cartão em cada leitor.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <h2>Histórico de identificações</h2>
      <p className="muted">
        Registro permanente dos nomes atribuídos automaticamente. Um nome nunca é
        reutilizado, então a etiqueta física de cada leitor continua válida mesmo
        se o slot for removido da lista acima.
      </p>
      <div className="card">
        <table className="responsive">
          <thead>
            <tr>
              <th>Nome</th>
              <th>Location path</th>
              <th>LUN</th>
              <th>Atribuído em</th>
            </tr>
          </thead>
          <tbody>
            {history.map((h) => (
              <tr key={h.id}>
                <td>{h.alias}</td>
                <td className="mono">{h.location_path}</td>
                <td>{h.lun}</td>
                <td>{new Date(h.assigned_at).toLocaleString()}</td>
              </tr>
            ))}
            {history.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  Nenhuma identificação automática ainda.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  )
}

function SlotRow({
  slot,
  onSave,
  onDelete,
}: {
  slot: Slot
  onSave: (s: Slot, alias: string) => void
  onDelete: (s: Slot) => void
}) {
  const [alias, setAlias] = useState(slot.alias)
  return (
    <tr>
      <td>
        <input value={alias} onChange={(e) => setAlias(e.target.value)} style={{ minWidth: 140 }} />
      </td>
      <td className="mono">{slot.location_path}</td>
      <td>{slot.lun}</td>
      <td>
        <div className="row">
          {alias !== slot.alias && (
            <button className="primary" onClick={() => onSave(slot, alias)}>
              Salvar
            </button>
          )}
          <button className="danger" onClick={() => onDelete(slot)}>
            Remover
          </button>
        </div>
      </td>
    </tr>
  )
}
