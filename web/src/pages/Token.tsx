import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, setToken } from '../api/client'

export default function TokenPage() {
  const [value, setValue] = useState('')
  const [err, setErr] = useState('')
  const nav = useNavigate()

  const saveToken = async (tok: string): Promise<boolean> => {
    setToken(tok)
    try {
      await api('/api/status')
      nav('/')
      return true
    } catch {
      setErr('Token rejeitado pelo serviço. Confira o valor exibido no primeiro boot (log do cardpit).')
      return false
    }
  }

  // Auto-login when the launcher opens /token?t=<token> — the user never has
  // to copy the token by hand. The token is stripped from the URL afterwards.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const t = params.get('t')
    if (t) {
      window.history.replaceState({}, '', window.location.pathname)
      void saveToken(t.trim())
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const save = () => {
    setErr('')
    void saveToken(value.trim())
  }

  return (
    <div className="card" style={{ maxWidth: 480, margin: '60px auto' }}>
      <h1>Acesso ao cardpit</h1>
      <p className="muted">
        Cole o token de acesso gerado no primeiro boot do serviço (exibido no
        console/log do cardpit).
      </p>
      <label className="field">
        <span>Token</span>
        <input
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && save()}
          autoFocus
        />
      </label>
      {err && <div className="banner err">{err}</div>}
      <button className="primary" onClick={save} disabled={!value.trim()}>
        Entrar
      </button>
    </div>
  )
}
