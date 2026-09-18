import type {
  ClassDetail,
  ClassList,
  ClassMembershipList,
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

let signedIn = false

function requireSession() {
  if (!signedIn) {
    throw new HttpError(401)
  }
}

export function resetMockApiSession() {
  signedIn = false
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
}
