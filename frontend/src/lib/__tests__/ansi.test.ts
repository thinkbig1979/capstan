import { describe, it, expect } from 'vitest'
import { parseAnsi, stripAnsi, hasAnsi, ansiSegmentClassName } from '../ansi'

const ESC = '\x1b'

describe('hasAnsi', () => {
  it('detects an SGR sequence', () => {
    expect(hasAnsi(`${ESC}[31mred${ESC}[0m`)).toBe(true)
  })
  it('detects a non-SGR CSI sequence (erase line)', () => {
    expect(hasAnsi(`progress${ESC}[K`)).toBe(true)
  })
  it('returns false for plain text', () => {
    expect(hasAnsi('just a plain log line')).toBe(false)
  })
})

describe('stripAnsi', () => {
  it('removes color codes', () => {
    expect(stripAnsi(`${ESC}[31mERROR${ESC}[0m: boom`)).toBe('ERROR: boom')
  })
  it('removes cursor/erase sequences', () => {
    expect(stripAnsi(`loading${ESC}[2K${ESC}[1Gdone`)).toBe('loadingdone')
  })
  it('leaves plain text untouched', () => {
    expect(stripAnsi('plain')).toBe('plain')
  })
})

describe('parseAnsi', () => {
  it('returns a single unstyled segment for plain text', () => {
    expect(parseAnsi('hello')).toEqual([{ text: 'hello' }])
  })

  it('parses a foreground color segment', () => {
    const segs = parseAnsi(`${ESC}[31mred text${ESC}[0m`)
    expect(segs).toHaveLength(1)
    expect(segs[0]).toMatchObject({ text: 'red text', fg: 'red' })
  })

  it('splits leading plain text from a colored span', () => {
    const segs = parseAnsi(`plain ${ESC}[32mgreen${ESC}[0m tail`)
    expect(segs.map((s) => s.text)).toEqual(['plain ', 'green', ' tail'])
    expect(segs[0].fg).toBeUndefined()
    expect(segs[1].fg).toBe('green')
    expect(segs[2].fg).toBeUndefined()
  })

  it('accumulates style attributes (bold + color)', () => {
    const segs = parseAnsi(`${ESC}[1m${ESC}[34mbold blue${ESC}[0m`)
    expect(segs[0]).toMatchObject({ text: 'bold blue', fg: 'blue', bold: true })
  })

  it('resets style on ESC[0m', () => {
    const segs = parseAnsi(`${ESC}[31ma${ESC}[0mb`)
    expect(segs[0]).toMatchObject({ text: 'a', fg: 'red' })
    expect(segs[1]).toMatchObject({ text: 'b' })
    expect(segs[1].fg).toBeUndefined()
  })

  it('treats empty params ESC[m as reset', () => {
    const segs = parseAnsi(`${ESC}[31ma${ESC}[mb`)
    expect(segs[1].fg).toBeUndefined()
  })

  it('parses bright foreground colors', () => {
    const segs = parseAnsi(`${ESC}[91mbright red${ESC}[0m`)
    expect(segs[0].fg).toBe('brightRed')
  })

  it('consumes 256-color sequences without leaking params as styles', () => {
    const segs = parseAnsi(`${ESC}[38;5;82mtext${ESC}[0m`)
    expect(segs).toHaveLength(1)
    expect(segs[0].text).toBe('text')
    // exact 256 color dropped, but no bogus bold/italic from the 5/82 params
    expect(segs[0].bold).toBeFalsy()
    expect(segs[0].italic).toBeFalsy()
  })

  it('consumes truecolor sequences', () => {
    const segs = parseAnsi(`${ESC}[38;2;255;0;0mtext${ESC}[0m`)
    expect(segs[0].text).toBe('text')
    expect(segs[0].bold).toBeFalsy()
  })

  it('drops non-SGR CSI sequences without breaking text', () => {
    const segs = parseAnsi(`abc${ESC}[Kdef`)
    expect(segs.map((s) => s.text).join('')).toBe('abcdef')
  })

  it('handles default-foreground code 39', () => {
    const segs = parseAnsi(`${ESC}[31ma${ESC}[39mb`)
    expect(segs[0].fg).toBe('red')
    expect(segs[1].fg).toBeUndefined()
  })
})

describe('ansiSegmentClassName', () => {
  it('maps red to a theme-aware class pair', () => {
    expect(ansiSegmentClassName({ fg: 'red' })).toContain('text-red-600')
    expect(ansiSegmentClassName({ fg: 'red' })).toContain('dark:text-red-400')
  })
  it('returns empty string for default white/no style', () => {
    expect(ansiSegmentClassName({})).toBe('')
    expect(ansiSegmentClassName({ fg: 'white' })).toBe('')
  })
  it('combines bold, italic and underline', () => {
    const cls = ansiSegmentClassName({ bold: true, italic: true, underline: true })
    expect(cls).toContain('font-bold')
    expect(cls).toContain('italic')
    expect(cls).toContain('underline')
  })
  it('maps dim to reduced opacity', () => {
    expect(ansiSegmentClassName({ dim: true })).toContain('opacity-70')
  })
})
