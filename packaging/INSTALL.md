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
5. Confira na interface (tudo já vem configurado para o modo kiosk):
   - **SSD de destino** — a unidade **D:** é selecionada automaticamente
     quando montada; use o seletor só para escolher outro disco
   - **Bot do Telegram** — informe o token e o **chat_id** e use
     "Enviar mensagem de teste". As releases públicas não contêm
     credenciais.
   - **Slots** — cada leitor ganha um nome fixo automaticamente na primeira
     vez que um cartão é inserido (avisado no Telegram); etiquete o leitor
     físico com o nome atribuído. A aba Slots guarda o histórico.
   - **Cartões** — qualquer cartão plugado é copiado por padrão, sem perguntas

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

## Releases e suporte

Releases: https://github.com/mateusgms/cardpit-releases/releases
