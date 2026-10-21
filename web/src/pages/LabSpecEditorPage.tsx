import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useBlocker, useLocation, useNavigate, useParams } from 'react-router-dom'

import type { LabSpecMappingOptionsList, LabSpecWrite, VmRoleSpec } from '../shared/api/contracts'
import { LabSpecMappingFields, type MappingCandidate } from './LabSpecMappingFields'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { LoginRedirect } from '../shared/ui/LoginRedirect'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

const emptyLabSpec: LabSpecWrite = {
  name: '',
  description: '',
  vms: [
    {
      role: '',
      name: '',
      imageRef: '',
      sizeRef: '',
      count: 1,
    },
  ],
  workspaceVm: {
    role: '',
    instanceIndex: 0,
  },
  internetOutbound: true,
  startupScript: '',
}

function providerForInitial(
  initial: LabSpecWrite,
  candidates: LabSpecMappingOptionsList,
) {
  const refs = initial.vms.flatMap((vm) => [vm.imageRef, vm.sizeRef]).filter(Boolean)
  const providers = new Set(
    refs.map((ref) =>
      candidates.items.find(
        (group) =>
          group.images.some((image) => image.id === ref) ||
          group.flavors.some((flavor) => flavor.id === ref),
      )?.providerConnectionId,
    ),
  )
  return providers.size === 1 && !providers.has(undefined)
    ? ([...providers][0] ?? '')
    : ''
}

interface LabSpecDraftBackup {
  labSpecId: string
  etag?: string
  form: LabSpecWrite
}

interface LabSpecFormProps {
  mappingOptions: LabSpecMappingOptionsList
  mappingError?: 'forbidden' | 'unavailable'
  initial: LabSpecWrite
  readOnly: boolean
  saving: boolean
  saveError: unknown
  requireEtag: boolean
  forbiddenMessage: string
  onSubmit: (input: LabSpecWrite) => void
  onReload: () => void
  onDraftChange: (draft: LabSpecWrite) => void
  retainedDraft: LabSpecWrite | null
  onDiscardRetainedDraft: () => void
}

function LabSpecForm({
  initial,
  mappingOptions,
  mappingError,
  readOnly,
  saving,
  saveError,
  requireEtag,
  forbiddenMessage,
  onSubmit,
  onReload,
  onDraftChange,
  retainedDraft,
  onDiscardRetainedDraft,
}: LabSpecFormProps) {
  const [form, setForm] = useState<LabSpecWrite>(initial)
  // Preserve an older ETag draft until an explicit Restore/Discard decision.
  const draftDecisionPending = Boolean(retainedDraft)
  const initialFingerprint = JSON.stringify(initial)
  // Keep the latest user changes above this ETag-keyed form. A background
  // refetch or explicit 412 reload must not silently discard the draft.
  useEffect(() => {
    if (JSON.stringify(form) !== initialFingerprint) onDraftChange(form)
  }, [form, initialFingerprint, onDraftChange])
  const [validationError, setValidationError] = useState<string | null>(null)
  const [chosenProviderConnectionId, setProviderConnectionId] = useState(
    () => providerForInitial(initial, mappingOptions),
  )
  // 초기 500 → 후보 재조회 성공 시 Provider만 계산해 복원한다.
  // 사용자가 선택한 Provider는 고정하고, 폼의 미저장 draft는 재마운트하지 않는다.
  const providerConnectionId =
    chosenProviderConnectionId || providerForInitial(form, mappingOptions)

  const selectedGroup = mappingOptions.items.find(
    (group) => group.providerConnectionId === providerConnectionId,
  )
  const images: MappingCandidate[] = mappingOptions.items.flatMap((group) =>
    group.images.map((image) => ({
      id: image.id,
      displayName: image.displayName,
      providerConnectionId: group.providerConnectionId,
    })),
  )
  const sizes: MappingCandidate[] = mappingOptions.items.flatMap((group) =>
    group.flavors.map((flavor) => ({
      id: flavor.id,
      displayName: `${flavor.displayName} · ${flavor.vcpus} vCPU / ${flavor.ramMiB} MiB / ${flavor.diskGiB} GiB`,
      providerConnectionId: group.providerConnectionId,
    })),
  )

  function updateVm(index: number, patch: Partial<VmRoleSpec>) {
    setForm((current) => ({
      ...current,
      vms: current.vms.map((vm, vmIndex) =>
        vmIndex === index ? { ...vm, ...patch } : vm,
      ),
    }))
  }

  function addVm() {
    setForm((current) => ({
      ...current,
      vms: [
        ...current.vms,
        {
          role: '',
          name: '',
          imageRef: '',
          sizeRef: '',
          count: 1,
        },
      ],
    }))
  }

  function removeVm(index: number) {
    setForm((current) => {
      if (current.vms.length === 1) {
        return current
      }

      return {
        ...current,
        vms: current.vms.filter((_, vmIndex) => vmIndex !== index),
      }
    })
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setValidationError(null)

    const normalized: LabSpecWrite = {
      ...form,
      name: form.name.trim(),
      description: form.description?.trim() || undefined,
      startupScript: form.startupScript?.trim() || undefined,
      workspaceVm: {
        ...form.workspaceVm,
        role: form.workspaceVm.role.trim(),
      },
      vms: form.vms.map((vm) => ({
        ...vm,
        role: vm.role.trim(),
        name: vm.name?.trim() || undefined,
        imageRef: vm.imageRef.trim(),
        sizeRef: vm.sizeRef.trim(),
        count: Math.max(1, Number(vm.count) || 1),
      })),
    }

    if (!normalized.name) {
      setValidationError('LabSpec 이름을 입력해 주세요.')
      return
    }

    if (
      normalized.vms.some(
        (vm) => !vm.role || !vm.imageRef || !vm.sizeRef || vm.count < 1,
      )
    ) {
      setValidationError('각 VM의 Role, Image 참조, Size 참조와 Count를 확인해 주세요.')
      return
    }

    if (!selectedGroup) {
      setValidationError('Provider 연결을 먼저 선택해 주세요.')
      return
    }

    if (normalized.vms.some((vm) =>
      !selectedGroup.images.some((image) => image.id === vm.imageRef) ||
      !selectedGroup.flavors.some((flavor) => flavor.id === vm.sizeRef)
    )) {
      setValidationError('모든 VM의 Image/Size를 같은 Provider 연결의 활성 Mapping에서 선택해 주세요.')
      return
    }

    const roles = normalized.vms.map((vm) => vm.role)
    if (new Set(roles).size !== roles.length) {
      setValidationError('VM Role은 LabSpec 안에서 중복될 수 없습니다.')
      return
    }

    const workspaceVm = normalized.vms.find(
      (vm) => vm.role === normalized.workspaceVm.role,
    )

    if (
      !workspaceVm ||
      normalized.workspaceVm.instanceIndex < 0 ||
      normalized.workspaceVm.instanceIndex >= workspaceVm.count
    ) {
      setValidationError('유효한 Workspace VM을 선택해 주세요.')
      return
    }

    onSubmit(normalized)
  }

  const staleError = saveError instanceof HttpError && saveError.status === 412
  const forbiddenError = saveError instanceof HttpError && saveError.status === 403
  const validationServerError =
    saveError instanceof HttpError && (saveError.status === 400 || saveError.status === 422)

  return (
    <form className="labspec-form" onSubmit={handleSubmit}>
      {readOnly && (
        <div className="notice-card notice-warning">
          {mappingError === 'forbidden'
            ? 'Mapping 후보를 조회할 강사 권한이 없습니다. 기존 실습 정의는 읽기 전용으로 확인할 수 있지만 수정·저장할 수 없습니다.'
            : mappingError === 'unavailable'
              ? 'Image/Size Mapping 후보를 불러오지 못했습니다. 기존 실습 정의는 읽기 전용으로 확인할 수 있지만 수정·저장할 수 없습니다.'
              : '다른 강사가 소유한 실습 정의입니다. 현재 계정에서는 내용을 확인만 할 수 있습니다.'}
        </div>
      )}

      {retainedDraft && (
        <div className="notice-card notice-warning" role="status">
          서버에서 갱신된 LabSpec을 표시하고 있습니다. 이전 미저장 수정본은 메모리에 보관했습니다.
          최신 내용을 확인한 뒤 수정본을 복원할 수 있지만, 복원하면 현재 표시된 입력값이 바뀝니다.
          <div className="form-actions">
            <button
              className="secondary-button"
              type="button"
              disabled={saving || readOnly}
              onClick={() => {
                setProviderConnectionId(providerForInitial(retainedDraft, mappingOptions))
                setForm(retainedDraft)
                setValidationError(null)
                onDiscardRetainedDraft()
              }}
            >
              이전 수정본 복원
            </button>
            <button
              className="text-button"
              type="button"
              disabled={saving}
              onClick={onDiscardRetainedDraft}
            >
              이전 수정본 버리기
            </button>
          </div>
        </div>
      )}

      {requireEtag && (
        <div className="notice-card notice-warning">
          최신 ETag를 확인하지 못해 안전한 수정 요청을 보낼 수 없습니다. 다시 불러와 주세요.
          <button className="text-button" type="button" disabled={saving} onClick={onReload}>
            다시 불러오기
          </button>
        </div>
      )}

      <section className="form-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">기본 설정</p>
            <h2>기본 정보</h2>
          </div>
        </div>

        <label className="field">
          <span>이름</span>
          <input
            value={form.name}
            disabled={readOnly || saving || draftDecisionPending}
            onChange={(event) =>
              setForm((current) => ({ ...current, name: event.target.value }))
            }
          />
        </label>

        <label className="field">
          <span>설명</span>
          <textarea
            rows={3}
            value={form.description ?? ''}
            disabled={readOnly || saving || draftDecisionPending}
            onChange={(event) =>
              setForm((current) => ({ ...current, description: event.target.value }))
            }
          />
        </label>
      </section>

      <section className="form-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">환경 구성</p>
            <h2>VM 구성</h2>
            <p className="muted">
              실습에 필요한 VM 역할과 이미지·크기 참조값을 정의합니다.
            </p>
          </div>
          {!readOnly && (
            <button className="secondary-button" type="button" disabled={saving || draftDecisionPending} onClick={addVm}>
              VM 역할 추가
            </button>
          )}
        </div>

        <label className="field">
          <span>Provider 연결</span>
          <select
            value={providerConnectionId}
            disabled={readOnly || saving || draftDecisionPending}
            onChange={(event) => {
              const nextProvider = event.target.value
              if (nextProvider === providerConnectionId) return
              setProviderConnectionId(nextProvider)
              setValidationError(null)
              setForm((current) => ({
                ...current,
                vms: current.vms.map((vm) => ({ ...vm, imageRef: '', sizeRef: '' })),
              }))
            }}
          >
            <option value="">선택해 주세요</option>
            {mappingOptions.items.map((group, index) => (
              <option key={group.providerConnectionId} value={group.providerConnectionId}>
                Provider {index + 1} · {group.providerConnectionId}
              </option>
            ))}
          </select>
        </label>
        <small className="muted">
          Provider 연결을 변경하면 모든 VM의 Image/Size 선택이 초기화됩니다.
        </small>
        {!mappingError && mappingOptions.items.length === 0 && (
          <div className="notice-card notice-warning" role="status">
            현재 Organization에 활성 Image/Flavor Mapping 후보가 없습니다.
            기관의 Mapping 설정을 확인해 주세요.
          </div>
        )}
        {!mappingError && !providerConnectionId && form.vms.some((vm) => vm.imageRef || vm.sizeRef) && (
          <div className="notice-card notice-warning">
            기존 LabSpec의 Mapping이 현재 조회된 후보와 일치하지 않습니다.
            Provider 연결을 다시 선택해 주세요.
          </div>
        )}
        <div className="vm-list">
          {form.vms.map((vm, index) => (
            <article className="vm-row" key={index}>
              <div className="vm-row-heading">
                <strong>VM 역할 {index + 1}</strong>
                {!readOnly && form.vms.length > 1 && (
                  <button
                    className="text-button danger-text"
                    type="button"
                    disabled={saving || draftDecisionPending}
                    onClick={() => removeVm(index)}
                  >
                    제거
                  </button>
                )}
              </div>

              <div className="form-grid">
                <label className="field">
                  <span>Role</span>
                  <input
                    value={vm.role}
                    disabled={readOnly || saving || draftDecisionPending}
                    placeholder="예: control"
                    onChange={(event) => updateVm(index, { role: event.target.value })}
                  />
                </label>
                <label className="field">
                  <span>표시 이름</span>
                  <input
                    value={vm.name ?? ''}
                    disabled={readOnly || saving || draftDecisionPending}
                    placeholder="선택"
                    onChange={(event) => updateVm(index, { name: event.target.value })}
                  />
                </label>
                {mappingError ? (
                  <>
                    <label className="field">
                      <span>Image 참조</span>
                      <input value={vm.imageRef} disabled />
                    </label>
                    <label className="field">
                      <span>Size 참조</span>
                      <input value={vm.sizeRef} disabled />
                    </label>
                  </>
                ) : (
                  <LabSpecMappingFields
                    value={{ imageRef: vm.imageRef, sizeRef: vm.sizeRef }}
                    images={images}
                    sizes={sizes}
                    providerConnectionId={providerConnectionId}
                    disabled={readOnly || saving || draftDecisionPending || !selectedGroup}
                    onChange={(next) => updateVm(index, next)}
                  />
                )}
                <label className="field">
                  <span>Count</span>
                  <input
                    type="number"
                    min={1}
                    value={vm.count}
                    disabled={readOnly || saving || draftDecisionPending}
                    onChange={(event) =>
                      updateVm(index, {
                        count: Math.max(1, event.target.valueAsNumber || 1),
                      })
                    }
                  />
                </label>
              </div>
            </article>
          ))}
        </div>
      </section>

      <section className="form-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">작업 환경</p>
            <h2>Workspace VM</h2>
            <p className="muted">
              파일 편집과 기본 미리보기에 사용할 대표 VM을 지정합니다.
            </p>
          </div>
        </div>

        <label className="field">
          <span>Workspace VM</span>
          <select
            value={`${form.workspaceVm.role}:${form.workspaceVm.instanceIndex}`}
            disabled={readOnly || saving || draftDecisionPending}
            onChange={(event) => {
              const separator = event.target.value.lastIndexOf(':')
              const role = event.target.value.slice(0, separator)
              const instanceIndex = Number(event.target.value.slice(separator + 1))

              setForm((current) => ({
                ...current,
                workspaceVm: { role, instanceIndex },
              }))
            }}
          >
            <option value=":0">선택해 주세요</option>
            {form.vms.flatMap((vm, vmIndex) =>
              Array.from({ length: Math.max(1, vm.count) }, (_, instanceIndex) => (
                <option
                  key={`${vmIndex}:${vm.role}:${instanceIndex}`}
                  value={`${vm.role}:${instanceIndex}`}
                >
                  {vm.role || '(Role 미입력)'} · #{instanceIndex + 1}
                </option>
              )),
            )}
          </select>
        </label>

        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={form.internetOutbound}
            disabled={readOnly || saving || draftDecisionPending}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                internetOutbound: event.target.checked,
              }))
            }
          />
          <span>외부 인터넷 연결 허용</span>
        </label>

        <label className="field">
          <span>Startup Script</span>
          <textarea
            rows={8}
            value={form.startupScript ?? ''}
            disabled={readOnly || saving || draftDecisionPending}
            placeholder="선택 입력"
            onChange={(event) =>
              setForm((current) => ({ ...current, startupScript: event.target.value }))
            }
          />
          <small className="muted">
            비밀번호나 Token 같은 민감정보는 시작 스크립트에 직접 입력하지 마세요.
          </small>
        </label>
      </section>

      {Boolean(validationError || staleError || forbiddenError || validationServerError || saveError) && (
        <div className="form-error" role="alert">
          {validationError ??
            (staleError
              ? '다른 곳에서 LabSpec이 수정되었습니다. 최신 내용을 다시 불러온 뒤 다시 저장해 주세요.'
              : forbiddenError
                ? forbiddenMessage
                : validationServerError
                  ? '저장할 수 없는 값이 있습니다. 입력 내용을 확인해 주세요.'
                  : 'LabSpec을 저장하지 못했습니다. 잠시 후 다시 시도해 주세요.')}
          {staleError && (
            <button className="text-button" type="button" disabled={saving} onClick={onReload}>
              최신 내용 다시 불러오기
            </button>
          )}
        </div>
      )}

      {!readOnly && (
        <div className="form-actions">
          <Link className="secondary-link" to="/lab-specs">
            취소
          </Link>
          <button className="primary-button" type="submit" disabled={saving || requireEtag || Boolean(retainedDraft)}>
            {saving ? '저장 중...' : '실습 정의 저장'}
          </button>
        </div>
      )}
    </form>
  )
}

export function LabSpecEditorPage() {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const { labSpecId } = useParams()
  const isNew = !labSpecId
  const resolvedLabSpecId = labSpecId ?? ''
  // A cached 412 draft is tied to the source LabSpec and its ETag.
  // It is never automatically submitted with a newer server ETag.
  const [draftBackup, setDraftBackup] = useState<LabSpecDraftBackup | null>(null)
  const [dirtyRoute, setDirtyRoute] = useState<string | null>(null)
  const [saveSuccess, setSaveSuccess] = useState<'updated' | null>(null)
  const [createdNoticeDismissed, setCreatedNoticeDismissed] = useState(false)
  const allowSavedNavigationRef = useRef(false)
  const currentRoute = resolvedLabSpecId || 'new-lab-spec'

  // React Router can reuse this page across /new -> /:id. The create-only
  // navigation bypass must not survive entry into the saved detail route.
  useEffect(() => {
    allowSavedNavigationRef.current = false
  }, [currentRoute])

  const meQuery = useQuery({
    queryKey: labbitQueryKeys.me,
    queryFn: () => api.getMe(),
    retry: false,
  })

  const labSpecQuery = useQuery({
    queryKey: labbitQueryKeys.labSpec(resolvedLabSpecId),
    queryFn: () => api.getLabSpec(resolvedLabSpecId),
    enabled: !isNew,
    retry: false,
  })

  const mappingOptionsQuery = useQuery({
    queryKey: labbitQueryKeys.labSpecMappingOptions(
      meQuery.data?.organization.id ?? '',
      meQuery.data?.id ?? '',
    ),
    queryFn: () => api.listLabSpecMappingOptions(),
    enabled: Boolean(
      meQuery.data &&
      (isNew || labSpecQuery.data?.labSpec.ownerUserId === meQuery.data.id),
    ),
    retry: false,
  })

  const captureDraft = useCallback((form: LabSpecWrite) => {
    setSaveSuccess(null)
    setCreatedNoticeDismissed(true)
    setDirtyRoute(currentRoute)
    if (!resolvedLabSpecId) return
    setDraftBackup((previous) => {
      // A background refetch may already have switched the form to a newer
      // ETag. Never overwrite an unconfirmed draft from the old revision.
      if (previous?.labSpecId === resolvedLabSpecId && previous.etag !== labSpecQuery.data?.etag) {
        return previous
      }
      return { labSpecId: resolvedLabSpecId, etag: labSpecQuery.data?.etag, form }
    })
  }, [currentRoute, resolvedLabSpecId, labSpecQuery.data?.etag])

  const saveMutation = useMutation({
    mutationFn: async (input: LabSpecWrite) => {
      if (isNew) {
        return {
          mode: 'create' as const,
          labSpec: await api.createLabSpec(input),
        }
      }

      const etag = labSpecQuery.data?.etag
      if (!etag) {
        throw new Error('LabSpec ETag is required for update')
      }

      return {
        mode: 'update' as const,
        result: await api.updateLabSpec(resolvedLabSpecId, input, etag),
      }
    },
    onSuccess: async (result) => {
      setDraftBackup(null)
      setDirtyRoute(null)
      setSaveSuccess(null)
      await queryClient.invalidateQueries({ queryKey: labbitQueryKeys.labSpecs })

      if (result.mode === 'create') {
        // Only the successful create navigates away automatically.
        // Pass feedback to the detail route because React Router may remount this page.
        setCreatedNoticeDismissed(false)
        allowSavedNavigationRef.current = true
        navigate(`/lab-specs/${encodeURIComponent(result.labSpec.id)}`, {
          replace: true,
          state: { labSpecJustCreatedId: result.labSpec.id },
        })
        return
      }

      setCreatedNoticeDismissed(true)
      setSaveSuccess('updated')
      queryClient.setQueryData(
        labbitQueryKeys.labSpec(resolvedLabSpecId),
        result.result,
      )
    },
    onError: (error) => {
      if (error instanceof HttpError && error.status === 401) {
        queryClient.removeQueries({ queryKey: labbitQueryKeys.me })
      }
    },
  })

  const hasUnsavedWork = dirtyRoute === currentRoute || saveMutation.isPending
  const navigationBlocker = useBlocker(
    ({ nextLocation }) =>
      hasUnsavedWork && !allowSavedNavigationRef.current &&
      nextLocation.pathname !== '/login',
  )

  useEffect(() => {
    if (!hasUnsavedWork) return
    const preventUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', preventUnload)
    return () => window.removeEventListener('beforeunload', preventUnload)
  }, [hasUnsavedWork])

  useEffect(() => {
    if (navigationBlocker.state !== 'blocked') return
    if (saveMutation.isPending) {
      navigationBlocker.reset()
      return
    }
    if (window.confirm('저장되지 않은 LabSpec 변경이 있습니다. 버리고 이동할까요?')) {
      queueMicrotask(() => {
        setDraftBackup(null)
        setDirtyRoute(null)
        navigationBlocker.proceed()
      })
      return
    }
    navigationBlocker.reset()
  }, [navigationBlocker, saveMutation.isPending])

  if (meQuery.isPending || (!isNew && labSpecQuery.isPending)) {
    return (
      <main className="app-page">
        <LoadingState label="실습 정의를 불러오는 중..." />
      </main>
    )
  }

  const authError =
    (meQuery.error instanceof HttpError && meQuery.error.status === 401) ||
    (labSpecQuery.error instanceof HttpError && labSpecQuery.error.status === 401) ||
    (mappingOptionsQuery.error instanceof HttpError && mappingOptionsQuery.error.status === 401) ||
    (saveMutation.error instanceof HttpError && saveMutation.error.status === 401)

  if (authError) {
    return <LoginRedirect reason="sessionExpired" />
  }

  // Backend는 owner 외 상세를 403으로 거절합니다. 잘못된 consumer 응답에도 상세는 노출하지 않습니다.
  const forbiddenLabSpec =
    !isNew &&
    ((labSpecQuery.error instanceof HttpError && labSpecQuery.error.status === 403) ||
      (labSpecQuery.data &&
        meQuery.data &&
        labSpecQuery.data.labSpec.ownerUserId !== meQuery.data.id))

  if (forbiddenLabSpec) {
    return (
      <main className="app-page">
        <ErrorState message="이 LabSpec을 볼 권한이 없습니다." />
        <Link className="secondary-link" to="/lab-specs">
          실습 정의 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (!isNew && labSpecQuery.error instanceof HttpError && labSpecQuery.error.status === 404) {
    return (
      <main className="app-page">
        <ErrorState message="LabSpec을 찾을 수 없습니다." />
        <Link className="secondary-link" to="/lab-specs">
          실습 정의 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (meQuery.error || !meQuery.data || (!isNew && (labSpecQuery.error || !labSpecQuery.data))) {
    return (
      <main className="app-page">
        <ErrorState message="LabSpec 정보를 불러오지 못했습니다." />
      </main>
    )
  }

  if (mappingOptionsQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="Image/Size Mapping 후보를 불러오는 중..." />
      </main>
    )
  }

  if (isNew && (mappingOptionsQuery.error || !mappingOptionsQuery.data)) {
    const forbidden = mappingOptionsQuery.error instanceof HttpError &&
      mappingOptionsQuery.error.status === 403
    return (
      <main className="app-page">
        <ErrorState message={forbidden
          ? 'Mapping 후보를 조회할 강사 권한이 없습니다.'
          : 'Image/Size Mapping 후보를 불러오지 못했습니다.'}
        />
        {!forbidden && (
          <button className="secondary-button" type="button" onClick={() => void mappingOptionsQuery.refetch()}>
            다시 시도
          </button>
        )}
        <Link className="secondary-link" to="/lab-specs">
          실습 정의 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  const mappingError = mappingOptionsQuery.error || !mappingOptionsQuery.data
    ? mappingOptionsQuery.error instanceof HttpError && mappingOptionsQuery.error.status === 403
      ? 'forbidden'
      : 'unavailable'
    : undefined

  const versionedLabSpec = isNew ? undefined : labSpecQuery.data
  const labSpec = versionedLabSpec?.labSpec
  const isOwner = isNew || labSpec?.ownerUserId === meQuery.data.id
  const requireEtag = !isNew && isOwner && !versionedLabSpec?.etag
  const initial: LabSpecWrite = labSpec
    ? {
        name: labSpec.name,
        description: labSpec.description,
        vms: labSpec.vms.map((vm) => ({ ...vm })),
        workspaceVm: { ...labSpec.workspaceVm },
        internetOutbound: labSpec.internetOutbound,
        startupScript: labSpec.startupScript,
      }
    : emptyLabSpec

  return (
    <main className="app-page">
      <header className="page-header page-header-spacious labspec-editor-header">
        <div>
          <Link className="back-link" to="/lab-specs">
            ← 실습 정의 목록
          </Link>
          <p className="eyebrow">실습 정의</p>
          <h1>{isNew ? '새 실습 정의' : labSpec?.name}</h1>
          <p className="muted">
            여기서는 실습 구성을 저장합니다. 실제 환경 생성은 수업 상세에서 별도로 시작합니다.
          </p>
        </div>
      </header>

      {(saveSuccess === 'updated' ||
        (!isNew &&
          location.state?.labSpecJustCreatedId === resolvedLabSpecId &&
          !createdNoticeDismissed)) && (
        <div className="notice-card notice-success" role="status">
          {saveSuccess === 'updated'
            ? '실습 정의 변경사항이 저장되었습니다.'
            : '새 실습 정의가 저장되었습니다. 실제 환경 생성은 수업 상세에서 별도로 시작합니다.'}
        </div>
      )}

      {mappingError === 'unavailable' && (
        <button className="secondary-button" type="button" onClick={() => void mappingOptionsQuery.refetch()}>
          Mapping 후보 다시 시도
        </button>
      )}
      <LabSpecForm
        mappingOptions={mappingOptionsQuery.data ?? { items: [] }}
        mappingError={mappingError}
        key={
          isNew
            ? 'new-lab-spec'
            : `${resolvedLabSpecId}:${versionedLabSpec?.etag ?? 'missing-etag'}`
        }
        initial={initial}
        readOnly={!isOwner || Boolean(mappingError)}
        saving={saveMutation.isPending}
        saveError={saveMutation.error}
        requireEtag={requireEtag}
        forbiddenMessage={
          isNew
            ? 'LabSpec을 생성할 권한이 없습니다.'
            : '이 LabSpec을 수정할 권한이 없습니다.'
        }
        retainedDraft={
          draftBackup?.labSpecId === resolvedLabSpecId &&
          draftBackup.etag !== versionedLabSpec?.etag
            ? draftBackup.form
            : null
        }
        onDraftChange={captureDraft}
        onDiscardRetainedDraft={() => {
          setDraftBackup(null)
          setDirtyRoute(null)
        }}
        onSubmit={(input) => {
          setSaveSuccess(null)
          saveMutation.reset()
          saveMutation.mutate(input)
        }}
        onReload={() => {
          saveMutation.reset()
          void labSpecQuery.refetch()
        }}
      />
    </main>
  )
}
