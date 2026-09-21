import type {
  ClassDetail,
  ClassList,
  ClassMembershipList,
  LabSpec,
  LabSpecList,
  LabExecution,
  LabSpecWrite,
  LoginRequest,
  Me,
  Operation,
} from './contracts'
import { HttpError } from './httpClient'
import type { LabbitApi } from './labbitApi'

export const mockCredentials: LoginRequest = {
  username: 'heechul',
  password: 'password',
}

export const mockMe: Me = {
  id: 'user-heechul',
  username: 'heechul',
  organization: {
    id: 'org-samsunglions',
    name: 'SamsungLions Org',
  },
  organizationRole: 'MEMBER',
}

export const mockClasses: ClassList = {
  items: [
    {
      id: 'class-kubernetes-basic',
      name: 'Kubernetes Basic',
      myRole: 'INSTRUCTOR',
      activeLabExecution: {
        id: 'execution-kubernetes-basic',
        status: 'ACTIVE',
      },
    },
    {
      id: 'class-docker-basic',
      name: 'Docker Basic',
      myRole: 'INSTRUCTOR',
    },
    {
      id: 'class-linux-networking',
      name: 'Linux Networking',
      myRole: 'STUDENT',
    },
  ],
}

const mockClassDetails: Record<string, ClassDetail> = {
  'class-kubernetes-basic': {
    id: 'class-kubernetes-basic',
    name: 'Kubernetes Basic',
    myRole: 'INSTRUCTOR',
    activeLabExecution: {
      id: 'execution-kubernetes-basic',
      status: 'ACTIVE',
    },
    myLabInstance: {
      id: 'lab-instance-heechul',
      userId: mockMe.id,
      status: 'READY',
      generation: 1,
    },
  },
  'class-docker-basic': {
    id: 'class-docker-basic',
    name: 'Docker Basic',
    myRole: 'INSTRUCTOR',
  },
  'class-linux-networking': {
    id: 'class-linux-networking',
    name: 'Linux Networking',
    myRole: 'STUDENT',
  },
}

const mockMemberships: Record<string, ClassMembershipList> = {
  'class-docker-basic': {
    items: [
      {
        userId: mockMe.id,
        username: mockMe.username,
        role: 'INSTRUCTOR',
      },
      {
        userId: 'user-student-a',
        username: 'student-a',
        role: 'STUDENT',
      },
      {
        userId: 'user-student-b',
        username: 'student-b',
        role: 'STUDENT',
      },
    ],
  },
  'class-kubernetes-basic': {
    items: [
      {
        userId: mockMe.id,
        username: mockMe.username,
        role: 'INSTRUCTOR',
      },
      {
        userId: 'user-student-a',
        username: 'student-a',
        role: 'STUDENT',
      },
      {
        userId: 'user-student-b',
        username: 'student-b',
        role: 'STUDENT',
      },
    ],
  },
}

const initialLabSpecs: LabSpec[] = [
  {
    id: 'lab-spec-kubernetes-basic',
    name: 'Kubernetes Basic',
    description: 'control과 worker로 구성한 기본 Kubernetes 실습 정의',
    ownerUserId: mockMe.id,
    vms: [
      {
        role: 'control',
        name: 'Control Plane',
        imageRef: 'ubuntu-24.04',
        sizeRef: 'medium',
        count: 1,
      },
      {
        role: 'worker',
        name: 'Worker',
        imageRef: 'ubuntu-24.04',
        sizeRef: 'medium',
        count: 2,
      },
    ],
    workspaceVm: {
      role: 'control',
      instanceIndex: 0,
    },
    internetOutbound: true,
    startupScript: '#!/bin/bash\necho "Labbit workspace ready"',
  },
  {
    id: 'lab-spec-linux-networking',
    name: 'Linux Networking',
    description: '다른 Instructor가 소유한 읽기 전용 예시',
    ownerUserId: 'user-instructor-other',
    vms: [
      {
        role: 'node',
        imageRef: 'rocky-9',
        sizeRef: 'small',
        count: 2,
      },
    ],
    workspaceVm: {
      role: 'node',
      instanceIndex: 0,
    },
    internetOutbound: false,
  },
]

const initialLabExecutions: LabExecution[] = [
  {
    id: 'execution-kubernetes-basic',
    classId: 'class-kubernetes-basic',
    labSpecId: 'lab-spec-kubernetes-basic',
    instructorUserId: mockMe.id,
    targetUserIds: ['user-student-a', 'user-student-b'],
    status: 'ACTIVE',
    labInstances: [
      {
        id: 'lab-instance-heechul',
        userId: mockMe.id,
        status: 'READY',
        generation: 1,
      },
      {
        id: 'lab-instance-student-a',
        userId: 'user-student-a',
        status: 'READY',
        generation: 1,
      },
      {
        id: 'lab-instance-student-b',
        userId: 'user-student-b',
        status: 'ERROR',
        generation: 1,
      },
    ],
  },
]

let signedIn = false
let labSpecs: LabSpec[] = []
let labSpecVersions = new Map<string, number>()
let nextLabSpecId = 1
let labExecutions: LabExecution[] = []
let operations = new Map<string, Operation>()
let operationReads = new Map<string, number>()
let nextExecutionId = 1

function cloneLabSpec(labSpec: LabSpec): LabSpec {
  return {
    ...labSpec,
    vms: labSpec.vms.map((vm) => ({ ...vm })),
    workspaceVm: { ...labSpec.workspaceVm },
  }
}

function currentEtag(labSpecId: string) {
  return `"mock-${labSpecId}-v${labSpecVersions.get(labSpecId) ?? 1}"`
}

function resetMockData() {
  labSpecs = initialLabSpecs.map(cloneLabSpec)
  labSpecVersions = new Map(initialLabSpecs.map((labSpec) => [labSpec.id, 1]))
  nextLabSpecId = 1
  labExecutions = initialLabExecutions.map((execution) => ({
    ...execution,
    targetUserIds: [...execution.targetUserIds],
    labInstances: execution.labInstances.map((instance) => ({ ...instance })),
  }))
  operations = new Map()
  operationReads = new Map()
  nextExecutionId = 1
}

resetMockData()

function requireSession() {
  if (!signedIn) {
    throw new HttpError(401)
  }
}

export function resetMockApiSession() {
  signedIn = false
  resetMockData()
}

export const mockLabbitApi: LabbitApi = {
  async login(credentials) {
    if (
      credentials.username !== mockCredentials.username ||
      credentials.password !== mockCredentials.password
    ) {
      throw new HttpError(401)
    }

    signedIn = true
  },

  async logout() {
    requireSession()
    signedIn = false
  },

  async getMe() {
    requireSession()
    return mockMe
  },

  async listClasses() {
    requireSession()
    return mockClasses
  },

  async getClass(classId) {
    requireSession()

    const classDetail = mockClassDetails[classId]
    if (!classDetail) {
      throw new HttpError(404)
    }

    return classDetail
  },

  async listClassMemberships(classId) {
    requireSession()

    const memberships = mockMemberships[classId]
    if (!memberships) {
      throw new HttpError(404)
    }

    return memberships
  },

  async listLabSpecs() {
    requireSession()

    const result: LabSpecList = {
      items: labSpecs.map(({ id, name, description, ownerUserId }) => ({
        id,
        name,
        description,
        ownerUserId,
      })),
    }

    return result
  },

  async getLabSpec(labSpecId) {
    requireSession()

    const labSpec = labSpecs.find((item) => item.id === labSpecId)
    if (!labSpec) {
      throw new HttpError(404)
    }

    return {
      labSpec: cloneLabSpec(labSpec),
      etag: currentEtag(labSpec.id),
    }
  },

  async createLabSpec(input: LabSpecWrite) {
    requireSession()

    const labSpec: LabSpec = {
      id: `lab-spec-created-${nextLabSpecId++}`,
      ownerUserId: mockMe.id,
      ...input,
      vms: input.vms.map((vm) => ({ ...vm })),
      workspaceVm: { ...input.workspaceVm },
    }

    labSpecs.push(labSpec)
    labSpecVersions.set(labSpec.id, 1)

    return cloneLabSpec(labSpec)
  },

  async updateLabSpec(labSpecId, input, etag) {
    requireSession()

    const index = labSpecs.findIndex((item) => item.id === labSpecId)
    if (index < 0) {
      throw new HttpError(404)
    }

    const current = labSpecs[index]
    if (current.ownerUserId !== mockMe.id) {
      throw new HttpError(403)
    }

    if (etag !== currentEtag(labSpecId)) {
      throw new HttpError(412)
    }

    const updated: LabSpec = {
      id: current.id,
      ownerUserId: current.ownerUserId,
      ...input,
      vms: input.vms.map((vm) => ({ ...vm })),
      workspaceVm: { ...input.workspaceVm },
    }

    labSpecs[index] = updated
    labSpecVersions.set(labSpecId, (labSpecVersions.get(labSpecId) ?? 1) + 1)

    return {
      labSpec: cloneLabSpec(updated),
      etag: currentEtag(labSpecId),
    }
  },

  async createLabExecution(classId, input) {
    requireSession()

    const classDetail = mockClassDetails[classId]
    if (!classDetail) {
      throw new HttpError(404)
    }
    if (classDetail.myRole !== 'INSTRUCTOR') {
      throw new HttpError(403)
    }
    if (classDetail.activeLabExecution) {
      throw new HttpError(409)
    }

    const students = new Set(
      (mockMemberships[classId]?.items ?? [])
        .filter((membership) => membership.role === 'STUDENT')
        .map((membership) => membership.userId),
    )

    if (
      input.targetStudentIds.length === 0 ||
      input.targetStudentIds.some((userId) => !students.has(userId))
    ) {
      throw new HttpError(422)
    }

    const executionId = `execution-created-${nextExecutionId++}`
    const operationId = `operation-provision-${executionId}`
    const labInstances = [
      {
        id: `lab-instance-${executionId}-instructor`,
        userId: mockMe.id,
        status: 'PROVISIONING',
        generation: 1,
      },
      ...input.targetStudentIds.map((userId) => ({
        id: `lab-instance-${executionId}-${userId}`,
        userId,
        status: 'PROVISIONING',
        generation: 1,
      })),
    ]

    const execution: LabExecution = {
      id: executionId,
      classId,
      labSpecId: input.labSpecId,
      instructorUserId: mockMe.id,
      targetUserIds: [...input.targetStudentIds],
      status: 'PROVISIONING',
      labInstances,
    }

    labExecutions.push(execution)
    classDetail.activeLabExecution = {
      id: executionId,
      status: 'PROVISIONING',
    }

    const now = new Date().toISOString()
    operations.set(operationId, {
      id: operationId,
      type: 'PROVISION',
      status: 'RUNNING',
      stage: 'PROVISIONING',
      target: {
        type: 'LAB_EXECUTION',
        id: executionId,
      },
      createdAt: now,
      updatedAt: now,
      startedAt: now,
    })
    operationReads.set(operationId, 0)

    return {
      operationId,
      target: {
        type: 'LAB_EXECUTION',
        id: executionId,
      },
    }
  },

  async getLabExecution(labExecutionId) {
    requireSession()

    const execution = labExecutions.find((item) => item.id === labExecutionId)
    if (!execution) {
      throw new HttpError(404)
    }

    return {
      ...execution,
      targetUserIds: [...execution.targetUserIds],
      labInstances: execution.labInstances.map((instance) => ({ ...instance })),
    }
  },

  async getOperation(operationId) {
    requireSession()

    const operation = operations.get(operationId)
    if (!operation) {
      throw new HttpError(404)
    }

    const reads = (operationReads.get(operationId) ?? 0) + 1
    operationReads.set(operationId, reads)

    if (reads >= 2 && operation.status === 'RUNNING') {
      operation.status = 'SUCCEEDED'
      operation.stage = 'READY'
      operation.updatedAt = new Date().toISOString()
      operation.finishedAt = operation.updatedAt

      const execution = labExecutions.find(
        (item) => item.id === operation.target.id,
      )
      if (execution) {
        execution.status = 'ACTIVE'
        execution.labInstances = execution.labInstances.map((instance) => ({
          ...instance,
          status: 'READY',
        }))
        const classDetail = mockClassDetails[execution.classId]
        if (classDetail?.activeLabExecution) {
          classDetail.activeLabExecution.status = 'ACTIVE'
        }
      }
    }

    return {
      ...operation,
      target: { ...operation.target },
      error: operation.error ? { ...operation.error } : undefined,
    }
  },
}
