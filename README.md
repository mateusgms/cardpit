# cardpit

Estação de ingestão automática de cartões de memória para Windows: detecta
cartões em leitores USB, copia para um SSD com verificação de integridade
(XXH3 em streaming) e deduplicação, identifica o **slot físico** de cada
cartão, notifica via Telegram (com relatório PNG) e é configurável por uma
UI web embutida no próprio binário.

PRD completo em [`docs/PRD.md`](docs/PRD.md).

## Layout

```
core/   serviço Go (binário único: engine + API + UI embutida)
web/    UI React + Vite + TypeScript
docs/   PRD, referência de syscalls Windows, checklist de testes manuais
```

## Build

Requisitos: Go 1.24+, Node 22+ (só para a UI), make.

```sh
make check     # gofmt + vet + testes (-race) + cross-compile p/ Windows
make release   # builda a UI, embute e gera dist/cardpit.exe (windows/amd64)
make dev       # roda no Linux/macOS com a plataforma fake (veja abaixo)
```

`go build` funciona sem a UI buildada (um placeholder committed em
`core/internal/httpapi/webui/dist/` mantém o embed válido); `make release`
sempre embute a UI real.

## Instalação (Windows)

1. Copie `cardpit.exe` e um `config.yaml` (baseado em `config.example.yaml`,
   com `platform: "windows"`) para uma pasta definitiva, ex.
   `C:\cardpit\`.
2. Console admin: `cardpit.exe install` → `cardpit.exe start`.
3. O **primeiro boot imprime o token de acesso** no log — guarde-o.
4. Abra `http://localhost:8532`, cole o token e configure:
   - o volume GUID do SSD de destino (`Get-Volume` no PowerShell);
   - o bot do Telegram (token + chat_id) — use "Enviar mensagem de teste";
   - calibre os slots na aba **Slots** (insira um cartão em cada leitor).
5. Opcional: `cardpit.exe tray` na inicialização do usuário para o ícone
   de bandeja (o serviço roda em Session 0 e não pode ter tray próprio).

Checklist de validação em hardware real:
[`docs/windows-syscalls.md`](docs/windows-syscalls.md).

## Desenvolvimento sem Windows

A plataforma `fake` transforma diretórios em "slots" e "cartões":

```sh
cp config.example.yaml config.dev.yaml   # platform: "fake"
make dev                                  # terminal 1 — backend :8532
cd web && npm run dev                     # terminal 2 — UI com proxy :5173
```

- Inserir cartão: `mkdir -p devcards/slot1/CARD01 && cp fotos/* devcards/slot1/CARD01/`
- Remover no meio da cópia: `rm -rf devcards/slot1/CARD01`
- Destino: configure `fake-dest` como volume GUID na UI (resolve para `devout/`).

O pipeline inteiro (watcher → engine → SSE → UI → Telegram com um bot
real) roda assim em qualquer SO; apenas `platform/win` exige Windows.

## Garantias de integridade

- Arquivo escrito como `*.cardpit-tmp`, `fsync`, hash conferido, só então
  renomeado — queda de energia nunca deixa arquivo final corrompido.
- Dedup por `(tamanho, mtime, XXH3-64)`; reinserir cartão não formatado
  copia só o que falta.
- Destino identificado por volume GUID; SSD ausente = nenhuma cópia (sem
  fallback), com alerta e retomada automática quando montar.
