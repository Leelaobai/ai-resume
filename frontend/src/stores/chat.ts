import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createSession,
  listSessions,
  deleteSession,
  updateSession,
  getMessages,
  getResume,
  chatSSE,
  type Session,
  type Message,
  type Resume,
} from '../api/client'

export interface MessageBlock {
  type: 'text' | 'tool_call' | 'resume_update'
  content?: string
  toolName?: string
  toolStatus?: 'running' | 'done'
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'tool'
  content: string
  blocks?: MessageBlock[]
  loading?: boolean
}

export const useChatStore = defineStore('chat', () => {
  const sessions = ref<Session[]>([])
  const currentSessionId = ref<string | null>(null)
  const messages = ref<ChatMessage[]>([])
  const resume = ref<Resume | null>(null)
  const sending = ref(false)

  async function loadSessions() {
    const resp = await listSessions()
    sessions.value = resp.data || []
  }

  async function newSession() {
    const resp = await createSession('新简历')
    sessions.value.unshift(resp.data)
    await selectSession(resp.data.id)
  }

  async function selectSession(id: string) {
    currentSessionId.value = id
    messages.value = []

    // 加载历史消息
    try {
      const resp = await getMessages(id)
      if (resp.data) {
        messages.value = resp.data.map((m: Message) => ({
          role: m.role as ChatMessage['role'],
          content: m.content,
        }))
      }
    } catch {
      // 新会话没有消息
    }

    // 加载简历
    await loadResume()
  }

  async function loadResume() {
    if (!currentSessionId.value) return
    try {
      const resp = await getResume(currentSessionId.value)
      resume.value = resp.data
    } catch {
      resume.value = null
    }
  }

  async function removeSession(id: string) {
    await deleteSession(id)
    sessions.value = sessions.value.filter((s) => s.id !== id)
    if (currentSessionId.value === id) {
      currentSessionId.value = null
      messages.value = []
      resume.value = null
    }
  }

  function sendMessage(content: string) {
    if (!currentSessionId.value || sending.value) return

    sending.value = true

    // 第一条消息时自动更新会话标题
    if (messages.value.length === 0) {
      const title = content.slice(0, 30) + (content.length > 30 ? '...' : '')
      const sid = currentSessionId.value
      updateSession(sid, title).then(() => {
        const s = sessions.value.find((s) => s.id === sid)
        if (s) s.title = title
      }).catch(() => {})
    }

    // 添加用户消息
    messages.value.push({ role: 'user', content })

    // 添加助手占位消息（用blocks记录事件流）
    messages.value.push({ role: 'assistant', content: '', blocks: [], loading: true })
    const assistantMsg = messages.value[messages.value.length - 1]

    chatSSE(
      currentSessionId.value,
      content,
      // onEvent
      (event) => {
        const blocks = assistantMsg.blocks!

        if (event.type === 'token') {
          // 每轮 ReAct 的文本输出作为独立块
          blocks.push({ type: 'text', content: event.data.content })
          assistantMsg.content += event.data.content
          assistantMsg.loading = false
        } else if (event.type === 'tool_call') {
          const toolName = event.data.name
          if (toolName === 'get_current_resume' || toolName === 'get_current_template') return // 静默
          blocks.push({ type: 'tool_call', toolName, toolStatus: 'running' })
          assistantMsg.loading = false
        } else if (event.type === 'tool_result') {
          const toolName = event.data.name
          // 找到最后一个同名且running的tool_call block
          for (let i = blocks.length - 1; i >= 0; i--) {
            if (blocks[i].type === 'tool_call' && blocks[i].toolName === toolName && blocks[i].toolStatus === 'running') {
              blocks[i].toolStatus = 'done'
              break
            }
          }
        } else if (event.type === 'resume_update') {
          blocks.push({ type: 'resume_update' })
          loadResume()
        }
      },
      // onDone
      () => {
        assistantMsg.loading = false
        sending.value = false
      },
      // onError
      (err) => {
        const blocks = assistantMsg.blocks!
        blocks.push({ type: 'text', content: `\n❌ 错误: ${err}` })
        assistantMsg.loading = false
        sending.value = false
      }
    )
  }

  return {
    sessions,
    currentSessionId,
    messages,
    resume,
    sending,
    loadSessions,
    newSession,
    selectSession,
    removeSession,
    sendMessage,
    loadResume,
  }
})
