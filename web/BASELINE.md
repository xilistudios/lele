# WebUI Test Baseline (T0.1)

**Commit base:** `707e0dd` (fix(channels): GW-L9 SSE streams survive server WriteTimeout #309)

## Señales baseline

| Command                  | Result              | Notes                                      |
| ------------------------ | ------------------- | ------------------------------------------ |
| `bun test`               | 845 pass / 5 fail   | Contaminación cross-file, **NO usable**    |
| `bun test --isolate`     | 850 pass / 0 fail   | ~238s. **Señal canónica**                  |
| `bunx tsc --noEmit`      | limpio              |                                            |
| `bun run lint`           | 51 errores          | 29 format/imports + 22 reglas (22 → PR6)  |
| `bun run build`          | OK                  | ~22.5s                                     |

## Causalidad de los 5 fallos (no aislados)

1. **`src/components/molecules/ChatComposer.test.tsx`** — usa `mock.module` que se fuga al
   registry global del proceso. Prueba:
   `bun test ChatComposer.test.tsx AgentEntityLayout.test.tsx` → 4 fail;
   sin ChatComposer → 0 fail.

2. **`App.test.tsx` → `useChatHistory.groups.test.ts`** — segundo contaminador con
   `mock.module`.

## Por qué `--isolate` es canónico

Cada test file corre en su propio proceso, eliminando fugas de `mock.module` entre archivos.
Hasta que T0.2 elimine los contaminadores, `--isolate` es el único modo con señal limpia.
