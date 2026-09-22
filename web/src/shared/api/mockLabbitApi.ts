import type {
  ClassDetail,
  ClassList,
  ClassMembershipList,
  LabSpec,
  LabSpecList,
  LabSpecWrite,
  LoginRequest,
  Me,
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
  'class-linux-networking': {
    id: 'class-linux-networking',
    name: 'Linux Networking',
    myRole: 'STUDENT',
  },
}

const mockMemberships: Record<string, ClassMembershipList> = {
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

let signedIn = false
let labSpecs: LabSpec[] = []
let labSpecVersions = new Map<string, number>()
let nextLabSpecId = 1

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
}
