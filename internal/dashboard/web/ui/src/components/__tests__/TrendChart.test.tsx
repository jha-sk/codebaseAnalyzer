import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TrendChart } from '../TrendChart'
import { counts, point } from '../../test/fixtures'

const history = [
  point({ commit_sha: 'aaaaaaa111', counts: counts(1, 2, 3, 4) }),
  point({ commit_sha: 'bbbbbbb222', counts: counts(0, 5, 1, 2) }),
]

test('a legend entry toggles its series off and back on', async () => {
  render(<TrendChart history={history} />)
  const critical = screen.getByRole('button', { name: 'critical' })

  expect(critical).toHaveAttribute('aria-pressed', 'true')
  await userEvent.click(critical)
  expect(critical).toHaveAttribute('aria-pressed', 'false')
  await userEvent.click(critical)
  expect(critical).toHaveAttribute('aria-pressed', 'true')
})

test('hiding every series restores them all rather than blanking the chart', async () => {
  render(<TrendChart history={history} />)
  for (const name of ['critical', 'high', 'medium', 'low']) {
    await userEvent.click(screen.getByRole('button', { name }))
  }
  for (const name of ['critical', 'high', 'medium', 'low']) {
    expect(screen.getByRole('button', { name })).toHaveAttribute('aria-pressed', 'true')
  }
})

test('the table view exposes every value the chart encodes', async () => {
  render(<TrendChart history={history} />)
  await userEvent.click(screen.getByRole('button', { name: 'Table' }))

  const rows = screen.getAllByRole('row')
  expect(rows).toHaveLength(3) // header + two runs

  // Header order pins which column is which, so a swapped pair fails below.
  const headers = within(rows[0]).getAllByRole('columnheader').map(h => h.textContent)
  expect(headers).toEqual(['Commit', 'When', 'critical', 'high', 'medium', 'low'])

  const cells = (row: HTMLElement) =>
    within(row).getAllByRole('cell').slice(2).map(c => c.textContent)
  expect(cells(rows[1])).toEqual(['1', '2', '3', '4'])
  expect(cells(rows[2])).toEqual(['0', '5', '1', '2'])
})

test('a single run explains itself instead of drawing a one-point line', () => {
  render(<TrendChart history={[history[0]]} />)
  expect(screen.getByText(/trend appears from the second run/i)).toBeInTheDocument()
})
