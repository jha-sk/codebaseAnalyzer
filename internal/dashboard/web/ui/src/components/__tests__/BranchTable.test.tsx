import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BranchTable, branchHealth } from '../BranchTable'
import { branch, counts } from '../../test/fixtures'

test('selecting a row reports the branch to the parent', async () => {
  const onSelect = vi.fn()
  render(<BranchTable branches={[branch(), branch({ name: 'feature' })]} selected="main" onSelect={onSelect} />)

  await userEvent.click(screen.getByText('feature'))
  expect(onSelect).toHaveBeenCalledWith('feature')
})

test('the selected branch is marked, and severity composition is described in text', () => {
  render(<BranchTable branches={[branch({ counts: counts(1, 0, 0, 2) })]} selected="main" onSelect={() => {}} />)

  expect(screen.getByRole('row', { selected: true })).toHaveTextContent('main')
  // Composition must not be conveyed by color alone.
  expect(screen.getByRole('img', { name: '1 critical, 2 low' })).toBeInTheDocument()
})

test('branchHealth weights a critical above a low and clamps at zero', () => {
  expect(branchHealth(counts(0, 0, 0, 0))).toBe(100)
  expect(branchHealth(counts(1, 0, 0, 0))).toBeLessThan(branchHealth(counts(0, 0, 0, 1)))
  expect(branchHealth(counts(50, 50, 50, 50))).toBe(0)
})
