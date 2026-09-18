export type ResourceId = string

export type OrganizationRole = 'ADMIN' | 'MEMBER'
export type ClassRole = 'INSTRUCTOR' | 'STUDENT'

export interface LoginRequest {
  username: string
  password: string
}

export interface OrganizationSummary {
  id: ResourceId
  name: string
}

export interface Me {
  id: ResourceId
  username: string
  organization: OrganizationSummary
  organizationRole: OrganizationRole
}

export interface LabExecutionSummary {
  id: ResourceId
  status: string
}

export interface LabInstanceSummary {
  id: ResourceId
  userId: ResourceId
  status: string
  generation: number
}

export interface ClassSummary {
  id: ResourceId
  name: string
  myRole: ClassRole
  activeLabExecution?: LabExecutionSummary
}

export interface ClassDetail {
  id: ResourceId
  name: string
  myRole: ClassRole
  activeLabExecution?: LabExecutionSummary
  myLabInstance?: LabInstanceSummary
}

export interface ClassList {
  items: ClassSummary[]
  nextCursor?: string
}

export interface ClassMembership {
  userId: ResourceId
  username: string
  role: ClassRole
}

export interface ClassMembershipList {
  items: ClassMembership[]
  nextCursor?: string
}
