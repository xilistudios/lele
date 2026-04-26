<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">

  <h1>Lele</h1>

  <p>Assistant personnel IA léger et efficace en Go.</p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | **Français** | [Español](README.es.md) | [English](README.md)
</div>

---

Lele est un projet indépendant axé sur la fourniture d'un assistant IA pratique avec une empreinte réduite, un démarrage rapide et un modèle de déploiement simple.

Aujourd'hui, le projet va bien au-delà d'un simple bot CLI minimal. Il inclut un runtime d'agent configurable, une passerelle multi-canal, une interface web, une API client native, des tâches planifiées, des sous-agents et un modèle d'automatisation centré sur l'espace de travail.

## Pourquoi Lele

- Implémentation légère en Go avec une empreinte opérationnelle réduite
- Assez efficace pour fonctionner confortablement sur des machines et cartes Linux modestes
- Un seul projet pour le CLI, les canaux de discussion, l'interface web et les intégrations clients locales
- Routage configurable des fournisseurs avec prise en charge des backends directs et compatibles OpenAI
- Conception orientée espace de travail avec compétences, mémoire, tâches planifiées et contrôles bac à sable

## Fonctionnalités Actuelles

### Runtime d'Agent

- Discussion CLI avec `lele agent`
- Boucle d'agent avec utilisation d'outils et limites d'itération configurables
- Pièces jointes dans les flux natifs/web
- Persistance des sessions et sessions éphémères optionnelles
- Agents nommés, liaisons et mécanismes de secours pour les modèles

### Interfaces

- Utilisation terminal via le CLI
- Mode passerelle pour les canaux de discussion
- Interface web intégrée
- Canal client natif avec API REST + WebSocket et appairage par PIN

### Automatisation

- Tâches planifiées avec `lele cron`
- Tâches périodiques basées sur le heartbeat via `HEARTBEAT.md`
- Sous-agents asynchrones pour le travail délégué
- Système de compétences pour des workflows réutilisables

### Sécurité et Opérations

- Prise en charge de la restriction à l'espace de travail
- Motifs de refus de commandes dangereuses pour les outils exec
- Flux d'approbation pour les actions sensibles
- Journaux, commandes d'état et gestion de la configuration

## Statut du Projet

Lele est un projet autonome en évolution active.

Le code actuel prend déjà en charge :

- des flux de passerelle de type production
- un parcours client web/natif
- un routage multi-fournisseur configurable
- plusieurs canaux de messagerie
- des compétences, sous-agents et automatisation planifiée

Le principal écart de documentation était que l'ancien README décrivait encore une identité de fork antérieur et ne correspondait pas à l'ensemble de fonctionnalités actuel. Ce README reflète le projet tel qu'il existe aujourd'hui.

## Démarrage Rapide

### Installation depuis les Sources

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

Le binaire est écrit dans `build/lele`.

### Configuration Initiale

```bash
lele onboard
```

`onboard` crée la configuration de base, les modèles d'espace de travail, et peut optionnellement activer l'interface web et générer un PIN d'appairage pour le flux client natif/web.

### Utilisation CLI Minimale

```bash
lele agent -m "Que peux-tu faire ?"
```

## Interface Web et Flux Client Natif

Lele inclut désormais une interface web locale ainsi qu'un canal client natif.

Flux typique :

1. Exécutez `lele onboard`
2. Activez l'interface web lorsque vous y êtes invité
3. Générez un PIN d'appairage
4. Démarrez les services avec `lele gateway`
5. Ouvrez l'application web dans votre navigateur et appairez avec le PIN

Le canal natif expose des points d'accès REST et WebSocket pour les clients de bureau et les intégrations locales.

Consultez `docs/client-api.md` pour l'API complète.

## Configuration

Fichier de configuration principal :

```text
~/.lele/config.json
```

Exemple de modèle de configuration :

```text
config/config.example.json
```

Domaines clés que vous pouvez configurer :

- `agents.defaults` : espace de travail, fournisseur, modèle, limites de jetons, limites d'outils
- `session` : comportement de session éphémère et liens d'identité
- `channels` : passerelle et intégrations de messagerie
- `providers` : fournisseurs directs et backends nommés compatibles OpenAI
- `tools` : recherche web, paramètres de sécurité exec
- `heartbeat` : exécution de tâches périodiques
- `gateway`, `logs`, `devices`

### Exemple Minimal

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
      "api_key": "YOUR_API_KEY"
    }
  }
}
```

## Fournisseurs

Lele prend en charge à la fois des fournisseurs intégrés et des définitions de fournisseurs nommés.

Les familles de fournisseurs intégrés actuellement représentées dans la configuration/runtime incluent :

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

Le projet prend également en charge les entrées de fournisseurs nommés compatibles OpenAI avec des paramètres par modèle tels que :

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

## Canaux

La passerelle inclut actuellement la configuration pour :

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

Certains canaux sont de simples intégrations basées sur des jetons, tandis que d'autres nécessitent une configuration webhook ou pont.

## Structure de l'Espace de Travail

Espace de travail par défaut :

```text
~/.lele/workspace/
```

Contenu typique :

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

Cette structure centrée sur l'espace de travail fait partie de ce qui rend Lele pratique et efficace : l'état, les invites, les compétences et l'automatisation vivent à un endroit prévisible.

## Planification, Compétences et Sous-Agents

### Tâches Planifiées

Utilisez `lele cron` pour créer des tâches ponctuelles ou récurrentes.

Exemples :

```bash
lele cron list
lele cron add --name reminder --message "Vérifier les sauvegardes" --every "2h"
```

### Heartbeat

Lele peut lire périodiquement `HEARTBEAT.md` depuis l'espace de travail et exécuter des tâches automatiquement.

### Compétences (Skills)

Les compétences intégrées et personnalisées peuvent être gérées avec :

```bash
lele skills list
lele skills search
lele skills install <skill>
```

### Sous-Agents

Lele prend en charge le travail délégué asynchrone via des sous-agents. Cela est utile pour les tâches de longue durée ou parallélisables.

Consultez `docs/SKILL_SUBAGENTS.md` pour plus de détails.

## Modèle de Sécurité

Lele peut restreindre l'accès aux fichiers et aux commandes de l'agent à l'espace de travail configuré.

Les contrôles clés incluent :

- `restrict_to_workspace`
- motifs de refus pour exec
- flux d'approbation pour les actions sensibles
- authentification par jeton pour les clients natifs
- limites de téléchargement et TTL pour les fichiers natifs

Consultez `docs/tools_configuration.md` et `docs/client-api.md` pour les détails opérationnels.

## Référence CLI

| Commande | Description |
| --- | --- |
| `lele onboard` | Initialiser la config et l'espace de travail |
| `lele agent` | Démarrer une session agent interactive |
| `lele agent -m "..."` | Exécuter une invite ponctuelle |
| `lele gateway` | Démarrer la passerelle de messagerie |
| `lele auth login` | Authentifier les fournisseurs pris en charge |
| `lele status` | Afficher le statut du runtime |
| `lele cron list` | Lister les tâches planifiées |
| `lele cron add ...` | Ajouter une tâche planifiée |
| `lele skills list` | Lister les compétences installées |
| `lele client pin` | Générer un PIN d'appairage |
| `lele client list` | Lister les clients natifs appairés |
| `lele version` | Afficher les informations de version |

## Documentation Supplémentaire

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

## Développement

Commandes utiles :

```bash
make build
make test
make fmt
make vet
make check
```

## Licence

MIT