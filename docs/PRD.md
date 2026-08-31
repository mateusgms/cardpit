# PRD — cardpit
**Estação de ingestão automática de cartões de memória**

| Campo | Valor |
|---|---|
| Versão | 1.0 |
| Data | 2026-07-07 |
| Autor | Mateus |
| Status | Aprovado para desenvolvimento |
| Repositório | `cardpit` (monorepo) |

---

## 1. Visão geral

### 1.1 Problema
O processo de descarregar cartões de memória de câmeras (fotos e vídeos) para um SSD é manual, repetitivo e sujeito a erros: esquecer de copiar, copiar para a pasta errada, duplicar arquivos, remover o cartão antes do fim da cópia, ou não ter confirmação de que a cópia foi íntegra. Quando há múltiplos cartões e leitores, não há forma clara de saber **qual cartão físico terminou** e pode ser removido.

### 1.2 Solução
Um serviço local para Windows que:
1. Detecta automaticamente a inserção de cartões de memória em leitores USB.
2. Copia o conteúdo para um SSD específico, com verificação de integridade e deduplicação.
3. Identifica **a porta/slot físico** de cada cartão, permitindo saber exatamente qual cartão terminou e pode ser removido.
4. Notifica via Telegram no início e no fim da cópia, com relatório de arquivos, tamanhos e duração.
5. É parametrizável por interface web — acessível localmente no PC e remotamente.

### 1.3 Princípios de design
- **Zero interação no caminho feliz**: inseriu o cartão → cópia acontece → notificação chega → cartão é ejetado. Nenhum clique necessário.
- **Nunca perder ou corromper mídia**: integridade sempre prevalece sobre velocidade.
- **Identidade física, não lógica**: cartões, slots e SSDs são identificados por propriedades estáveis (serial, volume GUID, location path), nunca por letras de unidade.
- **Binário único**: um `.exe` contém serviço, API e UI web embutida.

---

## 2. Objetivos e métricas de sucesso

| Objetivo | Métrica | Meta |
|---|---|---|
| Automação total do caminho feliz | Cliques necessários por ingestão | 0 |
| Integridade | Arquivos copiados com hash divergente não detectado | 0 |
| Deduplicação | Arquivos re-copiados ao reinserir cartão não formatado | 0 |
| Velocidade | Throughput vs. cópia manual pelo Explorer | ≥ 95% |
| Visibilidade | Tempo entre fim da cópia e notificação no Telegram | < 10 s |
| Clareza física | Usuário sabe qual cartão remover sem olhar o PC | 100% das mensagens identificam o slot |

---

## 3. Escopo

### 3.1 Dentro do escopo (v1)
- Windows 10/11 x64.
- Cartões com conteúdo de câmera (estrutura DCIM) e mídia genérica.
- Múltiplos leitores e cartões simultâneos (jobs concorrentes).
- Leitores multi-slot (SD + microSD + CF no mesmo dispositivo, via LUN).
- Notificação exclusivamente via Telegram Bot API.
- UI web servida pelo próprio binário (local e remota via rede/Tailscale).
- Instalação como serviço do Windows com ícone na bandeja.

### 3.2 Fora do escopo (v1)
- WhatsApp (API oficial é paga/burocrática; libs não-oficiais arriscam ban de número).
- macOS e Linux (arquitetura deve permitir port futuro isolando o watcher).
- Edição/transcodificação de mídia, geração de proxies, upload para nuvem.
- Feedback físico por LED via ESP32 (fase futura — a API de eventos deve prever webhooks para viabilizar).
- Backup para múltiplos destinos simultâneos (candidato forte para v2).

---

## 4. Usuário e contexto de uso

Usuário único, técnico, operando um PC Windows dedicado ou semi-dedicado. Fluxo típico: chega de uma sessão de fotos/filmagem com 1–4 cartões, insere todos nos leitores, e vai fazer outra coisa. Acompanha o progresso pelo Telegram no celular. Remove cada cartão quando a mensagem correspondente confirmar a conclusão. Eventualmente ajusta configurações pela UI web — do próprio PC ou de outro dispositivo na rede.

---

## 5. Requisitos funcionais

### RF-01 — Detecção de cartões

| ID | Requisito | Prioridade |
|---|---|---|
| RF-01.1 | O watcher deve detectar novos volumes removíveis por polling (`GetLogicalDrives` + `GetDriveType == DRIVE_REMOVABLE`) com intervalo configurável (padrão: 2 s). | P0 |
| RF-01.2 | Após detecção, aplicar debounce configurável (padrão: 3 s) antes de iniciar leitura, evitando corrida com a montagem do SO. | P0 |
| RF-01.3 | Identificar o cartão por volume serial number (`GetVolumeInformation`) + label. | P0 |
| RF-01.4 | Classificar o cartão contra a whitelist cadastrada: `conhecido`, `desconhecido`. | P0 |
| RF-01.5 | Para cartão desconhecido, aplicar a política configurada: `ignorar` \| `copiar` \| `perguntar via Telegram` (botões inline "Copiar / Ignorar / Ignorar sempre"). Padrão: `copiar` (modo kiosk 24/7: tudo que plugar é copiado sem interação). | P1 |
| RF-01.6 | Heurística opcional "só copiar se existir `\DCIM`" (toggle na UI, padrão: desligado). | P2 |
| RF-01.7 | Detectar remoção de volume a qualquer momento e reagir conforme RF-03.8. | P0 |

### RF-02 — Identificação de porta/slot físico

| ID | Requisito | Prioridade |
|---|---|---|
| RF-02.1 | Resolver a cadeia letra → volume GUID (`GetVolumeNameForVolumeMountPoint`) → disco físico (`IOCTL_STORAGE_GET_DEVICE_NUMBER`) → device instance ID → location path USB (`DEVPKEY_Device_LocationPaths` via CfgMgr32, subindo a árvore com `CM_Get_Parent`). | P0 |
| RF-02.2 | A chave de identidade do slot é `location_path + LUN`, cobrindo leitores multi-slot que expõem vários volumes num único dispositivo USB. | P0 |
| RF-02.3 | Wizard de calibração na UI: usuário insere um cartão em cada slot e atribui um apelido (ex.: "Leitor esquerdo", "Hub porta 1 — microSD"). Persistir apelido ↔ chave do slot. | P0 |
| RF-02.4 | Slots nunca vistos ganham automaticamente um nome fixo de uma lista pré-definida (registrado em log permanente, nome nunca reutilizado — o operador etiqueta o leitor físico com ele); se a auto-nomeação falhar, o fallback é o location path bruto. | P1 |
| RF-02.5 | Botão "recalibrar slot" na UI para quando o hub/leitor mudar de porta física. | P1 |
| RF-02.6 | Toda mensagem de Telegram e todo card de job na UI devem exibir o apelido do slot. | P0 |

### RF-03 — Copy engine

| ID | Requisito | Prioridade |
|---|---|---|
| RF-03.1 | Um job de cópia independente por volume detectado; jobs executam concorrentemente (limite configurável, padrão: 4). | P0 |
| RF-03.2 | Destino organizado por template configurável, padrão `{destino}\{YYYY-MM-DD}\{period}\`, com a data/período derivados do **mtime de cada arquivo**. `{period}` gera `Dia` (06–14h), `Tarde` (14–18h) ou `Noite` (18–06h). | P0 |
| RF-03.3 | Escrever cada arquivo como `{nome}.cardpit-tmp` e renomear para o nome final **somente após** o hash conferir. Arquivos `.cardpit-tmp` órfãos são limpos no boot do serviço. | P0 |
| RF-03.4 | Calcular xxHash64 em streaming durante a cópia (`io.TeeReader`) — custo zero de leitura extra. | P0 |
| RF-03.5 | Modo "verificação paranóica" (toggle, padrão: desligado): reler o arquivo do destino e conferir o hash antes do rename. | P1 |
| RF-03.6 | Deduplicação: manter índice `(tamanho, mtime, xxhash)` de todo arquivo já ingerido; ao reinserir cartão não formatado, copiar apenas o que é novo. Colisão de `(tamanho, mtime)` sem hash confirmado → calcular hash da origem antes de decidir. | P0 |
| RF-03.7 | Ao concluir com sucesso, ejetar o volume via `IOCTL_STORAGE_EJECT_MEDIA` (toggle, padrão: ligado) — sinal físico de "pode remover". | P1 |
| RF-03.8 | Remoção do cartão no meio da cópia: job marcado como `falhou`, `.cardpit-tmp` parciais removidos, alerta no Telegram com resumo do que foi/não foi copiado. Reinserção do mesmo cartão retoma do ponto de parada (via dedup). | P0 |
| RF-03.9 | Retry automático em erro de I/O transitório: 3 tentativas por arquivo com backoff; após esgotar, arquivo entra na lista de falhas do relatório e o job continua. | P1 |
| RF-03.10 | O destino é configurado por **volume GUID**, resolvido para letra em runtime. Se o SSD de destino não estiver montado, nenhuma cópia inicia e um alerta é enviado. Nunca copiar para um fallback. | P0 |
| RF-03.11 | Verificação de espaço livre no destino antes de iniciar; se insuficiente, job não inicia e alerta é enviado com o déficit. | P0 |
| RF-03.12 | Coletar métricas por job: nº de arquivos e bytes por tipo (foto/vídeo/outro), duração, throughput médio, lista de falhas. | P0 |

### RF-04 — Notificações (Telegram)

| ID | Requisito | Prioridade |
|---|---|---|
| RF-04.1 | Mensagem de **início** por job: apelido do slot, identificação do cartão, nº estimado de arquivos e bytes a copiar (pós-dedup), horário de início. | P0 |
| RF-04.2 | **Progresso** via `editMessageText` na própria mensagem de início (a cada 10% ou 30 s, o que ocorrer primeiro) — sem spam de mensagens novas. | P1 |
| RF-04.3 | Mensagem de **conclusão**: apelido do slot, resumo agrupado ("247 fotos (18,2 GB) · 12 vídeos (41,7 GB)"), duração, throughput, instrução explícita "✅ pode remover o cartão do {slot}". | P0 |
| RF-04.4 | Relatório visual em **PNG** anexado à conclusão (`sendPhoto`): tabela dos maiores arquivos, totais por tipo, timeline. Renderizado com a lib `gg` (sem Chrome headless). | P1 |
| RF-04.5 | Mensagem de **erro** para: cartão removido no meio, SSD ausente, espaço insuficiente, arquivos com falha após retries. | P0 |
| RF-04.6 | Botões inline para a política "perguntar" de cartão desconhecido (RF-01.5). | P1 |
| RF-04.7 | Bot restrito por `chat_id` allowlist — mensagens de qualquer outro chat são ignoradas. | P0 |
| RF-04.8 | Camada de notificação atrás de uma interface `Notifier` (Go), para permitir múltiplos canais no futuro sem tocar o core. | P1 |

### RF-05 — API HTTP + UI web

| ID | Requisito | Prioridade |
|---|---|---|
| RF-05.1 | Servidor HTTP embutido no binário (padrão: `0.0.0.0:8532`, configurável), servindo a API REST e a UI React via `embed.FS`. | P0 |
| RF-05.2 | **Dashboard**: jobs ativos com progresso em tempo real (SSE), estado de cada slot calibrado (vazio / copiando / concluído / erro). | P0 |
| RF-05.3 | **Configurações**: destino (seleção de volume por GUID com nome amigável), template de pastas, políticas (cartão desconhecido, verificação paranóica, ejeção automática, limite de concorrência), credenciais do bot (token + chat_id). | P0 |
| RF-05.4 | **Cartões**: whitelist com apelido, serial, último uso; ações de adicionar/remover/renomear. | P0 |
| RF-05.5 | **Slots**: wizard de calibração (RF-02.3) e recalibração (RF-02.5). | P0 |
| RF-05.6 | **Histórico**: lista paginada de ingestões com drill-down para a lista de arquivos de cada job. | P1 |
| RF-05.7 | Autenticação por token estático (header/cookie), configurado no primeiro boot. Suficiente para uso pessoal atrás de LAN/Tailscale; sem gestão de usuários. | P1 |
| RF-05.8 | UI responsiva — o acesso remoto típico é pelo celular. | P1 |

### RF-06 — Empacotamento e operação

| ID | Requisito | Prioridade |
|---|---|---|
| RF-06.1 | Instalável como serviço do Windows com auto-start (`kardianos/service`); comandos `cardpit install/uninstall/start/stop`. | P0 |
| RF-06.2 | Ícone na bandeja do sistema com: estado agregado, "Abrir painel" (navegador em `localhost:8532`), "Pausar detecção", "Sair". | P1 |
| RF-06.3 | Logs estruturados em JSON (arquivo com rotação) desde a fase 1. | P0 |
| RF-06.4 | Config bootstrap em `config.yaml` ao lado do binário (porta, caminho do SQLite); todo o resto vive no SQLite e é editado pela UI. | P0 |
| RF-06.5 | Migrations de schema embutidas e aplicadas no boot. | P0 |

---

## 6. Requisitos não-funcionais

| Categoria | Requisito |
|---|---|
| **Performance** | Throughput limitado apenas pelo hardware (leitor/SSD); overhead do hash em streaming < 5%. Buffers de cópia de 4 MiB. |
| **Confiabilidade** | Nenhum estado de job apenas em memória: transições persistidas no SQLite; queda de energia no meio da cópia deixa no máximo arquivos `.cardpit-tmp`, nunca arquivos finais corrompidos. |
| **Concorrência** | 4 jobs simultâneos sem degradação de detecção nem de UI; escrita no SQLite serializada (WAL mode). |
| **Segurança** | Token do bot e token da UI armazenados com DPAPI (Windows Data Protection); API nunca exposta sem o token; sem telemetria externa. |
| **Portabilidade** | Código específico de Windows isolado em pacote `platform/` atrás de interfaces (`VolumeWatcher`, `SlotResolver`, `Ejector`) para port futuro. |
| **Build** | `make build` produz o `.exe` único (UI buildada e embutida). CGO desabilitado (`modernc.org/sqlite`). |

---

## 7. Arquitetura e stack

### 7.1 Componentes (binário único)

```
┌────────────────────────────────────────────────────┐
│ cardpit.exe (serviço Go)                           │
│                                                    │
│  watcher ──► job manager ──► copy engine           │
│     │             │              │                 │
│     ▼             ▼              ▼                 │
│  slot resolver  event bus ◄── notifier (Telegram)  │
│                   │                                │
│                   ▼                                │
│         API HTTP + UI React (embed.FS) + SSE       │
│                   │                                │
│                SQLite (WAL)                        │
└────────────────────────────────────────────────────┘
```

O **event bus** interno (canal Go pub/sub) é a espinha dorsal: watcher publica `volume.attached/detached`, job manager publica `job.started/progress/completed/failed`; notifier, SSE e (futuramente) webhooks para ESP32 são apenas assinantes.

### 7.2 Stack

| Camada | Escolha | Justificativa |
|---|---|---|
| Serviço | Go 1.22+ | Binário único, concorrência natural, domínio do autor |
| Win32/USB | `golang.org/x/sys/windows` + CfgMgr32 via syscall | Detecção, volume info, location path, eject |
| Hash | `github.com/zeebo/xxh3` | xxHash64/XXH3, ordens de magnitude mais rápido que SHA |
| Banco | `modernc.org/sqlite` | SQLite puro Go, sem CGO |
| Telegram | `github.com/go-telegram/bot` | Bot API oficial, suporta edit e inline keyboards |
| Serviço Windows | `github.com/kardianos/service` | Install/start/stop multiplataforma |
| Tray | `github.com/getlantern/systray` | Ícone na bandeja |
| Relatório PNG | `github.com/fogleman/gg` | Desenho 2D puro Go, sem browser headless |
| Frontend | React 18 + Vite + TypeScript | Embutido via `embed.FS`; SSE para tempo real |
| Build | Makefile | `web build → embed → go build` |

### 7.3 Estrutura do monorepo

```
cardpit/
├── core/
│   ├── cmd/cardpit/        # main, subcomandos (install, run, uninstall)
│   ├── internal/
│   │   ├── watcher/        # polling de volumes, debounce, classificação
│   │   ├── platform/       # win32: slot resolver, eject, volume info
│   │   ├── engine/         # job manager, copy, hash, dedup
│   │   ├── notify/         # interface Notifier + impl telegram
│   │   ├── report/         # renderização do PNG
│   │   ├── httpapi/        # REST + SSE + embed da UI
│   │   ├── store/          # SQLite, migrations, repositórios
│   │   └── bus/            # event bus interno
│   └── go.mod
├── web/                    # React + Vite + TS
├── firmware/               # (futuro) ESP32 LEDs
├── docs/                   # ADRs, este PRD, guia de calibração
└── Makefile
```

---

## 8. Modelo de dados (SQLite)

```sql
CREATE TABLE cards (
  id           INTEGER PRIMARY KEY,
  volume_serial TEXT NOT NULL UNIQUE,   -- GetVolumeInformation
  label        TEXT,
  alias        TEXT NOT NULL,           -- "SanDisk 128 A"
  policy       TEXT NOT NULL DEFAULT 'copy',  -- copy | ignore
  created_at   TEXT NOT NULL,
  last_seen_at TEXT
);
CREATE TABLE slots (
  id            INTEGER PRIMARY KEY,
  location_path TEXT NOT NULL,          -- DEVPKEY_Device_LocationPaths
  lun           INTEGER NOT NULL DEFAULT 0,
  alias         TEXT NOT NULL,          -- "Leitor esquerdo"
  created_at    TEXT NOT NULL,
  UNIQUE (location_path, lun)
);
CREATE TABLE jobs (
  id            INTEGER PRIMARY KEY,
  card_id       INTEGER REFERENCES cards(id),
  slot_id       INTEGER REFERENCES slots(id),
  status        TEXT NOT NULL,          -- pending|copying|verifying|done|failed|cancelled
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  files_total   INTEGER, bytes_total INTEGER,
  files_copied  INTEGER, bytes_copied INTEGER,
  files_skipped INTEGER,                -- dedup
  files_failed  INTEGER,
  error         TEXT,
  tg_message_id INTEGER                 -- p/ editMessageText
);
CREATE TABLE ingested_files (
  id         INTEGER PRIMARY KEY,
  job_id     INTEGER NOT NULL REFERENCES jobs(id),
  src_path   TEXT NOT NULL,
  dst_path   TEXT NOT NULL,
  size       INTEGER NOT NULL,
  mtime      TEXT NOT NULL,
  xxhash     TEXT NOT NULL,
  media_type TEXT NOT NULL              -- photo | video | other
);
CREATE INDEX idx_dedup ON ingested_files (size, mtime, xxhash);
CREATE TABLE settings (                 -- chave-valor tipado
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

---

## 9. Contrato da API (REST + SSE)

| Método | Rota | Descrição |
|---|---|---|
| GET | `/api/status` | Estado agregado: slots, jobs ativos, destino montado? |
| GET | `/api/events` | **SSE** — stream de eventos do bus (progresso, transições) |
| GET | `/api/jobs?page=` | Histórico paginado |
| GET | `/api/jobs/{id}/files` | Arquivos de um job |
| POST | `/api/jobs/{id}/cancel` | Cancela job em andamento |
| GET/PUT | `/api/settings` | Configurações gerais |
| GET/POST/PUT/DELETE | `/api/cards` | Whitelist de cartões |
| GET/POST/PUT/DELETE | `/api/slots` | Slots calibrados |
| POST | `/api/slots/calibrate` | Inicia modo calibração: próximo volume detectado é associado ao apelido enviado |
| POST | `/api/telegram/test` | Envia mensagem de teste |

Autenticação: header `Authorization: Bearer {token}` em todas as rotas.

---

## 10. Fluxos principais

### 10.1 Caminho feliz
1. Cartão inserido → watcher detecta (≤ 2 s) → debounce 3 s.
2. Slot resolvido → cartão classificado como conhecido.
3. Scan da origem + dedup → job criado → **Telegram: início**.
4. Cópia concorrente com hash em streaming; progresso editado na mensagem e emitido via SSE.
5. Todos os hashes conferem → renames finais → índice de dedup atualizado.
6. Volume ejetado → **Telegram: conclusão + PNG + "pode remover o cartão do Leitor esquerdo"**.

### 10.2 Cartão removido no meio
Watcher detecta o detach → job `failed` → `.cardpit-tmp` removidos → **Telegram: alerta** com copiados/pendentes. Reinserção retoma via dedup (RF-03.8).

### 10.3 SSD de destino ausente
Cartão detectado, destino (por GUID) não montado → nenhum job inicia → **Telegram: alerta** "insira o SSD de destino". Watcher passa a monitorar também a montagem do destino; quando aparecer, jobs pendentes iniciam automaticamente.

### 10.4 Cartão desconhecido (política `perguntar`)
Detecção → **Telegram com botões**: `Copiar` / `Ignorar` / `Ignorar sempre`. `Copiar` cria o job e oferece cadastrar apelido pela UI; `Ignorar sempre` grava o cartão com `policy = ignore`.

---

## 11. Roadmap e critérios de aceite

### Fase 1 — Core CLI (fundação)
Watcher + slot resolver + copy engine + SQLite + logs JSON, operado por linha de comando.
**Aceite:** inserir cartão real → cópia íntegra organizada por data no SSD; reinserir → zero re-cópias; arrancar o cartão no meio → nenhum arquivo final corrompido e log do evento; dois cartões simultâneos → dois jobs concorrentes; log identifica o location path de cada um.

### Fase 2 — Telegram
Notifier completo: início, progresso por edit, conclusão com PNG, erros, botões inline.
**Aceite:** os 4 fluxos da seção 10 produzem as mensagens corretas; mensagens identificam o slot; nenhum flood (≤ 1 mensagem nova por evento de ciclo de vida).

### Fase 3 — API + UI web
REST + SSE + UI React embutida: dashboard, configurações, cartões, calibração de slots, histórico.
**Aceite:** calibrar 2 slots pelo wizard e vê-los nomeados no Telegram; alterar template de pastas pela UI e observar efeito no próximo job; acompanhar progresso ao vivo pelo celular na LAN.

### Fase 4 — Empacotamento
Serviço do Windows + tray + `make build` gerando `.exe` único.
**Aceite:** `cardpit install` + reboot → serviço ativo sem login; ingestão completa funciona sem nenhuma janela aberta; tray abre o painel.

### Fase 5 (futuro) — Extensões
Webhooks para ESP32 (LEDs por slot), segundo destino de backup, canal adicional de notificação.

---

## 12. Riscos e mitigações

| Risco | Impacto | Mitigação |
|---|---|---|
| APIs CfgMgr32/SetupAPI mal documentadas em Go | Atraso na fase 1 | Spike isolado de 1 dia validando a cadeia letra→porta antes de integrar; fallback: identificar só por serial do cartão (perde slot, mantém tudo o mais) |
| Leitores multi-slot com comportamento de LUN inconsistente | Slots trocados | Testar com o hardware real do usuário na calibração; chave inclui LUN + índice do volume |
| Windows atrasa montagem de cartões exFAT grandes | Cópia inicia cedo demais | Debounce + retry na abertura do volume |
| Telegram fora do ar / sem internet | Sem notificação | Fila local de mensagens com retry; a cópia nunca depende do notifier |
| Antivírus interferindo em cópia massiva | Throughput baixo | Documentar exclusão da pasta de destino no Defender |
| Letra de unidade reciclada entre detach/attach rápidos | Job fantasma | Identidade de job ancorada em volume GUID + serial, nunca em letra |

---

## 13. Questões em aberto

1. **Retenção do histórico**: manter `ingested_files` para sempre (é o índice de dedup) ou expurgar após N meses com dedup só por hash? *Proposta: manter — é barato em disco.*
2. **Progresso na mensagem de início vs. mensagem separada**: edit na mesma mensagem é mais limpo, mas o Telegram limita edits (~1/s por mensagem). *Proposta: edit com throttle de 30 s.*
3. **Formatar o cartão após ingestão confirmada?** Perigoso demais para v1. *Proposta: fora do escopo; no máximo um botão manual na UI em v2.*
4. Nome do template de pastas deve suportar variáveis do cartão (`{card_alias}`)? *Proposta: sim, custo baixo.*
