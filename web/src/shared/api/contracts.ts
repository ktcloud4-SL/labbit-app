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

export type WorkspaceFileKind = 'file' | 'directory'

export interface WorkspaceFileEntry {
  name: string
  path: string
  kind: WorkspaceFileKind
}

export interface WorkspaceFileTree {
  path: string
  items: WorkspaceFileEntry[]
}

export interface WorkspaceFileContent {
  path: string
  content: string
}

export interface WorkspaceFileSaved {
  path: string
}

export interface VersionedWorkspaceFileContent {
  file: WorkspaceFileContent
  etag: string
}

export interface VersionedWorkspaceFileSaved {
  file: WorkspaceFileSaved
  etag: string
}

export interface CreatePreviewSessionRequest {
  targetPort: number
}

export interface PreviewSession {
  id: ResourceId
  targetPort: number
  /** One-time bootstrap credential may be in fragment: never persist or log this URL. */
  previewUrl: string
  expiresAt: string
}

export interface TerminalTarget {
  vmKey: string
  role: string
  instanceIndex: number
}

export interface TerminalTargetList {
  generation: number
  workspaceVmKey: string
  items: TerminalTarget[]
}

export interface CreateTerminalSessionRequest {
  targetVmKey: string
  cols: number
  rows: number
}

export interface TerminalSession {
  id: ResourceId
  generation: number
  sessionToken: string
  tokenExpiresAt: string
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

export interface VmRoleSpec {
  role: string
  name?: string
  imageRef: string
  sizeRef: string
  count: number
}

export interface WorkspaceVmSelector {
  role: string
  instanceIndex: number
}

export interface LabSpecSummary {
  id: ResourceId
  name: string
  description?: string
  ownerUserId: ResourceId
}

export interface LabSpec extends LabSpecSummary {
  vms: VmRoleSpec[]
  workspaceVm: WorkspaceVmSelector
  internetOutbound: boolean
  startupScript?: string
}

export interface LabSpecWrite {
  name: string
  description?: string
  vms: VmRoleSpec[]
  workspaceVm: WorkspaceVmSelector
  internetOutbound: boolean
  startupScript?: string
}

export interface ImageMappingOption {
  id: ResourceId
  displayName: string
}

export interface FlavorMappingOption {
  id: ResourceId
  displayName: string
  vcpus: number
  ramMiB: number
  diskGiB: number
}

export interface LabSpecMappingOptions {
  providerConnectionId: ResourceId
  images: ImageMappingOption[]
  flavors: FlavorMappingOption[]
}

export interface LabSpecMappingOptionsList {
  items: LabSpecMappingOptions[]
}

export interface LabSpecList {
  items: LabSpecSummary[]
  nextCursor?: string
}

export interface VersionedLabSpec {
  labSpec: LabSpec
  etag?: string
}

export interface CreateLabExecutionRequest {
  labSpecId: ResourceId
  targetStudentIds: ResourceId[]
}

export interface LabExecution {
  id: ResourceId
  classId: ResourceId
  labSpecId: ResourceId
  instructorUserId: ResourceId
  targetUserIds: ResourceId[]
  status: string
  labInstances: LabInstanceSummary[]
}

export interface ResourceRef {
  type: string
  id: ResourceId
}

export interface OperationAccepted {
  operationId: ResourceId
  target: ResourceRef
}

export interface OperationError {
  code: string
  message?: string
}

export interface Operation {
  id: ResourceId
  type: string
  status: string
  stage?: string
  target: ResourceRef
  error?: OperationError
  createdAt: string
  updatedAt: string
  startedAt?: string
  finishedAt?: string
}

/** LBT-182 OpenAPI: source of truth is contracts/http/openapi.yaml */
export interface ConnectorEnrollmentTokenIssued {
  tokenId: ResourceId
  enrollmentToken: string
  expiresAt: string
}

export interface ConnectorSummary {
  id: ResourceId
  name: string
  registrationStatus: 'REGISTERED' | 'REVOKED'
  connectionStatus: 'NEVER_SEEN' | 'ONLINE' | 'OFFLINE' | 'REVOKED'
  createdAt: string
  revokedAt: string | null
  lastSeenAt: string | null
  lastConnectorVersion: string | null
  lastRuntimeId: string | null
}

export interface ConnectorList {
  items: ConnectorSummary[]
  observedAt: string
}
