<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!--
  LaunchAgent: web-агент как постоянный сервис (День 30).
  Шаблон: {{REPO}} подставляется при установке (make service-install).
  Настройки подключения агент читает из {{REPO}}/agent/.env (каскад llm).
  Запускается собранный бинарник bin/agent-web (без go run — не держит
  тулчейн Go в памяти).
-->
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>aichallenge.agent-web</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{REPO}}/agent/bin/agent-web</string>
    <string>--config</string>
    <string>config.service.json</string>
  </array>
  <key>WorkingDirectory</key>
  <string>{{REPO}}/agent</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/aichallenge-agent-web.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/aichallenge-agent-web.err.log</string>
</dict>
</plist>
