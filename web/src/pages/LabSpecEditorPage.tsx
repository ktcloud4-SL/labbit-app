import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { Link, Navigate, useLocation, useNavigate, useParams } from 'react-router-dom'

import type { LabSpecWrite, VmRoleSpec } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
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

interface LabSpecFormProps {
  initial: LabSpecWrite
  readOnly: boolean
  saving: boolean
  saveError: unknown
  requireEtag: boolean
  onSubmit: (input: LabSpecWrite) => void
  onReload: () => void
}

function LabSpecForm({
  initial,
  readOnly,
  saving,
  saveError,
  requireEtag,
  onSubmit,
  onReload,
}: LabSpecFormProps) {
  const [form, setForm] = useState<LabSpecWrite>(initial)
  const [validationError, setValidationError] = useState<string | null>(null)

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
        <div className="notice-card">
          이 LabSpec은 다른 Instructor가 소유하고 있어 현재 계정에서는 읽기만 할 수 있습니다.
        </div>
      )}

      {requireEtag && (
        <div className="notice-card notice-warning">
          최신 ETag를 확인하지 못해 안전한 수정 요청을 보낼 수 없습니다. 다시 불러와 주세요.
          <button className="text-button" type="button" onClick={onReload}>
            다시 불러오기
          </button>
        </div>
      )}

      <section className="form-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">Basic</p>
            <h2>기본 정보</h2>
          </div>
        </div>

        <label className="field">
          <span>이름</span>
          <input
            value={form.name}
            disabled={readOnly}
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
            disabled={readOnly}
            onChange={(event) =>
              setForm((current) => ({ ...current, description: event.target.value }))
            }
          />
        </label>
      </section>

      <section className="form-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">Multi-VM</p>
            <h2>VM 구성</h2>
            <p className="muted">
              Image/Size 후보 조회 계약은 아직 별도 확정 전이므로 현재 화면은 OpenAPI의
              논리 참조값만 입력합니다.
            </p>
          </div>
          {!readOnly && (
            <button className="secondary-button" type="button" onClick={addVm}>
              VM Role 추가
            </button>
          )}
        </div>

        <div className="vm-list">
          {form.vms.map((vm, index) => (
            <article className="vm-row" key={index}>
              <div className="vm-row-heading">
                <strong>VM Role {index + 1}</strong>
                {!readOnly && form.vms.length > 1 && (
                  <button
                    className="text-button danger-text"
                    type="button"
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
                    disabled={readOnly}
                    placeholder="예: control"
                    onChange={(event) => updateVm(index, { role: event.target.value })}
                  />
                </label>
                <label className="field">
                  <span>표시 이름</span>
                  <input
                    value={vm.name ?? ''}
                    disabled={readOnly}
                    placeholder="선택"
                    onChange={(event) => updateVm(index, { name: event.target.value })}
                  />
                </label>
                <label className="field">
                  <span>Image 참조</span>
                  <input
                    value={vm.imageRef}
                    disabled={readOnly}
                    placeholder="논리 imageRef"
                    onChange={(event) =>
                      updateVm(index, { imageRef: event.target.value })
                    }
                  />
                </label>
                <label className="field">
                  <span>Size 참조</span>
                  <input
                    value={vm.sizeRef}
                    disabled={readOnly}
                    placeholder="논리 sizeRef"
                    onChange={(event) =>
                      updateVm(index, { sizeRef: event.target.value })
                    }
                  />
                </label>
                <label className="field">
                  <span>Count</span>
                  <input
                    type="number"
                    min={1}
                    value={vm.count}
                    disabled={readOnly}
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
            <p className="eyebrow">Workspace</p>
            <h2>Workspace VM</h2>
            <p className="muted">
              Editor와 기본 Preview가 사용할 VM 하나를 지정합니다. Terminal의 Multi-VM
              선택과는 별개입니다.
            </p>
          </div>
        </div>

        <label className="field">
          <span>Workspace VM</span>
          <select
            value={`${form.workspaceVm.role}:${form.workspaceVm.instanceIndex}`}
            disabled={readOnly}
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
            {form.vms.flatMap((vm) =>
              Array.from({ length: Math.max(1, vm.count) }, (_, instanceIndex) => (
                <option
                  key={`${vm.role}:${instanceIndex}`}
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
            disabled={readOnly}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                internetOutbound: event.target.checked,
              }))
            }
          />
          <span>Internet Outbound 허용</span>
        </label>

        <label className="field">
          <span>Startup Script</span>
          <textarea
            rows={8}
            value={form.startupScript ?? ''}
            disabled={readOnly}
            placeholder="선택 입력"
            onChange={(event) =>
              setForm((current) => ({ ...current, startupScript: event.target.value }))
            }
          />
        </label>
      </section>

      {(validationError || staleError || forbiddenError || validationServerError || saveError) && (
        <div className="form-error" role="alert">
          {validationError ??
            (staleError
              ? '다른 곳에서 LabSpec이 수정되었습니다. 최신 내용을 다시 불러온 뒤 다시 저장해 주세요.'
              : forbiddenError
                ? '이 LabSpec을 수정할 권한이 없습니다.'
                : validationServerError
                  ? '저장할 수 없는 값이 있습니다. 입력 내용을 확인해 주세요.'
                  : 'LabSpec을 저장하지 못했습니다. 잠시 후 다시 시도해 주세요.')}
          {staleError && (
            <button className="text-button" type="button" onClick={onReload}>
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
          <button className="primary-button" type="submit" disabled={saving || requireEtag}>
            {saving ? '저장 중...' : 'LabSpec 저장'}
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
      await queryClient.invalidateQueries({ queryKey: labbitQueryKeys.labSpecs })

      if (result.mode === 'create') {
        navigate(`/lab-specs/${encodeURIComponent(result.labSpec.id)}`, {
          replace: true,
        })
        return
      }

      queryClient.setQueryData(
        labbitQueryKeys.labSpec(resolvedLabSpecId),
        result.result,
      )
    },
  })

  if (meQuery.isPending || (!isNew && labSpecQuery.isPending)) {
    return (
      <main className="app-page">
        <LoadingState label="LabSpec을 불러오는 중..." />
      </main>
    )
  }

  const authError =
    (meQuery.error instanceof HttpError && meQuery.error.status === 401) ||
    (labSpecQuery.error instanceof HttpError && labSpecQuery.error.status === 401)

  if (authError) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }

  if (!isNew && labSpecQuery.error instanceof HttpError && labSpecQuery.error.status === 403) {
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
      <header className="page-header">
        <div>
          <Link className="back-link" to="/lab-specs">
            ← 실습 정의 목록
          </Link>
          <p className="eyebrow">LabSpec</p>
          <h1>{isNew ? '새 LabSpec' : labSpec?.name}</h1>
          <p className="muted">
            저장은 실습 정의만 변경합니다. 실제 환경 생성은 Class의 별도 Provision
            동작에서 시작합니다.
          </p>
        </div>
      </header>

      <LabSpecForm
        key={
          isNew
            ? 'new-lab-spec'
            : `${resolvedLabSpecId}:${versionedLabSpec?.etag ?? 'missing-etag'}`
        }
        initial={initial}
        readOnly={!isOwner}
        saving={saveMutation.isPending}
        saveError={saveMutation.error}
        requireEtag={requireEtag}
        onSubmit={(input) => {
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
