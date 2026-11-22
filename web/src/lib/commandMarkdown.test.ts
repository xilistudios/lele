import { describe, expect, test } from 'bun:test'
import {
  COMMAND_FRONTMATTER_KEYS,
  COMMAND_NAME_MAX_LEN,
  type CommandFields,
  commandNameError,
  emptyCommandFields,
  isValidCommandName,
  parseCommandMarkdown,
  serializeCommandMarkdown,
} from './commandMarkdown'

function fields(overrides: Partial<CommandFields> = {}): CommandFields {
  return { ...emptyCommandFields(), ...overrides }
}

describe('isValidCommandName', () => {
  test('accepts the backend regex: lowercase alnum start, then . _ -', () => {
    for (const name of ['review', 'deploy', 'a', '0hotfix', 'fix-up', 'fix_up', 'fix.v2']) {
      expect(isValidCommandName(name)).toBe(true)
    }
  })

  test('rejects what the server answers with 400 name_invalid', () => {
    for (const name of [
      '',
      'Review',
      'review pr',
      '/review',
      '-review',
      '.review',
      'review/pr',
      '../../etc/passwd',
      'revisãon',
      'revièw',
    ]) {
      expect(isValidCommandName(name)).toBe(false)
    }
  })
})

describe('commandNameError', () => {
  test('null means the server would accept the name', () => {
    for (const name of ['review', 'a'.repeat(COMMAND_NAME_MAX_LEN)]) {
      expect(commandNameError(name)).toBeNull()
    }
  })

  test('names the rule that failed, so the form can explain it inline', () => {
    expect(commandNameError('Bad Name')).toBe('invalid')
    expect(commandNameError('')).toBe('invalid')
    expect(commandNameError('a'.repeat(COMMAND_NAME_MAX_LEN + 1))).toBe('tooLong')
    expect(commandNameError('review.md')).toBe('reservedExt')
    expect(commandNameError('notes.markdown')).toBe('reservedExt')
  })

  test('reports the length before the extension, as the server does', () => {
    expect(commandNameError(`${'a'.repeat(70)}.md`)).toBe('tooLong')
  })

  test('trims before validating, like agentCommandName does', () => {
    expect(commandNameError('  review  ')).toBeNull()
  })
})

describe('serializeCommandMarkdown', () => {
  test('writes only the keys that carry a value, in fixed order', () => {
    const content = serializeCommandMarkdown(
      fields({
        description: 'Deploy the current branch',
        agent: 'fixer',
        model: 'claude-opus',
        allow_shell: true,
        allow_absolute_files: true,
      }),
      'Ship it.',
    )
    expect(content).toBe(
      [
        '---',
        'description: "Deploy the current branch"',
        'agent: "fixer"',
        'model: "claude-opus"',
        'allow_shell: true',
        'allow_absolute_files: true',
        '---',
        'Ship it.',
      ].join('\n'),
    )
  })

  test('an empty field never reaches the frontmatter', () => {
    const content = serializeCommandMarkdown(fields({ description: 'x' }), 'body')
    expect(content).toBe('---\ndescription: "x"\n---\nbody')
    for (const key of COMMAND_FRONTMATTER_KEYS) {
      if (key !== 'description') expect(content).not.toContain(`${key}:`)
    }
  })

  test('"inherit" (null) OMITS allow_absolute_files; false writes it', () => {
    expect(serializeCommandMarkdown(fields({ allow_absolute_files: null }), 'b')).not.toContain(
      'allow_absolute_files',
    )
    expect(serializeCommandMarkdown(fields({ allow_absolute_files: false }), 'b')).toContain(
      'allow_absolute_files: false',
    )
    expect(serializeCommandMarkdown(fields({ allow_shell: false }), 'b')).not.toContain(
      'allow_shell',
    )
  })

  test('wraps values in quotes WITHOUT escaping — Go only strips one pair', () => {
    const content = serializeCommandMarkdown(fields({ description: 'say "hi" \\ go' }), 'b')
    expect(content).toContain('description: "say "hi" \\ go"')
  })

  test('a field value never leaks a fake frontmatter line', () => {
    const content = serializeCommandMarkdown(fields({ description: 'multi\nline' }), 'b')
    expect(content).toBe('---\ndescription: "multi line"\n---\nb')
  })

  test('the body is kept verbatim after the closing fence', () => {
    const body = 'line 1\n\n  indented\n$ARGUMENTS\n'
    expect(serializeCommandMarkdown(fields({ description: 'd' }), body)).toBe(
      `---\ndescription: "d"\n---\n${body}`,
    )
  })
})

describe('parseCommandMarkdown', () => {
  test('reads every known key and the body', () => {
    const parsed = parseCommandMarkdown(
      [
        '---',
        'description: "Deploy the current branch"',
        "agent: 'fixer'",
        'model: claude-opus',
        'allow_shell: yes',
        'allow_absolute_files: false',
        '---',
        'Ship $1 to @src/main.ts',
      ].join('\n'),
    )
    expect(parsed.hasFrontmatter).toBe(true)
    expect(parsed.fields).toEqual({
      description: 'Deploy the current branch',
      agent: 'fixer',
      model: 'claude-opus',
      allow_shell: true,
      allow_absolute_files: false,
    })
    expect(parsed.body).toBe('Ship $1 to @src/main.ts')
  })

  test('skips comments, blank lines, keyless lines and unknown keys', () => {
    const parsed = parseCommandMarkdown(
      ['---', '# a comment', '', 'nocolon', 'harness: true', 'description: kept', '---', 'b'].join(
        '\n',
      ),
    )
    expect(parsed.hasFrontmatter).toBe(true)
    expect(parsed.fields.description).toBe('kept')
    expect(parsed.fields.agent).toBe('')
  })

  test('the LAST occurrence of a key wins (Go parser parity)', () => {
    const parsed = parseCommandMarkdown('---\ndescription: one\ndescription: two\n---\nbody')
    expect(parsed.fields.description).toBe('two')
  })

  test('escape sequences stay literal \u2014 Go unquote does not unescape', () => {
    const bs = '\\'
    const quoted = ['---', `description: "say ${bs}${bs}\"hi"`, '---', 'b'].join('\n')
    expect(parseCommandMarkdown(quoted).fields.description).toBe(`say ${bs}${bs}"hi`)
    const trailing = ['---', `description: "C:${bs}${bs}"`, '---', 'b'].join('\n')
    expect(parseCommandMarkdown(trailing).fields.description).toBe(`C:${bs}${bs}`)
  })

  test('an unopened or unclosed fence means no frontmatter at all', () => {
    const noFence = parseCommandMarkdown('just a body')
    expect(noFence.hasFrontmatter).toBe(false)
    expect(noFence.body).toBe('just a body')

    const unclosed = parseCommandMarkdown('---\ndescription: broken\nno closing fence')
    expect(unclosed.hasFrontmatter).toBe(false)
    expect(unclosed.body).toBe('---\ndescription: broken\nno closing fence')
  })

  test('fence parity with the Go regex: a one-line block does not match, an empty one does', () => {
    const oneLine = parseCommandMarkdown('---\n---\nbody only')
    expect(oneLine.hasFrontmatter).toBe(false)
    expect(oneLine.body).toBe('---\n---\nbody only')

    const emptyBlock = parseCommandMarkdown('---\n\n---\nbody only')
    expect(emptyBlock.hasFrontmatter).toBe(true)
    expect(emptyBlock.fields).toEqual(emptyCommandFields())
    expect(emptyBlock.body).toBe('body only')
  })

  test('CRLF line endings parse like LF', () => {
    const parsed = parseCommandMarkdown('---\r\ndescription: win\r\n---\r\nbody\r\n')
    expect(parsed.hasFrontmatter).toBe(true)
    expect(parsed.fields.description).toBe('win')
    expect(parsed.body).toBe('body')
  })

  test('an unparseable boolean keeps the default (the server is the validator)', () => {
    const parsed = parseCommandMarkdown('---\ndescription: d\nallow_shell: quizas\n---\nb')
    expect(parsed.fields.allow_shell).toBe(false)
  })
})

describe('round-trip (serialize → parse)', () => {
  test('every field survives byte for byte in meaning', () => {
    const original = fields({
      description: 'Deploy "the" thing',
      agent: 'coder',
      model: 'gpt-5',
      allow_shell: true,
      allow_absolute_files: false,
    })
    const parsed = parseCommandMarkdown(serializeCommandMarkdown(original, 'body text'))
    expect(parsed.fields).toEqual(original)
    expect(parsed.body).toBe('body text')
  })

  test('inherit stays inherit through the round-trip', () => {
    const parsed = parseCommandMarkdown(
      serializeCommandMarkdown(fields({ allow_absolute_files: null }), 'b'),
    )
    expect(parsed.fields.allow_absolute_files).toBe(null)
  })

  test('editing a real file preserves untouched keys and rewrites changed ones', () => {
    const onDisk = '---\ndescription: "old"\nmodel: "m1"\n---\n\nDo things\n'
    const parsed = parseCommandMarkdown(onDisk)
    const next = fields({ ...parsed.fields, description: 'new' })
    const saved = serializeCommandMarkdown(next, parsed.body)
    expect(saved).toBe('---\ndescription: "new"\nmodel: "m1"\n---\nDo things')
  })
})
