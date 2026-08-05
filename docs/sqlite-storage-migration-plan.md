# Plan: Migración del almacenamiento a SQLite

**Estado:** Propuesta
**Autor:** lele (agente coder)
**Fecha:** 2026-08-04

---

## 1. Motivación

Hoy lele persiste todo su estado como archivos JSON sueltos bajo `~/.lele/`. Esto ha funcionado, pero ya muestra límites:

- **Escritura de sesiones completa por turno**: cada `Save` reescribe el JSON entero de la sesión (hasta 10 000 mensajes). En sesiones grandes esto domina el tiempo de I/O (el freeze de TUI de #130 fue exactamente esto).
- **Listados y búsquedas lineales**: listar sesiones, buscar en historial o filtrar por modo requiere leer/deserializar archivos completos (el índice `_index.json` es un parche manual que hay que mantener consistente).
- **Sin transaccionalidad**: writes concurrentes de cron, subagentes y canales sobre archivos compartidos dependen de locks en memoria + rename atómico; no hay garantía entre archivos relacionados (p.ej. sesión + goal).
- **Estado fragmentado**: ~8 formatos/ubicaciones distintas, cada uno con su propio código de load/save, migración y manejo de corrupción.

SQLite da transacciones ACID, índices, queries y un único archivo respaldable, sin añadir un servicio externo (sigue siendo embedded, ideal para el modelo single-binary de lele).

### Objetivos

1. Un único archivo de base de datos (`~/.lele/lele.db`) como almacenamiento primario del estado del servicio.
2. Migración automática y reversible de los datos existentes.
3. Sin cambio de comportamiento visible (API REST, WS, TUI, canales intactos).
4. Preservar cross-compilation pura (matriz goreleaser: linux/darwin/windows/freebsd × amd64/arm64/arm/armv6/mips64/riscv64/s390x).

### No-objetivos

- Multi-usuario/servidor compartido (SQLite = un proceso escritor, que es nuestro modelo).
- Reemplazar `keyring.enc` (vault cifrado; se queda como está, con su keyfile).
- Mover contenido de workspaces (`MEMORY.md`, `HEARTBEAT.md`, daily logs): son archivos que el usuario/agent edita a mano y deben seguir siendo texto plano.
- Vector DB / embeddings (eso va por separado, ver ROADMAP).

---

## 2. Inventario actual

| Datos | Ubicación hoy | Formato | Volumen real | Escritura |
|---|---|---|---|---|
| Sesiones (historial completo) | `~/.lele/sessions/<key>.json` + `_index.json` | JSON por sesión | 249 sesiones / 44 MB | Muy alta (cada turno) |
| Cron jobs | `~/.lele/cron/jobs.json` | JSON array | Bajo | Baja |
| Goals | `~/.lele/goals/<sessionkey>.json` | JSON por goal | Bajo | Media |
| Group chat state | `~/.lele/groups/<id>.json` | JSON por grupo | Bajo | Media |
| OAuth credentials | `~/.lele/auth.json` | JSON map | Bajo | Baja |
| Native clients (WebUI tokens) | `~/.lele/native_clients.json` | JSON | Bajo | Baja |
| Telegram offset | `~/.lele/telegram_offset.txt` | Texto | 1 línea | Media |
| Workspace state (last channel/chat) | `<workspace>/state/state.json` | JSON | Bajo | Baja |
| **Excluidos** | `keyring.enc`, `MEMORY.md`, `HEARTBEAT.md`, `memory/*.md`, `config.json` | — | — | — |

`config.json` se queda como archivo (lo edita el usuario; `pkg/migrate` ya lo convierte entre versiones).

---

## 3. Elección de driver

La restricción dominante es la matriz de release: goreleaser compila en **pure Go** para targets exóticos (mips64, riscv64, s390x, armv6, FreeBSD). `mattn/go-sqlite3` requiere CGO y toolchains cruzados → descartado como default.

| Driver | CGO | Cross-compile | Notas |
|---|---|---|---|
| **`modernc.org/sqlite`** | No | ✅ todos los targets | SQLite completo traducido a Go. ~+10 MB binario, ~2–3× más lento que CGO (irrelevante para nuestro volumen) |
| `ncruces/go-sqlite3` | No (WASM) | ✅ | Más rápido que modernc, API distinta, depende de runtime WASM |
| `mattn/go-sqlite3` | Sí | ❌ sin toolchains | El más probado, pero rompe releases |

**Decisión propuesta: `modernc.org/sqlite`** vía `database/sql`. Es la opción estándar pure-Go, mantiene la matriz de releases intacta y el overhead es despreciable para nuestro patrón de uso (escrituras pequeñas, pocas por segundo). Si el benchmark de la Fase 1 mostrara un problema, `ncruces` es el fallback sin cambiar el resto del plan.

---

## 4. Esquema propuesto

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;

-- Metadatos de sesión (lo que hoy vive en _index.json)
CREATE TABLE sessions (
    key             TEXT PRIMARY KEY,
    name            TEXT NOT NULL DEFAULT '',
    mode            TEXT NOT NULL DEFAULT 'agent',  -- chat/agent/group
    summary         TEXT NOT NULL DEFAULT '',
    verbose_level   TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    thinking_level  TEXT NOT NULL DEFAULT '',
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    compaction_count INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,   -- RFC3339Nano
    updated_at      TEXT NOT NULL
);
CREATE INDEX idx_sessions_mode_updated ON sessions(mode, updated_at DESC);

-- Mensajes: uno por fila en vez de reescribir la sesión completa
CREATE TABLE session_messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_key TEXT NOT NULL REFERENCES sessions(key) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,          -- orden dentro de la sesión
    role        TEXT NOT NULL,
    content     TEXT NOT NULL DEFAULT '',  -- texto plano / TextOnlyContent
    message     TEXT NOT NULL,             -- providers.Message completo en JSON
    excluded    INTEGER NOT NULL DEFAULT 0,-- mensajes fuera de contexto (compaction)
    created_at  TEXT NOT NULL,
    UNIQUE(session_key, seq)
);
CREATE INDEX idx_messages_session ON session_messages(session_key, seq);

CREATE TABLE cron_jobs (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    schedule    TEXT NOT NULL,  -- CronSchedule JSON
    payload     TEXT NOT NULL,  -- message/command/spawn JSON
    state       TEXT NOT NULL,  -- next_run, last_run, etc. JSON
    scope       TEXT NOT NULL DEFAULT 'global',
    session_key TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE goals (
    session_key TEXT PRIMARY KEY,
    goal        TEXT NOT NULL,  -- Goal JSON
    updated_at  TEXT NOT NULL
);

CREATE TABLE groups_state (
    id         TEXT PRIMARY KEY,
    state      TEXT NOT NULL,  -- GroupState JSON
    updated_at TEXT NOT NULL
);

CREATE TABLE auth_credentials (
    provider_key TEXT PRIMARY KEY,
    credential   TEXT NOT NULL,  -- AuthCredential JSON (tokens incluidos: mismo nivel de protección que auth.json hoy; 0600)
    updated_at   TEXT NOT NULL
);

CREATE TABLE native_clients (
    id         TEXT PRIMARY KEY,
    client     TEXT NOT NULL,  -- JSON
    created_at TEXT NOT NULL
);

-- Clave/valor para restos pequeños (telegram_offset, state de workspace, etc.)
CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Control de migración
CREATE TABLE schema_meta (
    key   TEXT PRIMARY KEY,   -- 'schema_version', 'migrated_from_json'
    value TEXT NOT NULL
);
```

Notas de diseño:

- **`session_messages.message` guarda el `providers.Message` completo en JSON** (ContentParts, ToolCalls, Media). Normalizar esas estructuras en columnas sería frágil frente a la evolución del protocolo; el JSON por fila nos da granularidad de escritura sin acoplar el schema.
- El hot-path pasa de "reescribir 44 MB de JSON de sesión" a `INSERT`/`UPDATE` de una fila.
- `sessions` reemplaza `_index.json`: listar/filtrar por modo es una query con índice.
- Los payloads complejos (cron schedule/spawn, goal, group state) se quedan como JSON dentro de una fila: son opacos para la DB y evolucionan sin migraciones de schema.

---

## 5. Arquitectura

### 5.1 Nuevo paquete `pkg/store`

```
pkg/store/
├── store.go        # interfaz Store + constructor (abre DB, PRAGMAs, migraciones)
├── migrations.go   # schema versionado (DDL embebido, aplicado en orden)
├── sessions.go     # repositorio de sesiones/mensajes
├── cron.go         # repositorio de cron jobs
├── goals.go        # repositorio de goals
├── groups.go
├── auth.go
├── kv.go
└── store_test.go
```

Interfaz mínima (los repositorios concretos pueden crecer):

```go
type Store interface {
    Sessions() SessionRepo
    Cron() CronRepo
    Goals() GoalRepo
    Groups() GroupRepo
    Auth() AuthRepo
    KV() KVRepo
    Close() error
}
```

- Una sola conexión de escritura (SQLite lo exige en la práctica); `database/sql` con `SetMaxOpenConns(1)` para writes y pool de lectura si hiciera falta.
- El paquete no sabe de JSON legacy: la migración de datos vive aparte (§7).

### 5.2 Adaptación de los consumidores

Cada consumidor cambia de "archivos + mutex" a repositorio, **manteniendo su API pública**:

| Consumidor | Cambio |
|---|---|
| `pkg/session.SessionManager` | El más grande. `saveUnlocked` → upsert de metadatos + delta de mensajes (append de nuevas filas, update de la última en streaming). Se elimina `_index.json`, `loadSessionMetadataParallel`, `saveSeq` (la atomicidad la da la transacción). LRU/eviction en memoria se mantiene, pero "evictar" ya no requiere flush de archivo completo |
| `pkg/cron.CronService` | `loadStore`/`saveStoreUnsafe` → queries. Desaparece el lock global para persistir |
| `pkg/agent.GoalManager` | persist/removePersisted/loadFromDisk → repo |
| `pkg/group` (SaveGroup/LoadGroup/ListGroups) | → repo |
| `pkg/auth` (LoadStore/SaveStore) | → repo |
| `pkg/channels` native clients store | → repo |
| `pkg/channels/telegram.go` offset | `kv` |
| `pkg/state` (workspace state) | `kv` con prefijo por workspace |

El wiring se hace en `pkg/agent/instance.go` / `loop.go` (donde hoy se construyen `NewSessionManager`, `NewCronService`, etc.): se abre un único `store.Store` y se inyecta.

---

## 6. Fases de implementación

### Fase 0 — Cimientos (1 PR)
- Añadir `modernc.org/sqlite` a `go.mod`; verificar `go build` en todos los targets de goreleaser (al menos `GOOS=linux GOARCH=mips64`, `riscv64`, `freebsd/amd64`, `windows/386`).
- Crear `pkg/store` con schema versionado, PRAGMAs y tests básicos.
- Benchmark de humo vs JSON actual (save de sesión de 1 000 mensajes).

### Fase 1 — Stores pequeños (1 PR)
- Migrar cron, goals, groups, auth, native clients, telegram offset y workspace state a `pkg/store`.
- Cada uno con su repositorio + tests. Los archivos JSON legacy se siguen leyendo como fallback si no hay DB (ver §7).

### Fase 2 — Sesiones (1 PR, el grande)
- `SessionRepo` con append incremental de mensajes.
- Adaptar `SessionManager` preservando exactamente su API pública (la usan agent, channels, TUI, REST).
- Mantener el comportamiento de streaming-throttle (`lastStreamFlush`) pero persistiendo deltas.
- Tests de regresión: los existentes (`manager_test.go`, `manager_lockio_test.go`, etc.) deben pasar sin cambio de contrato. El test de "readers no bloqueados >50ms durante saves" debería mejorar.

### Fase 3 — Migración de datos + CLI (1 PR)
- `lele migrate-storage [--dry-run] [--rollback]`:
  1. Lee todos los JSON legacy → inserta en `lele.db` dentro de una transacción.
  2. Valida conteos y checksums (nº mensajes por sesión, jobs, goals…).
  3. Renombra el directorio legacy a `~/.lele/backup-json-<timestamp>/` (no borra nada).
  4. Escribe `schema_meta.migrated_from_json`.
- Auto-migración en el arranque si hay datos legacy y no hay DB (con log claro), para que actualizar de v0.5.x a la nueva versión sea transparente.

### Fase 4 — Limpieza (1 PR, tras un release de rodaje)
- Eliminar código JSON legacy (`_index.json`, load/save de archivos, `saveSeq`).
- Documentar backup/restore en `docs/deployment.md`.
- Opcional: `lele db export` para volver a JSON si alguien lo necesita.

---

## 7. Migración y rollback

- **Forward**: la Fase 3 nunca borra los JSON; los mueve a un backup. Si la DB falla en el arranque y existe el backup, se loguea y se ofrece `lele migrate-storage --rollback` (restaura los JSON y elimina la DB).
- **Dual-read transitorio**: durante las Fases 1–2, si la tabla está vacía pero existe el JSON, el repositorio lee del JSON (migración lazy por acceso) — cubre el caso de quien actualice sin ejecutar la migración explícita.
- **Versionado de schema**: `schema_meta.schema_version` + migraciones DDL secuenciales e idempotentes (patrón simple, sin framework externo).

## 8. Riesgos y mitigaciones

| Riesgo | Impacto | Mitigación |
|---|---|---|
| Binario +~10 MB (modernc) | Estético/distribución | Aceptable; goreleaser ya comprime. Medir en Fase 0 |
| Rendimiento modernc en writes | Bajo para nuestro volumen | Benchmark Fase 0; fallback `ncruces` |
| Corrupción de la DB | Pierde todo el estado junto | WAL + `synchronous=NORMAL`; backup JSON retenido; `PRAGMA integrity_check` en el arranque tras crash |
| Concurrencia escritor | Bloqueos | Un proceso (nuestro modelo ya es así); `busy_timeout`; pool `MaxOpenConns(1)` para writes |
| Regresión en `SessionManager` (zona muy tocada: #129, #130, #131) | Alta | Mantener API pública exacta; suite de tests existente como contrato; lock-free reads sigue siendo requisito |
| Targets exóticos (mips64, riscv64) | Build roto | Verificación explícita en Fase 0 y CI (job de cross-compile en `pr.yml`) |

## 9. Testing

- Unit por repositorio (temp dirs, `t.TempDir()`).
- `SessionManager`: suite existente sin cambios de contrato + tests nuevos de append incremental y de que un crash a mitad de transacción no pierde mensajes ya confirmados.
- Migración: test golden con fixtures de JSON legacy reales (sesión con ContentParts/ToolCalls/Media, jobs con spawn config, goals).
- CI: añadir job `cross-compile` (mips64/riscv64/freebsd) a `pr.yml` para que un cambio de dependencias no rompa releases silenciosamente.
- El test de contención de locks (`manager_lockio_test.go`) se mantiene como SLO: readers nunca bloqueados >50ms.

## 10. Preguntas abiertas

1. ¿`lele.db` cifrado (SQLCipher-like) para tokens OAuth? Hoy `auth.json` está en plano; SQLite no empeora nada, pero podríamos aprovechar para cifrar al menos `auth_credentials`. Propuesta: no en este proyecto, issue aparte.
2. ¿Retención de sesiones? Con mensajes por fila es barato añadir `lele sessions prune --older-than` (fuera de alcance aquí).
3. ¿Un solo `lele.db` global o uno por workspace para `state`? Propuesta: uno global con prefijos en `kv` (más simple, un solo backup).

---

## Resumen ejecutivo

- **Qué**: consolidar el estado del servicio (sesiones, cron, goals, groups, auth, clientes, offsets, workspace state) en un único SQLite (`~/.lele/lele.db`) vía nuevo paquete `pkg/store`.
- **Cómo**: driver pure-Go `modernc.org/sqlite` (preserva cross-compilation), 4 fases con PRs independientes, migración automática reversible que conserva los JSON como backup.
- **Por qué ahora**: el parche del freeze (#130) y el índice manual de sesiones muestran que el modelo JSON por archivo ya no escala; SQLite elimina esa clase de bugs de raíz sin cambiar el modelo embedded de lele.
