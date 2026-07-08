# Windows: cadeia letra → slot físico (referência)

Este documento descreve a cadeia de chamadas Win32 usada pelo
`platform/win` para transformar uma letra de unidade na identidade física
do slot (`location_path + LUN`), além dos IOCTLs de ejeção e do DPAPI.
Nada disso roda em CI (o CI apenas cross-compila) — em caso de dúvida,
este é o mapa para depurar no hardware real.

## A cadeia

```
"E:\"                                             (letra, efêmera)
  │  GetVolumeNameForVolumeMountPoint
  ▼
\\?\Volume{xxxxxxxx-...}\                          (volume GUID, estável p/ o volume)
  │  CreateFile (sem trailing \) + DeviceIoControl
  │  IOCTL_STORAGE_GET_DEVICE_NUMBER = 0x002D1080
  ▼
disk number N                                      (PhysicalDriveN)
  │  CM_Get_Device_Interface_ListW(GUID_DEVINTERFACE_DISK)
  │  → abre cada interface, mesmo IOCTL, compara N
  ▼
\\?\usbstor#disk&ven_...#7&1f30c9&0&SERIAL&1#{...} (interface do disco)
  │  CM_Get_Device_Interface_PropertyW(DEVPKEY_Device_InstanceId)
  ▼
USBSTOR\DISK&VEN_...\7&1F30C9&0&SERIAL&1           (device instance ID)
  │  CM_Locate_DevNodeW
  ▼
devnode do disco
  │  DEVPKEY_Device_Address  → LUN (fallback: sufixo "&N" do instance ID)
  │  CM_Get_Parent (subindo ≤ 10 níveis)
  │  DEVPKEY_Device_LocationPaths no primeiro nó que responder
  ▼
"PCIROOT(0)#PCI(0x14,0x00)#USBROOT(0)#USB(2)#USB(3)"   ← SlotKey.LocationPath
```

Propriedades-chave:

| Constante | Valor |
|---|---|
| `GUID_DEVINTERFACE_DISK` | `{53f56307-b6bf-11d0-94f2-00a0c91efb8b}` |
| `DEVPKEY_Device_InstanceId` | `{78c34fc8-104a-4aca-9ea4-524d52996e57}`, PID 256, `DEVPROP_TYPE_STRING (0x12)` |
| `DEVPKEY_Device_LocationPaths` | `{a45c254e-df1c-4efd-8020-67d146a850e0}`, PID 37, `DEVPROP_TYPE_STRING_LIST (0x2012)` |
| `DEVPKEY_Device_Address` | `{a45c254e-df1c-4efd-8020-67d146a850e0}`, PID 30, `DEVPROP_TYPE_UINT32 (0x07)` |
| `IOCTL_STORAGE_GET_DEVICE_NUMBER` | `0x002D1080` |
| `IOCTL_STORAGE_EJECT_MEDIA` | `0x002D4808` |
| `FSCTL_LOCK_VOLUME` / `FSCTL_DISMOUNT_VOLUME` | `0x00090018` / `0x00090020` |

Por que essa identidade funciona (RF-02.2):

- **Location path** é derivado da topologia física (porta do hub/controlador),
  não da ordem de enumeração — estável entre reboots e reinserções.
- Leitores **multi-slot** expõem um único dispositivo USB com vários LUNs:
  os volumes compartilham o location path e diferem no LUN.
- Leitores que expõem cada slot como dispositivo USB separado diferem no
  próprio location path — a chave `(location_path, lun)` cobre os dois casos.

Falha em qualquer elo → `ErrSlotUnknown`: a ingestão prossegue e o job é
reportado com "slot desconhecido" (fallback do PRD).

## Ejeção (RF-03.7)

`FSCTL_LOCK_VOLUME` (5 tentativas, 500 ms) → `FSCTL_DISMOUNT_VOLUME` →
`IOCTL_STORAGE_EJECT_MEDIA`. Falha é **não-fatal**: log + aviso "remova
manualmente" — o conteúdo já está íntegro no destino.

## DPAPI

`CryptProtectData`/`CryptUnprotectData` com `CRYPTPROTECT_LOCAL_MACHINE`:
o serviço (LocalSystem) e o tray (sessão do usuário) precisam abrir os
mesmos segredos. Blobs ficam na tabela `settings` com prefixo `dpapi:`.

## Nota sobre xxHash

O hash usado é **XXH3-64** (`zeebo/xxh3`), não o xxHash64 clássico. É
internamente consistente (só comparamos com o nosso próprio índice), mas
**não troque a lib** sem invalidar/reconstruir o índice de dedup.

---

# Checklist de testes manuais no Windows

Executar na primeira instalação em hardware real (nada disso roda em CI).

## Fase 1 — pipeline
- [ ] `cardpit.exe run --config config.yaml` no console; log mostra `platform=windows`.
- [ ] Inserir cartão com DCIM → cópia organizada por data no SSD; hashes no histórico.
- [ ] Log da attach mostra `location_path` e `lun` reais.
- [ ] Reinserir o mesmo cartão sem formatar → `files_skipped` = tudo, zero cópias.
- [ ] Arrancar o cartão no meio da cópia → job `failed`, nenhum `*.cardpit-tmp` no destino, nenhum arquivo final corrompido; reinserir retoma.
- [ ] Dois cartões em dois leitores → dois jobs concorrentes com slots distintos.
- [ ] Leitor multi-slot: SD + microSD simultâneos → mesmo location path, LUNs diferentes.

## Fase 2 — Telegram
- [ ] Token + chat_id na UI → "Enviar mensagem de teste" chega.
- [ ] Início/progresso (edit)/conclusão + PNG chegam; conclusão diz o slot certo.
- [ ] Cartão desconhecido → botões inline; cada um dos 3 botões funciona.
- [ ] Mensagem de outro chat_id é ignorada.
- [ ] Desconectar a internet durante uma cópia → cópia termina; mensagens chegam depois (fila com retry).

## Fase 3 — UI
- [ ] Wizard: calibrar 2 slots e vê-los nomeados no Telegram e no painel.
- [ ] Trocar template de pastas pela UI → próximo job usa o novo template.
- [ ] Acompanhar progresso ao vivo pelo celular na LAN (SSE).

## Fase 4 — serviço
- [ ] `cardpit.exe install` (admin) + reboot → serviço ativo sem login.
- [ ] Ingestão completa sem nenhuma janela aberta.
- [ ] `cardpit.exe tray` → ícone; "Abrir painel" abre o navegador; "Pausar detecção" reflete na UI.
- [ ] Ejeção automática: Windows toca o som de remoção e o volume some do Explorer.

## Descobrir o volume GUID do SSD de destino

```powershell
Get-Volume | Select-Object DriveLetter, FileSystemLabel, Path
```

Copie o `Path` (`\\?\Volume{...}\`) para Configurações → Destino.

## Antivírus

Adicionar a pasta de destino às exclusões do Defender evita throughput
degradado em cópias massivas (risco documentado no PRD §12).
