<template>
  <div class="chat-panel">
    <div class="chat-messages" ref="messagesRef">
      <div v-if="!store.currentSessionId" class="chat-welcome">
        <div class="welcome-icon">📝</div>
        <h2>AI Resume Agent</h2>
        <p>通过对话，AI 帮你生成专业简历</p>
        <div class="welcome-steps">
          <div class="step">
            <span class="step-num">1</span>
            <span>新建会话，描述你的工作经历和技能</span>
          </div>
          <div class="step">
            <span class="step-num">2</span>
            <span>AI 自动提取信息，生成结构化简历</span>
          </div>
          <div class="step">
            <span class="step-num">3</span>
            <span>粘贴目标岗位 JD，获取针对性优化建议</span>
          </div>
          <div class="step">
            <span class="step-num">4</span>
            <span>一键导出 PDF，投递求职</span>
          </div>
        </div>
        <button class="btn-start" @click="store.newSession()">开始创建简历</button>
      </div>
      <div
        v-for="(msg, i) in store.messages"
        :key="i"
        class="message"
        :class="msg.role"
      >
        <div class="message-role">{{ msg.role === 'user' ? '你' : 'AI助手' }}</div>
        <div class="message-content">
          <span v-if="msg.loading" class="loading-dot">思考中...</span>
          <template v-else>
            <!-- 工具调用卡片 -->
            <div v-if="msg.toolCalls?.length" class="tool-calls">
              <div
                v-for="(tc, j) in msg.toolCalls"
                :key="j"
                class="tool-card"
                :class="tc.status"
              >
                <span class="tool-icon">{{ toolIcon(tc.name) }}</span>
                <span class="tool-label">{{ toolLabel(tc.name) }}</span>
                <span v-if="tc.status === 'running'" class="tool-spinner"></span>
                <span v-else class="tool-check">&#10003;</span>
              </div>
            </div>
            <!-- 文本内容 -->
            <div v-if="msg.content" class="markdown-body" v-html="renderMarkdown(msg.content)"></div>
          </template>
        </div>
      </div>
    </div>

    <FileUpload v-if="store.currentSessionId" />

    <div class="chat-input" v-if="store.currentSessionId">
      <textarea
        v-model="input"
        @compositionstart="composing = true"
        @compositionend="composing = false"
        @keydown.enter.exact="handleSend"
        placeholder="描述你的工作经历、技能，或粘贴目标岗位JD进行匹配优化..."
        rows="3"
        :disabled="store.sending"
      />
      <button @click="handleSend" :disabled="store.sending || !input.trim()">
        {{ store.sending ? '生成中...' : '发送' }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useChatStore } from '../stores/chat'
import FileUpload from './FileUpload.vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

// marked 配置
marked.setOptions({
  breaks: true,
  gfm: true,
})

const store = useChatStore()
const input = ref('')
const messagesRef = ref<HTMLElement>()
const composing = ref(false)

function handleSend() {
  if (composing.value) return
  const msg = input.value.trim()
  if (!msg || store.sending) return
  input.value = ''
  store.sendMessage(msg)
}

function renderMarkdown(content: string): string {
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
}

const TOOL_META: Record<string, { icon: string; label: string }> = {
  extract_user_info: { icon: '💾', label: '保存记忆' },
  update_resume_section: { icon: '📝', label: '更新简历' },
  get_current_resume: { icon: '📄', label: '读取简历' },
  match_jd: { icon: '🎯', label: 'JD匹配分析' },
}

function toolIcon(name: string): string {
  return TOOL_META[name]?.icon || '🔧'
}

function toolLabel(name: string): string {
  return TOOL_META[name]?.label || name
}

// 消息更新时自动滚到底部
watch(
  () => store.messages.length + (store.messages.at(-1)?.content?.length || 0),
  async () => {
    await nextTick()
    if (messagesRef.value) {
      messagesRef.value.scrollTop = messagesRef.value.scrollHeight
    }
  }
)
</script>

<style scoped>
.chat-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: #f8f9fa;
}

.chat-messages {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}

.chat-welcome {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #666;
  text-align: center;
  padding: 40px 20px;
}

.welcome-icon {
  font-size: 48px;
  margin-bottom: 12px;
}

.chat-welcome h2 {
  margin: 0 0 8px 0;
  font-size: 22px;
  color: #333;
  font-weight: 600;
}

.chat-welcome > p {
  margin: 0 0 24px 0;
  font-size: 14px;
  color: #888;
}

.welcome-steps {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 28px;
  text-align: left;
}

.step {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
  color: #555;
}

.step-num {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: #4a6cf7;
  color: white;
  font-size: 12px;
  font-weight: 600;
  flex-shrink: 0;
}

.btn-start {
  background: #4a6cf7;
  color: white;
  border: none;
  padding: 10px 24px;
  border-radius: 8px;
  cursor: pointer;
  font-size: 15px;
  font-weight: 500;
  transition: background 0.2s;
}

.btn-start:hover {
  background: #3b5de7;
}

.message {
  margin-bottom: 16px;
  max-width: 85%;
}

.message.user {
  margin-left: auto;
}

.message.assistant {
  margin-right: auto;
}

.message-role {
  font-size: 12px;
  color: #888;
  margin-bottom: 4px;
}

.message.user .message-role {
  text-align: right;
}

.message-content {
  padding: 10px 14px;
  border-radius: 12px;
  font-size: 14px;
  line-height: 1.6;
}

.message.user .message-content {
  background: #4a6cf7;
  color: white;
  border-bottom-right-radius: 4px;
}

.message.assistant .message-content {
  background: white;
  color: #333;
  border-bottom-left-radius: 4px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

/* 工具调用卡片 */
.tool-calls {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}

.tool-card {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-radius: 6px;
  font-size: 12px;
  background: #f0f4ff;
  color: #4a6cf7;
  border: 1px solid #dbe4ff;
}

.tool-card.done {
  background: #f0fdf4;
  color: #16a34a;
  border-color: #bbf7d0;
}

.tool-icon {
  font-size: 14px;
}

.tool-spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid #4a6cf7;
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.tool-check {
  color: #16a34a;
  font-weight: bold;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Markdown 内容样式 */
.markdown-body :deep(p) {
  margin: 0 0 8px 0;
}

.markdown-body :deep(p:last-child) {
  margin-bottom: 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  margin: 4px 0;
  padding-left: 20px;
}

.markdown-body :deep(li) {
  margin-bottom: 2px;
}

.markdown-body :deep(code) {
  background: rgba(0, 0, 0, 0.06);
  padding: 2px 4px;
  border-radius: 3px;
  font-size: 13px;
}

.markdown-body :deep(pre) {
  background: #f6f8fa;
  padding: 10px;
  border-radius: 6px;
  overflow-x: auto;
  margin: 8px 0;
}

.markdown-body :deep(pre code) {
  background: none;
  padding: 0;
}

.markdown-body :deep(strong) {
  font-weight: 600;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3) {
  margin: 12px 0 6px 0;
  font-weight: 600;
}

.markdown-body :deep(h1) { font-size: 18px; }
.markdown-body :deep(h2) { font-size: 16px; }
.markdown-body :deep(h3) { font-size: 15px; }

.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid #e5e7eb;
  margin: 12px 0;
}

.markdown-body :deep(table) {
  border-collapse: collapse;
  width: 100%;
  margin: 8px 0;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid #e5e7eb;
  padding: 6px 10px;
  font-size: 13px;
}

.markdown-body :deep(th) {
  background: #f9fafb;
  font-weight: 600;
}

.loading-dot {
  color: #999;
  animation: blink 1s infinite;
}

@keyframes blink {
  50% { opacity: 0.3; }
}

.chat-input {
  display: flex;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid #e0e0e0;
  background: white;
}

.chat-input textarea {
  flex: 1;
  border: 1px solid #ddd;
  border-radius: 8px;
  padding: 10px;
  font-size: 14px;
  resize: none;
  outline: none;
  font-family: inherit;
}

.chat-input textarea:focus {
  border-color: #4a6cf7;
}

.chat-input button {
  background: #4a6cf7;
  color: white;
  border: none;
  padding: 0 20px;
  border-radius: 8px;
  cursor: pointer;
  font-size: 14px;
  white-space: nowrap;
}

.chat-input button:disabled {
  background: #ccc;
  cursor: not-allowed;
}

.chat-input button:not(:disabled):hover {
  background: #3b5de7;
}
</style>
