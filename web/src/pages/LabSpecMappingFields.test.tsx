import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { LabSpecMappingFields, type MappingCandidate, type MappingReferenceValue } from './LabSpecMappingFields'

const images: MappingCandidate[] = [
  { id: 'image-a', displayName: 'Ubuntu', providerConnectionId: 'provider-a', providerConnectionName: 'A' },
  { id: 'image-b', displayName: 'Rocky', providerConnectionId: 'provider-b', providerConnectionName: 'B' },
]
const sizes: MappingCandidate[] = [
  { id: 'size-a', displayName: 'Small', providerConnectionId: 'provider-a', providerConnectionName: 'A' },
  { id: 'size-b', displayName: 'Medium', providerConnectionId: 'provider-b', providerConnectionName: 'B' },
]

function Harness({
  initial = { imageRef: '', sizeRef: '' },
  options = images,
  flavors = sizes,
  providerConnectionId,
}: {
  initial?: MappingReferenceValue
  options?: readonly MappingCandidate[]
  flavors?: readonly MappingCandidate[]
  providerConnectionId?: string
}) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <LabSpecMappingFields
        value={value}
        images={options}
        sizes={flavors}
        onChange={setValue}
        providerConnectionId={providerConnectionId}
      />
      <output data-testid="selection">{`${value.imageRef}/${value.sizeRef}`}</output>
    </>
  )
}

describe('LabSpec Mapping Picker: mock UI-only', () => {
  it('Image 전에는 Size를 비활성화하고 허용된 후보를 보여준다', () => {
    render(<Harness />)
    expect(screen.getByRole('combobox', { name: 'Image 참조' })).toBeEnabled()
    expect(screen.getByRole('combobox', { name: 'Size 참조' })).toBeDisabled()
    expect(screen.getByRole('option', { name: 'Ubuntu · A' })).toBeInTheDocument()
  })

  it('Image의 Provider와 같은 Size만 선택하고 Provider를 바꾸면 기존 Size를 초기화한다', () => {
    render(<Harness />)
    fireEvent.change(screen.getByRole('combobox', { name: 'Image 참조' }), {
      target: { value: 'image-a' },
    })
    expect(screen.getByRole('option', { name: 'Small · A' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Medium · B' })).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', { name: 'Size 참조' }), {
      target: { value: 'size-a' },
    })
    expect(screen.getByTestId('selection')).toHaveTextContent('image-a/size-a')
    fireEvent.change(screen.getByRole('combobox', { name: 'Image 참조' }), {
      target: { value: 'image-b' },
    })
    expect(screen.getByTestId('selection')).toHaveTextContent('image-b/')
    expect(screen.getByRole('option', { name: 'Medium · B' })).toBeInTheDocument()
  })

  it('선택된 LabSpec-level Provider 연결과 다른 Image를 허용하지 않는다', () => {
    render(<Harness providerConnectionId="provider-a" />)
    expect(screen.getByRole('option', { name: 'Ubuntu · A' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Rocky · B' })).not.toBeInTheDocument()
  })

  it('후보에서 빠진 기존 ID를 조용히 지우지 않고 경고한다', () => {
    render(<Harness initial={{ imageRef: 'old-image', sizeRef: 'old-size' }} />)
    expect(screen.getByTestId('selection')).toHaveTextContent('old-image/old-size')
    expect(screen.getByRole('option', { name: '현재 Image 참조 (후보에 없음)' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '현재 Size 참조 (후보에 없음)' })).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('현재 선택 가능한 후보와 일치하지 않습니다')
  })

  it('빈 후보/미할당 Size를 표시하고 선택을 비활성화한다', () => {
    const { rerender } = render(<Harness options={[]} flavors={[]} />)
    expect(screen.getByText(/선택 가능한 Image Mapping이 없습니다/)).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Image 참조' })).toBeDisabled()
    rerender(<Harness options={images} flavors={[]} />)
    fireEvent.change(screen.getByRole('combobox', { name: 'Image 참조' }), {
      target: { value: 'image-a' },
    })
    expect(screen.getByText(/같은 Provider 연결의 Size Mapping이 없습니다/)).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Size 참조' })).toBeDisabled()
  })

  it('read-only 모드에서는 입력 불가, 변경 callback 호출도 없다', () => {
    const onChange = vi.fn()
    render(<LabSpecMappingFields
      value={{ imageRef: 'image-a', sizeRef: 'size-a' }}
      images={images}
      sizes={sizes}
      onChange={onChange}
      disabled
    />)
    expect(screen.getByRole('combobox', { name: 'Image 참조' })).toBeDisabled()
    expect(screen.getByRole('combobox', { name: 'Size 참조' })).toBeDisabled()
    expect(onChange).not.toHaveBeenCalled()
  })
})
