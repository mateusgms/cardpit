# Changelog

All notable changes to cardpit are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- descoberta de SSDs portáteis/removíveis (incluindo nome do hardware) e proteção contra ingestão do próprio destino
- descoberta de leitores USB e cartões já conectados durante a inicialização
- pastas padrão por data e período (`Dia`, `Tarde`, `Noite`)
- estimativa ao vivo de velocidade e tempo restante no painel e Telegram
- canal público de releases para atualização manual e automática sem acesso ao repositório privado

### Changed
- releases públicas não incorporam mais credenciais do Telegram; novas instalações configuram token e chats pela UI ou pelo ambiente do serviço

## [v0.1.9] - 2026-07-20

### Added
- modo kiosk — slots auto-nomeados, copiar sempre e destino D: padrão
- indica chat IDs pré-configurados na tela de Configurações
- pré-configura o chat ID do Telegram via TELEGRAM_CHAT_ID


## [v0.1.8] - 2026-07-10

### Added
- seletor de disco de destino, retomada imediata da fila e logs visíveis


## [v0.1.7] - 2026-07-09

### Added
- embute o token do Telegram no exe durante o build
- pré-configura o token do Telegram via TELEGRAM_KEY


## [v0.1.6] - 2026-07-09

### Fixed
- manual check bypasses auto_update setting

## [v0.1.5] - 2026-07-09

### Added
- add CHANGELOG and auto-generate release notes


<!-- New version entries are automatically inserted here by the release workflow -->
