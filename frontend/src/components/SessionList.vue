<template>
  <div class="session-list">
    <div class="session-header">
      <h3>会话列表</h3>
      <button class="btn-new" @click="store.newSession()">+ 新建</button>
    </div>
    <div class="session-items">
      <div
        v-for="session in store.sessions"
        :key="session.id"
        class="session-item"
        :class="{ active: session.id === store.currentSessionId }"
        @click="store.selectSession(session.id)"
      >
        <div class="session-info">
          <span class="session-title">{{ session.title }}</span>
          <span class="session-time">{{ formatTime(session.updated_at) }}</span>
        </div>
        <button
          class="btn-delete"
          @click.stop="confirmDelete(session.id)"
        >×</button>
      </div>
      <div v-if="store.sessions.length === 0" class="empty-hint">
        点击"新建"开始创建简历
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useChatStore } from '../stores/chat'

const store = useChatStore()
onMounted(() => store.loadSessions())

function confirmDelete(id: string) {
  if (confirm('确定删除这个会话吗？')) {
    store.removeSession(id)
  }
}

function formatTime(dateStr: string): string {
  if (!dateStr) return ''
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  const diffHour = Math.floor(diffMs / 3600000)
  const diffDay = Math.floor(diffMs / 86400000)

  if (diffMin < 1) return '刚刚'
  if (diffMin < 60) return `${diffMin}分钟前`
  if (diffHour < 24) return `${diffHour}小时前`
  if (diffDay < 7) return `${diffDay}天前`
  return `${date.getMonth() + 1}/${date.getDate()}`
}
</script>

<style scoped>
.session-list {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: #1a1a2e;
  color: #eee;
}

.session-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  border-bottom: 1px solid #333;
}

.session-header h3 {
  margin: 0;
  font-size: 14px;
}

.btn-new {
  background: #4a6cf7;
  color: white;
  border: none;
  padding: 6px 12px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
}

.btn-new:hover {
  background: #3b5de7;
}

.session-items {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.session-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  border-radius: 8px;
  cursor: pointer;
  margin-bottom: 4px;
  font-size: 13px;
}

.session-item:hover {
  background: #2a2a4a;
}

.session-item.active {
  background: #4a6cf7;
}

.session-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.session-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-time {
  font-size: 11px;
  color: #666;
}

.btn-delete {
  background: none;
  border: none;
  color: #999;
  font-size: 18px;
  cursor: pointer;
  padding: 0 4px;
}

.btn-delete:hover {
  color: #ff4444;
}

.empty-hint {
  text-align: center;
  color: #666;
  padding: 20px;
  font-size: 13px;
}
</style>
