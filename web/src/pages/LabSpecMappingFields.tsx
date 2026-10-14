import type { ChangeEvent } from 'react'

/** UI-only model. Not an HTTP response contract. Caller supplies active, permitted Org mappings. */
export interface MappingCandidate {
  id: string
  displayName: string
  providerConnectionId: string
  providerConnectionName?: string
}

export interface MappingReferenceValue {
  imageRef: string
  sizeRef: string
}

interface Props {
  value: MappingReferenceValue
  images: readonly MappingCandidate[]
  sizes: readonly MappingCandidate[]
  onChange: (next: MappingReferenceValue) => void
  disabled?: boolean
  /** Optional Provider scope selected by the parent across all VM roles. */
  providerConnectionId?: string
}

function label(item: MappingCandidate) {
  return item.providerConnectionName
    ? `${item.displayName} · ${item.providerConnectionName}`
    : item.displayName
}

/**
 * Presentational component only. No API call, persistence, or Provider mutation.
 * Integrate into LabSpecEditor only after the server catalog contract is agreed.
 */
export function LabSpecMappingFields({
  value,
  images,
  sizes,
  onChange,
  disabled = false,
  providerConnectionId,
}: Props) {
  const allowedImages = providerConnectionId
    ? images.filter((item) => item.providerConnectionId === providerConnectionId)
    : images
  const image = allowedImages.find((item) => item.id === value.imageRef)
  const previousSize = sizes.find((item) => item.id === value.sizeRef)
  const allowedSizes = image
    ? sizes.filter((item) => item.providerConnectionId === image.providerConnectionId)
    : []
  const missingImage = Boolean(value.imageRef && !image)
  const missingSize = Boolean(
    value.sizeRef && !allowedSizes.some((item) => item.id === value.sizeRef),
  )

  function chooseImage(event: ChangeEvent<HTMLSelectElement>) {
    const imageRef = event.target.value
    const nextImage = allowedImages.find((item) => item.id === imageRef)
    const keepSize = Boolean(
      nextImage && previousSize &&
      nextImage.providerConnectionId === previousSize.providerConnectionId,
    )
    onChange({ imageRef, sizeRef: keepSize ? value.sizeRef : '' })
  }

  function chooseSize(event: ChangeEvent<HTMLSelectElement>) {
    const sizeRef = event.target.value
    if (sizeRef && !allowedSizes.some((item) => item.id === sizeRef)) return
    onChange({ ...value, sizeRef })
  }

  return (
    <>
      <div className="form-grid">
        <label className="field">
          <span>Image 참조</span>
          <select
            value={value.imageRef}
            disabled={disabled || allowedImages.length === 0}
            onChange={chooseImage}
          >
            <option value="">Image를 선택해 주세요</option>
            {missingImage && (
              <option value={value.imageRef} disabled>
                현재 Image 참조 (후보에 없음)
              </option>
            )}
            {allowedImages.map((item) => (
              <option key={item.id} value={item.id}>{label(item)}</option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Size 참조</span>
          <select
            value={value.sizeRef}
            disabled={disabled || !image || allowedSizes.length === 0}
            onChange={chooseSize}
          >
            <option value="">Size를 선택해 주세요</option>
            {missingSize && (
              <option value={value.sizeRef} disabled>
                현재 Size 참조 (후보에 없음)
              </option>
            )}
            {allowedSizes.map((item) => (
              <option key={item.id} value={item.id}>{label(item)}</option>
            ))}
          </select>
        </label>
      </div>
      {allowedImages.length === 0 && (
        <p className="muted" role="status">
          선택 가능한 Image Mapping이 없습니다. 기관 설정의 허용 후보를 확인해 주세요.
        </p>
      )}
      {image && allowedSizes.length === 0 && (
        <p className="muted" role="status">
          선택한 Image와 같은 Provider 연결의 Size Mapping이 없습니다.
        </p>
      )}
      {(missingImage || missingSize) && (
        <p className="form-error" role="alert">
          기존 Image/Size 참조가 현재 선택 가능한 후보와 일치하지 않습니다.
          값을 확인한 뒤 저장해 주세요.
        </p>
      )}
    </>
  )
}
