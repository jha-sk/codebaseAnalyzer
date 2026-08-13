import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SeverityCards } from '../SeverityCards'
import { current, point } from '../../test/fixtures'

test('clicking a card sets the filter and clicking the pressed card clears it', async () => {
  const onFilter = vi.fn()
  const { rerender } = render(
    <SeverityCards current={current()} history={[point()]} filter={null} onFilter={onFilter} />,
  )

  await userEvent.click(screen.getByRole('button', { name: /critical findings/i }))
  expect(onFilter).toHaveBeenCalledWith('critical')

  rerender(<SeverityCards current={current()} history={[point()]} filter="critical" onFilter={onFilter} />)
  await userEvent.click(screen.getByRole('button', { name: /critical findings/i }))
  expect(onFilter).toHaveBeenLastCalledWith(null)
})

test('each card states its count and its delta as text', () => {
  render(<SeverityCards current={current()} history={[point()]} filter={null} onFilter={() => {}} />)

  expect(screen.getByRole('button', { name: /1 critical findings/i })).toHaveTextContent('+1 vs previous run')
  expect(screen.getByRole('button', { name: /2 high findings/i })).toHaveTextContent('-1 vs previous run')
})

test('an empty run renders zeroes rather than crashing', () => {
  render(<SeverityCards current={null} history={[]} filter={null} onFilter={() => {}} />)
  expect(screen.getAllByText('0')).toHaveLength(4)
})
