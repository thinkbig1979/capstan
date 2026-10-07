import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { NumericField } from '../NumericField'
import { parseWholeNumber } from '../parseWholeNumber'

describe('parseWholeNumber', () => {
  it.each([
    ['0', 0],
    ['15', 15],
    ['007', 7],
    ['', null],
    [' ', null],
    ['-5', null],
    ['1.5', null],
    ['1e3', null],
    ['abc', null],
  ])('%j -> %j', (raw, want) => {
    expect(parseWholeNumber(raw)).toBe(want)
  })
})

describe('NumericField', () => {
  it('reports null for an emptied box, never 0', () => {
    const onValueChange = vi.fn()
    render(<NumericField aria-label="n" value={5} onValueChange={onValueChange} />)

    fireEvent.change(screen.getByLabelText('n'), { target: { value: '' } })

    expect(onValueChange).toHaveBeenCalledTimes(1)
    expect(onValueChange).toHaveBeenCalledWith(null)
  })

  it('reports a typed 0 as 0', () => {
    const onValueChange = vi.fn()
    render(<NumericField aria-label="n" value={5} onValueChange={onValueChange} />)

    fireEvent.change(screen.getByLabelText('n'), { target: { value: '0' } })

    expect(onValueChange).toHaveBeenCalledWith(0)
  })

  it('shows an empty, invalid, described box for null and nothing extra for a value', () => {
    const { rerender } = render(<NumericField aria-label="n" value={null} onValueChange={() => {}} />)
    const input = screen.getByLabelText('n')

    expect(input).toHaveValue(null)
    expect(input).toBeInvalid()
    expect(input).toHaveAccessibleDescription('Enter a number')

    rerender(<NumericField aria-label="n" value={0} onValueChange={() => {}} />)
    expect(screen.getByLabelText('n')).toHaveValue(0)
    expect(screen.getByLabelText('n')).not.toBeInvalid()
    expect(screen.queryByText('Enter a number')).not.toBeInTheDocument()
  })
})
