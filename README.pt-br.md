<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">

  <h1>Lele</h1>

  <p>Assistente pessoal de IA leve e eficiente em Go.</p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="Licença">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md) | English
</div>

---

Lele é um projeto independente focado em oferecer um assistente de IA prático, com pegada leve, inicialização rápida e um modelo de implantação direto.

Hoje, o projeto é muito mais do que um bot CLI mínimo. Inclui um runtime de agente configurável, gateway multicanal, interface web, API de cliente nativa, tarefas agendadas, subagentes e um modelo de automação centrado no workspace.

## Por que Lele

- Implementação leve em Go com pequena pegada operacional
- Eficiente o suficiente para rodar tranquilamente em máquinas e placas Linux modestas
- Um único projeto para CLI, canais de chat, interface web e integrações com clientes locais
- Roteamento configurável de provedores com suporte a backends diretos e compatíveis com OpenAI
- Design workspace-first com habilidades (skills), memória, tarefas agendadas e controles de sandbox

## Recursos Atuais

### Runtime do Agente

- Chat via CLI com `lele agent`
- Loop de agente com uso de ferramentas e limites configuráveis de iteração
- Anexos de arquivos em fluxos nativos/web
- Persistência de sessão e sessões efêmeras opcionais
- Agentes nomeados, vinculações e fallbacks de modelo

### Interfaces

- Uso via terminal através da CLI
- Modo gateway para canais de chat
- Interface web embutida
- Canal de cliente nativo com API REST + WebSocket e emparelhamento por PIN

### Automação

- Tarefas agendadas com `lele cron`
- Tarefas periódicas baseadas em heartbeat a partir de `HEARTBEAT.md`
- Subagentes assíncronos para trabalho delegado
- Sistema de habilidades (skills) para fluxos de trabalho reutilizáveis

### Segurança e Operações

- Suporte a restrição de workspace
- Padrões de negação para comandos perigosos em ferramentas exec
- Fluxo de aprovação para ações sensíveis
- Logs, comandos de status e gerenciamento de configuração

## Status do Projeto

Lele é um projeto independente em evolução ativa.

O código atual já suporta:

- fluxos de gateway estilo produção
- caminho para cliente web/nativo
- roteamento configurável para múltiplos provedores
- múltiplos canais de mensagens
- habilidades, subagentes e automação agendada

A principal lacuna de documentação era que o README antigo ainda descrevia uma identidade de fork anterior e não correspondia ao conjunto atual de funcionalidades. Este README reflete o projeto como ele existe agora.

## Início Rápido

### Instalação a Partir do Código-Fonte

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

O binário é gerado em `build/lele`.

### Configuração Inicial

```bash
lele onboard
```

O `onboard` cria a configuração base, os templates do workspace e pode opcionalmente habilitar a interface web e gerar um PIN de emparelhamento para o fluxo de cliente nativo/web.

### Uso Mínimo da CLI

```bash
lele agent -m "O que você sabe fazer?"
```

## Interface Web e Fluxo do Cliente Nativo

Lele agora inclui uma interface web local além de um canal de cliente nativo.

Fluxo típico:

1. Execute `lele onboard`
2. Habilite a interface web quando solicitado
3. Gere um PIN de emparelhamento
4. Inicie os serviços com `lele gateway` e `lele web start`
5. Abra o app web no seu navegador e emparelhe com o PIN

O canal nativo expõe endpoints REST e WebSocket para clientes desktop e integrações locais.

Consulte `docs/client-api.md` para a API completa.

## Configuração

Arquivo de configuração principal:

```text
~/.lele/config.json
```

Exemplo de template de configuração:

```text
config/config.example.json
```

Áreas principais que você pode configurar:

- `agents.defaults`: workspace, provedor, modelo, limites de tokens, limites de ferramentas
- `session`: comportamento de sessão efêmera e links de identidade
- `channels`: gateway e integrações de mensagens
- `providers`: provedores diretos e backends nomeados compatíveis com OpenAI
- `tools`: busca web, configurações de segurança do cron e exec
- `heartbeat`: execução de tarefas periódicas
- `gateway`, `logs`, `devices`

### Exemplo Mínimo

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true,
      "model": "glm-4.7",
      "max_tokens": 8192,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "SUA_CHAVE_API"
    }
  }
}
```

## Provedores

Lele suporta tanto provedores embutidos quanto definições de provedores nomeados.

Famílias de provedores embutidos atualmente representadas na configuração/runtime incluem:

- `anthropic`
- `openai`
- `openrouter`
- `groq`
- `zhipu`
- `gemini`
- `vllm`
- `nvidia`
- `ollama`
- `moonshot`
- `deepseek`
- `github_copilot`

O projeto também suporta entradas de provedores nomeados compatíveis com OpenAI com configurações por modelo, como:

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

## Canais

O gateway atualmente inclui configuração para:

- `telegram`
- `discord`
- `whatsapp`
- `feishu`
- `slack`
- `line`
- `onebot`
- `qq`
- `dingtalk`
- `maixcam`
- `native`
- `web`

Alguns canais são integrações simples baseadas em token, enquanto outros exigem configuração de webhook ou bridge.

## Estrutura do Workspace

Workspace padrão:

```text
~/.lele/workspace/
```

Conteúdo típico:

```text
~/.lele/workspace/
├── sessions/
├── memory/
├── state/
├── cron/
├── skills/
├── AGENT.md
├── HEARTBEAT.md
├── IDENTITY.md
├── SOUL.md
└── USER.md
```

Essa estrutura centrada no workspace é parte do que mantém o Lele prático e eficiente: estado, prompts, habilidades e automação vivem em um lugar previsível.

## Agendamento, Habilidades e Subagentes

### Tarefas Agendadas

Use `lele cron` para criar tarefas únicas ou recorrentes.

Exemplos:

```bash
lele cron list
lele cron add --name lembrete --message "Verificar backups" --every "2h"
```

### Heartbeat

O Lele pode periodicamente ler o `HEARTBEAT.md` do workspace e executar tarefas automaticamente.

### Habilidades (Skills)

Habilidades embutidas e personalizadas podem ser gerenciadas com:

```bash
lele skills list
lele skills search
lele skills install <skill>
```

### Subagentes

O Lele suporta trabalho delegado assíncrono através de subagentes. Isso é útil para tarefas de longa duração ou paralelizáveis.

Consulte `docs/SKILL_SUBAGENTS.md` para detalhes.

## Modelo de Segurança

O Lele pode restringir o acesso do agente a arquivos e comandos ao workspace configurado.

Controles principais incluem:

- `restrict_to_workspace`
- Padrões de negação para exec
- Fluxo de aprovação para ações sensíveis
- Autenticação por token para clientes nativos
- Limites de upload e TTL para uploads de arquivos nativos

Consulte `docs/tools_configuration.md` e `docs/client-api.md` para detalhes operacionais.

## Referência da CLI

| Comando | Descrição |
| --- | --- |
| `lele onboard` | Inicializa configuração e workspace |
| `lele agent` | Inicia sessão interativa do agente |
| `lele agent -m "..."` | Executa um prompt único |
| `lele gateway` | Inicia gateway de mensagens |
| `lele web start` | Inicia a interface web embutida |
| `lele web status` | Mostra status da interface web |
| `lele auth login` | Autentica provedores suportados |
| `lele status` | Mostra status do runtime |
| `lele cron list` | Lista tarefas agendadas |
| `lele cron add ...` | Adiciona uma tarefa agendada |
| `lele skills list` | Lista habilidades instaladas |
| `lele client pin` | Gera um PIN de emparelhamento |
| `lele client list` | Lista clientes nativos emparelhados |
| `lele version` | Mostra informações de versão |

## Documentação Adicional

- `docs/agents-models-providers.md`
- `docs/architecture.md`
- `docs/channel-setup.md`
- `docs/cli-reference.md`
- `docs/config-reference.md`
- `docs/client-api.md`
- `docs/deployment.md`
- `docs/examples.md`
- `docs/installation-and-onboarding.md`
- `docs/logging-and-observability.md`
- `docs/model-routing.md`
- `docs/security-and-sandbox.md`
- `docs/session-and-workspace.md`
- `docs/skills-authoring.md`
- `docs/tools_configuration.md`
- `docs/troubleshooting.md`
- `docs/web-ui.md`
- `docs/SKILL_SUBAGENTS.md`
- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`

## Desenvolvimento

Targets úteis:

```bash
make build
make test
make fmt
make vet
make check
```

## Licença

MIT
