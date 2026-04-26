import { describe, expect, test } from 'bun:test'
import {
  isDiffStatLine,
  isFileDiffRow,
  parseBlocks,
  parseDiffStat,
  parseFileDiffRow,
  parseInlineMarkdown,
  parseMarkdownTable,
} from './markdown'

describe('parseBlocks', () => {
  test('parsea texto plano', () => {
    const blocks = parseBlocks('Hola mundo')
    expect(blocks).toHaveLength(1)
    expect(blocks[0].type).toBe('text')
    expect(blocks[0].content).toBe('Hola mundo')
  })

  test('parsea bloques de código', () => {
    const input = '```typescript\nconst x = 1\n```'
    const blocks = parseBlocks(input)
    expect(blocks).toHaveLength(1)
    expect(blocks[0].type).toBe('code')
    expect(blocks[0].content).toBe('const x = 1')
    expect(blocks[0].label).toBe('typescript')
  })

  test('mezcla texto y bloques de código', () => {
    const input = 'Texto\n```js\ncode\n```\nMás texto'
    const blocks = parseBlocks(input)
    expect(blocks).toHaveLength(3)
    expect(blocks[0].type).toBe('text')
    expect(blocks[0].content).toBe('Texto')
    expect(blocks[1].type).toBe('code')
    expect(blocks[1].content).toBe('code')
    expect(blocks[1].label).toBe('js')
    expect(blocks[2].type).toBe('text')
    expect(blocks[2].content).toBe('Más texto')
  })

  test('parsea bloques de tool', () => {
    const input = 'Shell ls -la\nBash echo hola\nTool read_file /tmp/test.txt'
    const blocks = parseBlocks(input)
    expect(blocks).toHaveLength(3)
    expect(blocks[0].type).toBe('tool')
    expect(blocks[0].content).toBe('ls -la')
    expect(blocks[0].label).toBe('Shell')
    expect(blocks[1].type).toBe('tool')
    expect(blocks[1].content).toBe('echo hola')
    expect(blocks[1].label).toBe('Bash')
  })

  test('ignora código sin cerrar al final', () => {
    const input = '```js\ncódigo sin cerrar'
    const blocks = parseBlocks(input)
    expect(blocks).toHaveLength(1)
    expect(blocks[0].type).toBe('code')
    expect(blocks[0].content).toBe('código sin cerrar')
  })

  test('no genera bloques de texto vacíos', () => {
    const blocks = parseBlocks('')
    expect(blocks).toHaveLength(0)
  })

  test('maneja código con backticks escapados', () => {
    const input = '```\n`inline code`\n```'
    const blocks = parseBlocks(input)
    expect(blocks).toHaveLength(1)
    expect(blocks[0].type).toBe('code')
    expect(blocks[0].content).toBe('`inline code`')
  })
})

describe('parseInlineMarkdown', () => {
  test('texto plano sin formato', () => {
    const result = parseInlineMarkdown('Hola mundo')
    expect(result).toHaveLength(1)
    expect(result[0].text).toBe('Hola mundo')
    expect(result[0].token).toBeUndefined()
  })

  test('código inline', () => {
    const result = parseInlineMarkdown('Usa `codigo` aquí')
    expect(result).toHaveLength(3)
    expect(result[0].text).toBe('Usa ')
    expect(result[1].text).toBe('codigo')
    expect(result[1].token?.type).toBe('code')
    expect(result[2].text).toBe(' aquí')
  })

  test('negrita', () => {
    const result = parseInlineMarkdown('Hola **mundo**')
    expect(result).toHaveLength(2)
    expect(result[1].text).toBe('mundo')
    expect(result[1].token?.type).toBe('bold')
  })

  test('cursiva', () => {
    const result = parseInlineMarkdown('Hola *mundo*')
    expect(result).toHaveLength(2)
    expect(result[1].text).toBe('mundo')
    expect(result[1].token?.type).toBe('italic')
  })

  test('enlaces', () => {
    const result = parseInlineMarkdown('Ve a [Google](https://google.com)')
    expect(result).toHaveLength(2)
    expect(result[1].text).toBe('Google')
    expect(result[1].token?.type).toBe('link')
    expect(result[1].token?.href).toBe('https://google.com')
  })

  test('mezcla múltiples formatos', () => {
    const result = parseInlineMarkdown('`código` y **negrita**')
    expect(result).toHaveLength(3)
    expect(result[0].token?.type).toBe('code')
    expect(result[1].text).toBe(' y ')
    expect(result[2].token?.type).toBe('bold')
  })
})

describe('parseMarkdownTable', () => {
  test('parsea tabla básica', () => {
    const lines = ['| Header 1 | Header 2 |', '| --- | --- |', '| Cell 1 | Cell 2 |']
    const result = parseMarkdownTable(lines)
    expect(result).not.toBeNull()
    expect(result?.headers).toEqual(['Header 1', 'Header 2'])
    expect(result?.rows).toHaveLength(1)
    expect(result?.rows[0]).toEqual(['Cell 1', 'Cell 2'])
    expect(result?.lineCount).toBe(3)
  })

  test('retorna null si no es tabla', () => {
    const result = parseMarkdownTable(['Solo texto'])
    expect(result).toBeNull()
  })

  test('retorna null si no hay línea separadora', () => {
    const result = parseMarkdownTable(['| A | B |'])
    expect(result).toBeNull()
  })

  test('parsea tabla con alineaciones', () => {
    const lines = ['| L | C | R |', '| :--- | :---: | ---: |', '| a | b | c |']
    const result = parseMarkdownTable(lines)
    expect(result?.alignments).toEqual(['left', 'center', 'right'])
  })

  test('tabla con múltiples filas', () => {
    const lines = ['| A | B |', '|---|---|', '| 1 | 2 |', '| 3 | 4 |']
    const result = parseMarkdownTable(lines)
    expect(result?.rows).toHaveLength(2)
    expect(result?.rows[1]).toEqual(['3', '4'])
  })
})

describe('parseDiffStat', () => {
  test('parsea diff stat con orden "changed files"', () => {
    const result = parseDiffStat('3 changed files, +50 -10')
    expect(result).not.toBeNull()
    expect(result?.files).toBe('3')
    expect(result?.added).toBe('+50')
    expect(result?.removed).toBe('-10')
  })

  test('parsea diff stat con "changed file" singular', () => {
    const result = parseDiffStat('1 changed file, +1 -0')
    expect(result).not.toBeNull()
    expect(result?.files).toBe('1')
    expect(result?.added).toBe('+1')
    expect(result?.removed).toBe('-0')
  })

  test('retorna null si no coincide', () => {
    const result = parseDiffStat('solo texto')
    expect(result).toBeNull()
  })
})

describe('parseFileDiffRow', () => {
  test('parsea fila de diff file', () => {
    const result = parseFileDiffRow('src/index.ts +10 -5')
    expect(result).not.toBeNull()
    expect(result?.filename).toBe('src/index.ts')
    expect(result?.added).toBe('+10')
    expect(result?.removed).toBe('-5')
  })

  test('retorna null si no coincide', () => {
    const result = parseFileDiffRow('solo texto')
    expect(result).toBeNull()
  })
})

describe('isDiffStatLine / isFileDiffRow', () => {
  test('detecta diff stat line', () => {
    expect(isDiffStatLine('3 changed files, +50 -10')).toBe(true)
    expect(isDiffStatLine('1 changed file, +1 -1')).toBe(true)
    expect(isDiffStatLine('Hola mundo')).toBe(false)
  })

  test('detecta file diff row', () => {
    expect(isFileDiffRow('src/index.ts +10 -5')).toBe(true)
    expect(isFileDiffRow('solo texto')).toBe(false)
  })
})
