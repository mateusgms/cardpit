# Instalação do cardpit

## Pré-requisitos

Nenhum. Apenas o arquivo `cardpit.exe` (Windows x64).

## Instalação — passo a passo

1. Crie uma pasta permanente, ex.: `C:\cardpit\`
2. Copie os arquivos do zip para essa pasta:
   - `cardpit.exe`
   - `config.yaml`
   - `setup.bat`
3. Clique duas vezes em **`setup.bat`**
   - O Windows pedirá confirmação de UAC ("Deseja permitir…") — clique em **Sim**
   - O terminal exibirá o **token de acesso** — anote-o
4. Abra **http://localhost:8532** no navegador e cole o token
5. Configure na interface:
   - **Volume GUID do SSD** de destino (`Get-Volume | Select FriendlyName,Path` no PowerShell)
   - **Bot do Telegram** (token + chat_id) — teste com "Enviar mensagem de teste"
   - **Slots** na aba Slots (insira um cartão em cada leitor para calibrar)

O serviço inicia automaticamente com o Windows e reinicia sozinho em caso de falha.

## Atualização automática

O cardpit verifica por novas versões ao iniciar e uma vez por dia.
Quando encontra uma versão mais nova:
1. Baixa o novo executável
2. Verifica a integridade (SHA-256)
3. Aguarda o término das cópias em andamento
4. Troca o executável em disco e reinicia o serviço automaticamente

Para **desativar** as atualizações automáticas: acesse Configurações → desative "Atualização automática".

## Recuperar o token de acesso

Se precisar do token novamente:

```
cardpit.exe token
```

## Remover o serviço

```
cardpit.exe stop
cardpit.exe uninstall
```

## Suporte

Repositório: https://github.com/mateusgms/cardpit
