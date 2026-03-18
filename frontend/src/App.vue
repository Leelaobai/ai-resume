<template>
  <div class="layout">
    <!-- 左侧：会话列表 -->
    <aside class="sidebar">
      <SessionList />
    </aside>

    <!-- 中间：聊天面板 -->
    <main class="chat-area">
      <ChatPanel />
    </main>

    <!-- 右侧：简历预览 -->
    <section class="preview-area">
      <ResumePreview />
    </section>
  </div>
</template>

<script setup lang="ts">
import SessionList from './components/SessionList.vue'
import ChatPanel from './components/ChatPanel.vue'
import ResumePreview from './components/ResumePreview.vue'
</script>

<style scoped>
.layout {
  display: flex;
  width: 100%;
  height: 100%;
}

.sidebar {
  width: 220px;
  flex-shrink: 0;
  border-right: 1px solid #e0e0e0;
}

.chat-area {
  flex: 1;
  min-width: 300px;
}

.preview-area {
  flex: 1.2;
  min-width: 400px;
  border-left: 1px solid #e0e0e0;
}
</style>
