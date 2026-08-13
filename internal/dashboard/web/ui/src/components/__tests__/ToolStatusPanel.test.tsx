import { render, screen } from '@testing-library/react'
import { ToolStatusPanel } from '../ToolStatusPanel'

test('a skipped tool states its reason in text, not only in color', () => {
  render(<ToolStatusPanel tools={[
    { name: 'gosec', skipped: false },
    { name: 'clippy', skipped: true, error: 'install failed' },
  ]} />)

  expect(screen.getByText('ran')).toBeInTheDocument()
  expect(screen.getByText(/skipped — install failed/)).toBeInTheDocument()
})

test('the pipeline diagram describes which tools ran for a screen reader', () => {
  render(<ToolStatusPanel tools={[
    { name: 'gosec', skipped: false },
    { name: 'clippy', skipped: true, error: 'install failed' },
  ]} />)

  expect(screen.getByRole('img', { name: /gosec ran, clippy skipped/ })).toBeInTheDocument()
})

test('a branch with no runs renders a plain message', () => {
  render(<ToolStatusPanel tools={null} />)
  expect(screen.getByText(/No runs on this branch yet/)).toBeInTheDocument()
})

test('a tool outside the known five still appears in the diagram', () => {
  render(<ToolStatusPanel tools={[
    { name: 'gosec', skipped: false },
    { name: 'eslint', skipped: false },
  ]} />)

  expect(screen.getByRole('img', { name: /gosec ran.*eslint ran/ })).toBeInTheDocument()
})
