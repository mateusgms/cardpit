@echo off
REM setup.bat — instala e inicia o cardpit como serviço Windows.
REM Solicita elevação UAC automaticamente (admin necessário).
PowerShell -NoProfile -ExecutionPolicy Bypass -Command ^
  "Start-Process -FilePath '%~dp0cardpit.exe' -ArgumentList 'setup','--config','%~dp0config.yaml' -Verb RunAs -Wait"
pause
