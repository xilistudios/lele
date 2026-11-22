import { spawnSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import path from 'node:path'
import type { FullConfig } from '@playwright/test'

const ENTRY = 'e2e/harness/ordering-harness.ts'
const OUTFILE = 'e2e/harness/dist/harness.js'

export default function globalSetup(config: FullConfig): void {
  const webDir = config.configFile ? path.dirname(config.configFile) : process.cwd()
  mkdirSync(path.dirname(path.join(webDir, OUTFILE)), { recursive: true })

  const result = spawnSync(
    'bun',
    ['build', ENTRY, `--outfile=${OUTFILE}`, '--format=iife', '--target=browser'],
    { cwd: webDir, encoding: 'utf8' },
  )

  if (result.error) {
    throw new Error(`harness build failed to spawn bun: ${result.error.message}`)
  }
  if (result.status !== 0) {
    throw new Error(
      `harness build failed (exit ${result.status})\n` +
        `command: bun build ${ENTRY} --outfile=${OUTFILE} --format=iife --target=browser\n` +
        `stdout:\n${result.stdout ?? ''}\nstderr:\n${result.stderr ?? ''}`,
    )
  }
}
