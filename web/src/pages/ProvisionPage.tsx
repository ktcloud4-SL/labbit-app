import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { Link, Navigate, useLocation, useNavigate, useParams } from 'react-router-dom'

import type { CreateLabExecutionRequest } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

interface ProvisionConfirmation {
  request: CreateLabExecutionRequest
  idempotencyKey: string
}

function createIdempotencyKey() {
  return crypto.randomUUID()
}

export function ProvisionPage() {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const { classId } = useParams()
  const resolvedClassId = classId ?? ''
  const [labSpecId, setLabSpecId] = useState('')
  const [selectedStudentIds, setSelectedStudentIds] = useState<string[]>([])
  const [validationError, setValidationError] = useState<string | null>(null)
  const [confirmation, setConfirmation] = useState<ProvisionConfirmation | null>(null)

  const classQuery = useQuery({
    queryKey: labbitQueryKeys.classDetail(resolvedClassId),
    queryFn: () => api.getClass(resolvedClassId),
    enabled: Boolean(resolvedClassId),
    retry: false,
  })
  const canLoadProvisionInputs =
    classQuery.data?.myRole === 'INSTRUCTOR' &&
    !classQuery.data.activeLabExecution

  const membershipsQuery = useQuery({
    queryKey: labbitQueryKeys.classMemberships(resolvedClassId),
    queryFn: () => api.listClassMemberships(resolvedClassId),
    enabled: Boolean(resolvedClassId && canLoadProvisionInputs),
    retry: false,
  })
  const labSpecsQuery = useQuery({
    queryKey: labbitQueryKeys.labSpecs,
    queryFn: () => api.listLabSpecs(),
    enabled: canLoadProvisionInputs,
    retry: false,
  })

  const provisionMutation = useMutation({
    mutationFn: (pending: ProvisionConfirmation) =>
      api.createLabExecution(
        resolvedClassId,
        pending.request,
        pending.idempotencyKey,
      ),
    onSuccess: async (accepted) => {
      await queryClient.invalidateQueries({
        queryKey: labbitQueryKeys.classDetail(resolvedClassId),
      })
      navigate(`/operations/${encodeURIComponent(accepted.operationId)}`, {
        replace: true,
      })
    },
  })

  if (!resolvedClassId) {
    return (
      <main className="app-page">
        <ErrorState message="Class ID가 없습니다." />
      </main>
    )
  }

  if (classQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="Class 권한을 확인하는 중..." />
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 401) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 403) {
    return (
      <main className="app-page">
        <ErrorState message="이 Class를 운영할 권한이 없습니다." />
        <Link className="secondary-link" to="/classes">
          수업 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 404) {
    return (
      <main className="app-page">
        <ErrorState message="Class를 찾을 수 없습니다." />
        <Link className="secondary-link" to="/classes">
          수업 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (classQuery.error || !classQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="Class 정보를 불러오지 못했습니다." />
      </main>
    )
  }

  if (classQuery.data.myRole !== 'INSTRUCTOR') {
    return (
      <main className="app-page">
        <ErrorState message="INSTRUCTOR만 새 실습 환경을 생성할 수 있습니다." />
        <Link className="secondary-link" to={`/classes/${encodeURIComponent(resolvedClassId)}`}>
          Class 상세로 돌아가기
        </Link>
      </main>
    )
  }

  if (classQuery.data.activeLabExecution) {
    return (
      <main className="app-page">
        <header className="page-header">
          <div>
            <Link className="back-link" to={`/classes/${encodeURIComponent(resolvedClassId)}`}>
              ← Class 상세
            </Link>
            <p className="eyebrow">Provision</p>
            <h1>새 환경 생성</h1>
          </div>
        </header>
        <section className="notice-card notice-warning">
          <strong>이미 활성 LabExecution이 있습니다.</strong>
          <p className="muted">
            중복 Provision은 허용되지 않습니다. 현재 실행을 Cleanup한 뒤 새 실행을 시작해야 합니다.
          </p>
          <Link
            className="primary-link inline-link"
            to={`/lab-executions/${encodeURIComponent(classQuery.data.activeLabExecution.id)}`}
          >
            현재 실습 운영 보기
          </Link>
        </section>
      </main>
    )
  }

  if (membershipsQuery.isPending || labSpecsQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="Provision 정보를 준비하는 중..." />
      </main>
    )
  }

  const inputErrors = [membershipsQuery.error, labSpecsQuery.error]
  if (inputErrors.some((error) => error instanceof HttpError && error.status === 401)) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }

  if (
    membershipsQuery.error ||
    labSpecsQuery.error ||
    !membershipsQuery.data ||
    !labSpecsQuery.data
  ) {
    return (
      <main className="app-page">
        <ErrorState message="Provision에 필요한 정보를 불러오지 못했습니다." />
      </main>
    )
  }

  const students = membershipsQuery.data.items.filter(
    (membership) => membership.role === 'STUDENT',
  )
  const selectedLabSpec = labSpecsQuery.data.items.find(
    (labSpec) => labSpec.id === labSpecId,
  )
  const selectedStudents = students.filter((student) =>
    selectedStudentIds.includes(student.userId),
  )

  function handlePrepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setValidationError(null)
    provisionMutation.reset()

    if (!labSpecId) {
      setValidationError('Provision에 사용할 LabSpec을 선택해 주세요.')
      return
    }

    if (selectedStudentIds.length === 0) {
      setValidationError('이번 실행에 참여할 학생을 최소 1명 선택해 주세요.')
      return
    }

    setConfirmation({
      request: {
        labSpecId,
        targetStudentIds: [...selectedStudentIds],
      },
      idempotencyKey: createIdempotencyKey(),
    })
  }

  const mutationError = provisionMutation.error
  const errorMessage =
    mutationError instanceof HttpError && mutationError.status === 409
      ? '활성 LabExecution 또는 다른 변경 작업과 충돌했습니다. 현재 상태를 다시 확인해 주세요.'
      : mutationError instanceof HttpError && mutationError.status === 422
        ? '선택한 LabSpec 또는 학생 대상이 현재 Class 규칙과 맞지 않습니다.'
        : mutationError instanceof HttpError && mutationError.status === 503
          ? 'Connector 또는 Provider가 일시적으로 사용할 수 없습니다.'
          : mutationError
            ? 'Provision 요청을 접수하지 못했습니다. 잠시 후 다시 시도해 주세요.'
            : null

  return (
    <main className="app-page">
      <header className="page-header">
        <div>
          <Link className="back-link" to={`/classes/${encodeURIComponent(resolvedClassId)}`}>
            ← Class 상세
          </Link>
          <p className="eyebrow">Provision</p>
          <h1>{classQuery.data.name} · 새 환경 생성</h1>
          <p className="muted">
            저장된 LabSpec과 이번 실행의 학생 대상을 확인한 뒤 LabExecution을 시작합니다.
          </p>
        </div>
      </header>

      {!confirmation ? (
        <form className="labspec-form" onSubmit={handlePrepare}>
          <section className="form-section">
            <div className="section-heading">
              <div>
                <p className="eyebrow">LabSpec</p>
                <h2>실습 정의 선택</h2>
              </div>
              <Link className="secondary-link" to="/lab-specs">
                LabSpec 관리
              </Link>
            </div>
            <label className="field">
              <span>LabSpec</span>
              <select
                value={labSpecId}
                onChange={(event) => setLabSpecId(event.target.value)}
              >
                <option value="">선택해 주세요</option>
                {labSpecsQuery.data.items.map((labSpec) => (
                  <option key={labSpec.id} value={labSpec.id}>
                    {labSpec.name}
                  </option>
                ))}
              </select>
            </label>
          </section>

          <section className="form-section">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Target</p>
                <h2>참여 학생 선택</h2>
                <p className="muted">
                  실행 시작 시 선택한 학생 집합이 고정되며 이후 Membership 변경은 현재 실행에 자동 반영되지 않습니다.
                </p>
              </div>
            </div>

            {students.length === 0 ? (
              <div className="empty-state compact-empty">
                <h2>선택할 STUDENT가 없습니다.</h2>
              </div>
            ) : (
              <div className="selection-list">
                {students.map((student) => (
                  <label className="selection-row" key={student.userId}>
                    <input
                      type="checkbox"
                      checked={selectedStudentIds.includes(student.userId)}
                      onChange={(event) =>
                        setSelectedStudentIds((current) =>
                          event.target.checked
                            ? [...current, student.userId]
                            : current.filter((userId) => userId !== student.userId),
                        )
                      }
                    />
                    <span>
                      <strong>{student.username}</strong>
                      <small>{student.userId}</small>
                    </span>
                  </label>
                ))}
              </div>
            )}
          </section>

          {validationError && (
            <p className="form-error" role="alert">
              {validationError}
            </p>
          )}

          <div className="form-actions">
            <Link className="secondary-link" to={`/classes/${encodeURIComponent(resolvedClassId)}`}>
              취소
            </Link>
            <button className="primary-button" type="submit">
              생성 내용 확인
            </button>
          </div>
        </form>
      ) : (
        <section className="confirmation-card">
          <p className="eyebrow">Confirm</p>
          <h2>Provision 시작 전 확인</h2>
          <dl className="summary-list">
            <div>
              <dt>Class</dt>
              <dd>{classQuery.data.name}</dd>
            </div>
            <div>
              <dt>LabSpec</dt>
              <dd>{selectedLabSpec?.name ?? confirmation.request.labSpecId}</dd>
            </div>
            <div>
              <dt>대상 학생</dt>
              <dd>{selectedStudents.map((student) => student.username).join(', ')}</dd>
            </div>
            <div>
              <dt>생성 범위</dt>
              <dd>강사용 LabInstance 1개 + 선택한 학생별 LabInstance</dd>
            </div>
          </dl>
          <p className="muted">
            요청이 접수되면 학생 대상 집합이 고정되고 비동기 Operation으로 생성 상태를 추적합니다.
          </p>

          {errorMessage && (
            <p className="form-error" role="alert">
              {errorMessage}
            </p>
          )}

          <div className="form-actions">
            <button
              className="secondary-button"
              type="button"
              disabled={provisionMutation.isPending}
              onClick={() => {
                provisionMutation.reset()
                setConfirmation(null)
              }}
            >
              다시 선택
            </button>
            <button
              className="primary-button"
              type="button"
              disabled={provisionMutation.isPending}
              onClick={() => provisionMutation.mutate(confirmation)}
            >
              {provisionMutation.isPending ? '요청 중...' : 'Provision 시작'}
            </button>
          </div>
        </section>
      )}
    </main>
  )
}
