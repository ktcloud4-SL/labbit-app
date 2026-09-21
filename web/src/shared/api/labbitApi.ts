import type {
  ClassDetail,
  ClassList,
  ClassMembershipList,
  CreateLabExecutionRequest,
  LabExecution,
  LabSpec,
  LabSpecList,
  LabSpecWrite,
  LoginRequest,
  Me,
  Operation,
  OperationAccepted,
  VersionedLabSpec,
} from './contracts'
import { request, requestWithMetadata } from './httpClient'

export const labbitQueryKeys = {
  me: ['me'] as const,
  classes: ['classes'] as const,
  classDetail: (classId: string) => ['classes', classId] as const,
  classMemberships: (classId: string) => ['classes', classId, 'memberships'] as const,
  labSpecs: ['lab-specs'] as const,
  labSpec: (labSpecId: string) => ['lab-specs', labSpecId] as const,
  labExecution: (labExecutionId: string) =>
    ['lab-executions', labExecutionId] as const,
  operation: (operationId: string) => ['operations', operationId] as const,
}

export interface LabbitApi {
  login(credentials: LoginRequest): Promise<void>
  logout(): Promise<void>
  getMe(): Promise<Me>
  listClasses(): Promise<ClassList>
  getClass(classId: string): Promise<ClassDetail>
  listClassMemberships(classId: string): Promise<ClassMembershipList>
  listLabSpecs(): Promise<LabSpecList>
  getLabSpec(labSpecId: string): Promise<VersionedLabSpec>
  createLabSpec(input: LabSpecWrite): Promise<LabSpec>
  updateLabSpec(
    labSpecId: string,
    input: LabSpecWrite,
    etag: string,
  ): Promise<VersionedLabSpec>
  createLabExecution(
    classId: string,
    input: CreateLabExecutionRequest,
    idempotencyKey: string,
  ): Promise<OperationAccepted>
  getLabExecution(labExecutionId: string): Promise<LabExecution>
  cleanupLabExecution(
    labExecutionId: string,
    idempotencyKey: string,
  ): Promise<OperationAccepted>
  resetLabInstance(
    labInstanceId: string,
    idempotencyKey: string,
  ): Promise<OperationAccepted>
  getOperation(operationId: string): Promise<Operation>
}

export const httpLabbitApi: LabbitApi = {
  login(credentials) {
    return request<void>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(credentials),
    })
  },

  logout() {
    return request<void>('/auth/logout', {
      method: 'POST',
    })
  },

  getMe() {
    return request<Me>('/me')
  },

  listClasses() {
    return request<ClassList>('/classes')
  },

  getClass(classId) {
    return request<ClassDetail>(`/classes/${encodeURIComponent(classId)}`)
  },

  listClassMemberships(classId) {
    return request<ClassMembershipList>(
      `/classes/${encodeURIComponent(classId)}/memberships`,
    )
  },

  listLabSpecs() {
    return request<LabSpecList>('/lab-specs')
  },

  async getLabSpec(labSpecId) {
    const response = await requestWithMetadata<LabSpec>(
      `/lab-specs/${encodeURIComponent(labSpecId)}`,
    )

    return {
      labSpec: response.data,
      etag: response.headers.get('etag') ?? undefined,
    }
  },

  createLabSpec(input) {
    return request<LabSpec>('/lab-specs', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },

  async updateLabSpec(labSpecId, input, etag) {
    const response = await requestWithMetadata<LabSpec>(
      `/lab-specs/${encodeURIComponent(labSpecId)}`,
      {
        method: 'PUT',
        headers: {
          'If-Match': etag,
        },
        body: JSON.stringify(input),
      },
    )

    return {
      labSpec: response.data,
      etag: response.headers.get('etag') ?? undefined,
    }
  },

  createLabExecution(classId, input, idempotencyKey) {
    return request<OperationAccepted>(
      `/classes/${encodeURIComponent(classId)}/lab-executions`,
      {
        method: 'POST',
        headers: {
          'Idempotency-Key': idempotencyKey,
        },
        body: JSON.stringify(input),
      },
    )
  },

  getLabExecution(labExecutionId) {
    return request<LabExecution>(
      `/lab-executions/${encodeURIComponent(labExecutionId)}`,
    )
  },

  cleanupLabExecution(labExecutionId, idempotencyKey) {
    return request<OperationAccepted>(
      `/lab-executions/${encodeURIComponent(labExecutionId)}/cleanup`,
      {
        method: 'POST',
        headers: {
          'Idempotency-Key': idempotencyKey,
        },
      },
    )
  },

  resetLabInstance(labInstanceId, idempotencyKey) {
    return request<OperationAccepted>(
      `/lab-instances/${encodeURIComponent(labInstanceId)}/reset`,
      {
        method: 'POST',
        headers: {
          'Idempotency-Key': idempotencyKey,
        },
      },
    )
  },

  getOperation(operationId) {
    return request<Operation>(`/operations/${encodeURIComponent(operationId)}`)
  },
}
