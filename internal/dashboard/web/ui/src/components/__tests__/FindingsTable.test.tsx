import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FindingsTable } from '../FindingsTable'
import { current, finding } from '../../test/fixtures'

const run = current().run

test('a row expands to its explanation and collapses again', async () => {
  render(<FindingsTable run={run} findings={[finding()]} filter={null} viewingOlder={false} />)
  const row = screen.getByRole('button', { expanded: false })

  await userEvent.click(row)
  expect(screen.getByRole('button', { expanded: true })).toBeInTheDocument()
  expect(screen.getByText(/leaks secrets in the binary/)).toBeInTheDocument()

  await userEvent.click(row)
  expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument()
})

test('expanding one row leaves the others collapsed', async () => {
  const findings = [
    finding({ file: 'a.go', explanation: 'first explanation' }),
    finding({ file: 'b.go', ruleID: 'G102', explanation: 'second explanation' }),
  ]
  render(<FindingsTable run={run} findings={findings} filter={null} viewingOlder={false} />)

  const rows = screen.getAllByRole('button', { expanded: false })
  expect(rows).toHaveLength(2)

  await userEvent.click(rows[0])
  expect(screen.getAllByRole('button', { expanded: true })).toHaveLength(1)
  expect(screen.getAllByRole('button', { expanded: false })).toHaveLength(1)
})

test('the severity filter hides everything else', () => {
  const findings = [finding({ severity: 'high' }), finding({ severity: 'low', file: 'z.go' })]
  render(<FindingsTable run={run} findings={findings} filter="low" viewingOlder={false} />)

  expect(screen.getByText('z.go:4')).toBeInTheDocument()
  expect(screen.queryByText('a.go:4')).not.toBeInTheDocument()
  expect(screen.getByText(/low only/)).toBeInTheDocument()
})

test('a finding with no explanation says so instead of showing a blank panel', async () => {
  render(<FindingsTable run={run} findings={[finding({ explanation: '' })]} filter={null} viewingOlder={false} />)
  await userEvent.click(screen.getByRole('button'))
  expect(screen.getByText(/analysed without an LLM provider/)).toBeInTheDocument()
})

test('viewing an older run is stated in the heading', () => {
  render(<FindingsTable run={run} findings={[finding()]} filter={null} viewingOlder />)
  expect(screen.getByText(/older run/)).toBeInTheDocument()
})

test('a finding with a fix pattern shows it, labelled, when expanded', async () => {
  render(<FindingsTable run={run} findings={[finding({ fixPattern: 'os.Getenv("API_KEY")' })]} filter={null} viewingOlder={false} />)
  await userEvent.click(screen.getByRole('button'))
  expect(screen.getByText('Suggested fix')).toBeInTheDocument()
  expect(screen.getByText('os.Getenv("API_KEY")')).toBeInTheDocument()
})

test('a finding with no fix pattern shows the explanation but no empty fix section', async () => {
  render(<FindingsTable run={run} findings={[finding({ fixPattern: '' })]} filter={null} viewingOlder={false} />)
  await userEvent.click(screen.getByRole('button'))
  expect(screen.getByText(/leaks secrets in the binary/)).toBeInTheDocument()
  expect(screen.queryByText('Suggested fix')).not.toBeInTheDocument()
})
