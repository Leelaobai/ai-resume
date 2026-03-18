import axios from 'axios'

const api = axios.create({
  baseURL: 'http://localhost:8090/api',
})

export interface Session {
  id: string
  title: string
  created_at: string
  updated_at: string
}

export interface Message {
  id: string
  session_id: string
  role: string
  content: string
  created_at: string
}

export interface ResumeData {
  basic_info: {
    name: string
    title: string
    email: string
    phone: string
    location: string
    website: string
    github: string
  }
  summary: string
  experience: Array<{
    company: string
    title: string
    start_date: string
    end_date: string
    description: string[]
  }>
  education: Array<{
    school: string
    degree: string
    major: string
    start_date: string
    end_date: string
  }>
  skills: Array<{
    category: string
    items: string[]
  }>
  projects: Array<{
    name: string
    role: string
    description: string
    highlights: string[]
    tech_stack: string[]
  }>
}

export interface Resume {
  id: string
  session_id: string
  data: ResumeData
  template_id: string
}

// 会话API
export const createSession = (title: string) =>
  api.post<Session>('/sessions', { title })

export const listSessions = () =>
  api.get<Session[]>('/sessions')

export const deleteSession = (id: string) =>
  api.delete(`/sessions/${id}`)

export const updateSession = (id: string, title: string) =>
  api.patch(`/sessions/${id}`, { title })

export const updateTemplate = (sessionId: string, templateId: string) =>
  api.patch(`/sessions/${sessionId}/resume/template`, { template_id: templateId })

// 消息API
export const getMessages = (sessionId: string) =>
  api.get<Message[]>(`/sessions/${sessionId}/messages`)

// 简历API
export const getResume = (sessionId: string) =>
  api.get<Resume>(`/sessions/${sessionId}/resume`)

// SSE聊天（不用axios，用fetch）
export function chatSSE(
  sessionId: string,
  message: string,
  onEvent: (event: { type: string; data: any }) => void,
  onDone: () => void,
  onError: (err: string) => void
) {
  const ctrl = new AbortController()

  fetch(`http://localhost:8090/api/sessions/${sessionId}/chat`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ message }),
    signal: ctrl.signal,
  })
    .then(async (resp) => {
      if (!resp.ok || !resp.body) {
        onError(`HTTP ${resp.status}`)
        return
      }

      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          const jsonStr = line.slice(6)
          try {
            const event = JSON.parse(jsonStr)
            if (event.type === 'done') {
              onDone()
            } else if (event.type === 'error') {
              onError(event.data.message)
            } else {
              onEvent(event)
            }
          } catch {
            // skip
          }
        }
      }
      onDone()
    })
    .catch((err) => {
      if (err.name !== 'AbortError') onError(err.message)
    })

  return ctrl
}
