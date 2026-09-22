import type {
  ClassDetail,
  ClassList,
  ClassMembershipList,
  LabSpec,
  LabSpecList,
  LabSpecWrite,
  LoginRequest,
  Me,
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
}
