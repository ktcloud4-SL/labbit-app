import type {
  ClassDetail,
  ClassList,
  ClassMembershipList,
  LoginRequest,
  Me,
} from './contracts'
import { request } from './httpClient'

export const labbitQueryKeys = {
  me: ['me'] as const,
  classes: ['classes'] as const,
  classDetail: (classId: string) => ['classes', classId] as const,
  classMemberships: (classId: string) => ['classes', classId, 'memberships'] as const,
}

export interface LabbitApi {
  login(credentials: LoginRequest): Promise<void>
  logout(): Promise<void>
  getMe(): Promise<Me>
  listClasses(): Promise<ClassList>
  getClass(classId: string): Promise<ClassDetail>
  listClassMemberships(classId: string): Promise<ClassMembershipList>
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
}
