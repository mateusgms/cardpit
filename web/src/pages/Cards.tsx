import { useEffect, useState } from 'react'
import { api, fmtDate } from '../api/client'
import type { Card } from '../api/types'

export default function Cards() {
  const [cards, setCards] = useState<Card[]>([])
  const [newSerial, setNewSerial] = useState('')
  const [newAlias, setNewAlias] = useState('')
  const [err, setErr] = useState('')

  const load = () =>
    api<{ cards: Card[] | null }>('/api/cards').then((r) => setCards(r.cards ?? []))

  useEffect(() => {
    load()
  }, [])

  const add = async () => {
    setErr('')
    try {
      await api('/api/cards', {
        method: 'POST',
        body: JSON.stringify({ serial: newSerial.trim().toUpperCase(), alias: newAlias.trim() }),
      })
      setNewSerial('')
      setNewAlias('')
      load()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const update = async (c: Card, alias: string, policy: string) => {
    await api(`/api/cards/${c.id}`, {
      method: 'PUT',
      body: JSON.stringify({ alias, policy }),
    })
    load()
  }

  const del = async (c: Card) => {
    if (!confirm(`Remover o cartão "${c.alias}" da whitelist?`)) return
    await api(`/api/cards/${c.id}`, { method: 'DELETE' })
    load()
  }

  return (
    <>
      <h1>Cartões</h1>
      <p className="muted">
        Cartões conhecidos são copiados (ou ignorados) automaticamente. Cartões fora
        desta lista seguem a política de "cartão desconhecido" das Configurações.
      </p>

      <div className="card">
        <table className="responsive">
          <thead>
            <tr>
              <th>Apelido</th>
              <th>Serial</th>
              <th>Label</th>
              <th>Política</th>
              <th>Último uso</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {cards.map((c) => (
              <CardRow key={c.id} card={c} onSave={update} onDelete={del} />
            ))}
            {cards.length === 0 && (
              <tr>
                <td colSpan={6} className="muted">
                  Nenhum cartão cadastrado.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <h2>Adicionar manualmente</h2>
      <div className="card">
        <div className="row">
          <input
            placeholder="Serial (ex.: A1B2C3D4)"
            value={newSerial}
            onChange={(e) => setNewSerial(e.target.value)}
            style={{ maxWidth: 220 }}
          />
          <input
            placeholder="Apelido"
            value={newAlias}
            onChange={(e) => setNewAlias(e.target.value)}
            style={{ maxWidth: 220 }}
          />
          <button className="primary" onClick={add} disabled={!newSerial.trim()}>
            Adicionar
          </button>
        </div>
        {err && <div className="banner err" style={{ marginTop: 10 }}>{err}</div>}
        <p className="muted" style={{ marginBottom: 0 }}>
          Dica: insira o cartão uma vez e use os botões da pergunta no Painel — o
          serial é registrado automaticamente.
        </p>
      </div>
    </>
  )
}

function CardRow({
  card,
  onSave,
  onDelete,
}: {
  card: Card
  onSave: (c: Card, alias: string, policy: string) => void
  onDelete: (c: Card) => void
}) {
  const [alias, setAlias] = useState(card.alias)
  const [policy, setPolicy] = useState<string>(card.policy)
  const dirty = alias !== card.alias || policy !== card.policy

  return (
    <tr>
      <td>
        <input value={alias} onChange={(e) => setAlias(e.target.value)} style={{ minWidth: 120 }} />
      </td>
      <td className="mono">{card.volume_serial}</td>
      <td className="muted">{card.label || '—'}</td>
      <td>
        <select value={policy} onChange={(e) => setPolicy(e.target.value)}>
          <option value="copy">copiar</option>
          <option value="ignore">ignorar</option>
        </select>
      </td>
      <td className="muted">{fmtDate(card.last_seen_at ?? '')}</td>
      <td>
        <div className="row">
          {dirty && (
            <button className="primary" onClick={() => onSave(card, alias, policy)}>
              Salvar
            </button>
          )}
          <button className="danger" onClick={() => onDelete(card)}>
            Remover
          </button>
        </div>
      </td>
    </tr>
  )
}
