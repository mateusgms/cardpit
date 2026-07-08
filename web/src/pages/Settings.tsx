import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { Settings } from '../api/types'

export default function SettingsPage() {
  const [s, setS] = useState<Settings>({})
  const [hasTgToken, setHasTgToken] = useState(false)
  const [tgToken, setTgToken] = useState('')
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api<{ settings: Settings; has_telegram_token: boolean }>('/api/settings').then((r) => {
      setS(r.settings)
      setHasTgToken(r.has_telegram_token)
    })
  }, [])

  const set = (k: string, v: string) => setS((prev) => ({ ...prev, [k]: v }))

  const save = async () => {
    setSaving(true)
    setMsg('')
    setErr('')
    try {
      const body: Settings = {
        dest_volume_guid: s.dest_volume_guid ?? '',
        dest_template: s.dest_template ?? '{YYYY-MM-DD}',
        max_concurrent_jobs: s.max_concurrent_jobs ?? '4',
        verify_mode: s.verify_mode ?? 'fast',
        eject_after_copy: s.eject_after_copy ?? 'true',
        unknown_card_policy: s.unknown_card_policy ?? 'ask',
        require_dcim: s.require_dcim ?? 'false',
        telegram_chat_ids: s.telegram_chat_ids ?? '',
      }
      if (tgToken.trim()) body.telegram_bot_token = tgToken.trim()
      await api('/api/settings', { method: 'PUT', body: JSON.stringify(body) })
      if (tgToken.trim()) {
        setHasTgToken(true)
        setTgToken('')
      }
      setMsg('Configurações salvas. (Alterar o limite de jobs simultâneos requer reiniciar o serviço.)')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const tgTest = async () => {
    setMsg('')
    setErr('')
    try {
      await api('/api/telegram/test', { method: 'POST' })
      setMsg('Mensagem de teste enviada ✔ — confira o Telegram.')
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <>
      <h1>Configurações</h1>

      <h2>Destino</h2>
      <div className="card">
        <label className="field">
          <span>Volume GUID do SSD de destino</span>
          <input
            value={s.dest_volume_guid ?? ''}
            onChange={(e) => set('dest_volume_guid', e.target.value)}
            placeholder={'\\\\?\\Volume{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}\\'}
          />
        </label>
        <p className="muted">
          No Windows: <code className="mono">Get-Volume | Select FriendlyName, Path</code> no
          PowerShell. No modo de desenvolvimento (fake), use <code className="mono">fake-dest</code>.
          A cópia nunca usa outro destino se este não estiver montado.
        </p>
        <label className="field">
          <span>Template de pastas (tokens: {'{YYYY-MM-DD} {YYYY} {MM} {DD} {card_alias}'})</span>
          <input
            value={s.dest_template ?? ''}
            onChange={(e) => set('dest_template', e.target.value)}
          />
        </label>
      </div>

      <h2>Políticas</h2>
      <div className="card">
        <label className="field">
          <span>Cartão desconhecido</span>
          <select
            value={s.unknown_card_policy ?? 'ask'}
            onChange={(e) => set('unknown_card_policy', e.target.value)}
          >
            <option value="ask">perguntar (Telegram/painel)</option>
            <option value="copy">copiar sempre</option>
            <option value="ignore">ignorar</option>
          </select>
        </label>
        <label className="field">
          <span>Verificação</span>
          <select value={s.verify_mode ?? 'fast'} onChange={(e) => set('verify_mode', e.target.value)}>
            <option value="fast">rápida — hash em streaming durante a cópia</option>
            <option value="paranoid">paranóica — relê o destino antes do rename</option>
          </select>
        </label>
        <label className="field">
          <span>Ejetar o cartão ao concluir</span>
          <select
            value={s.eject_after_copy ?? 'true'}
            onChange={(e) => set('eject_after_copy', e.target.value)}
          >
            <option value="true">sim (sinal físico de "pode remover")</option>
            <option value="false">não</option>
          </select>
        </label>
        <label className="field">
          <span>Só copiar cartões com pasta DCIM</span>
          <select value={s.require_dcim ?? 'false'} onChange={(e) => set('require_dcim', e.target.value)}>
            <option value="false">não — copiar qualquer mídia</option>
            <option value="true">sim — exigir \DCIM</option>
          </select>
        </label>
        <label className="field">
          <span>Jobs simultâneos (1–16)</span>
          <input
            type="number"
            min={1}
            max={16}
            value={s.max_concurrent_jobs ?? '4'}
            onChange={(e) => set('max_concurrent_jobs', e.target.value)}
            style={{ maxWidth: 120 }}
          />
        </label>
      </div>

      <h2>Telegram</h2>
      <div className="card">
        <label className="field">
          <span>
            Token do bot {hasTgToken && <em>(já configurado — preencha só para trocar)</em>}
          </span>
          <input
            type="password"
            value={tgToken}
            onChange={(e) => setTgToken(e.target.value)}
            placeholder={hasTgToken ? '••••••••' : '123456789:ABC-DEF...'}
          />
        </label>
        <label className="field">
          <span>Chat IDs autorizados (separados por vírgula)</span>
          <input
            value={s.telegram_chat_ids ?? ''}
            onChange={(e) => set('telegram_chat_ids', e.target.value)}
            placeholder="123456789"
          />
        </label>
        <div className="row">
          <button onClick={tgTest}>Enviar mensagem de teste</button>
          <span className="muted">O notificador reinicia sozinho ao salvar token/chats.</span>
        </div>
      </div>

      {msg && <div className="banner info">{msg}</div>}
      {err && <div className="banner err">{err}</div>}
      <button className="primary" onClick={save} disabled={saving}>
        {saving ? 'Salvando…' : 'Salvar configurações'}
      </button>
    </>
  )
}
