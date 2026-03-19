<template>
  <div class="resume-preview">
    <div class="preview-header">
      <h3>简历预览</h3>
      <div class="preview-actions" v-if="store.currentSessionId">
        <div class="template-switcher">
          <button
            v-for="t in templates"
            :key="t.id"
            class="btn-template"
            :class="{ active: !isCustomTemplate && currentTemplate === t.id }"
            @click="switchTemplate(t.id)"
          >{{ t.label }}</button>
          <button
            v-if="hasCustomTemplate"
            class="btn-template"
            :class="{ active: isCustomTemplate }"
            @click="switchTemplate('custom')"
          >自定义</button>
        </div>
        <button v-if="hasCustomTemplate" class="btn-reset" @click="resetTemplate">
          重置样式
        </button>
        <button class="btn-export" @click="exportPDF" :disabled="exporting">
          {{ exporting ? '导出中...' : '导出 PDF' }}
        </button>
      </div>
    </div>

    <div class="preview-body" v-if="store.currentSessionId">
      <div class="iframe-wrapper">
        <iframe
          v-if="htmlUrl"
          :src="htmlUrl"
          class="resume-iframe"
          ref="iframeRef"
        ></iframe>
        <div v-else class="empty-resume">
          <p>简历内容为空</p>
          <p class="empty-hint">在左侧对话框描述你的经历，AI 会帮你生成简历</p>
        </div>
      </div>
    </div>

    <div class="preview-body" v-else>
      <div class="empty-resume">
        <p class="empty-icon">📄</p>
        <p>简历预览区域</p>
        <p class="empty-hint">创建会话后，简历将在这里实时展示</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { updateTemplate, deleteCustomTemplate } from '../api/client'

const store = useChatStore()
const exporting = ref(false)
const iframeRef = ref<HTMLIFrameElement>()
const refreshKey = ref(0)

const templates = [
  { id: 'classic', label: '经典' },
  { id: 'modern', label: '现代' },
  { id: 'minimal', label: '简约' },
]

const currentTemplate = computed(() => store.resume?.template_id || 'classic')
const hasCustomTemplate = computed(() => !!store.resume?.custom_template)
const isCustomTemplate = computed(() => currentTemplate.value === 'custom' && hasCustomTemplate.value)

const htmlUrl = computed(() => {
  if (!store.currentSessionId || !store.resume?.data) return ''
  // refreshKey forces iframe reload
  return `http://localhost:8090/api/sessions/${store.currentSessionId}/resume/html?_t=${refreshKey.value}`
})

// 当 resume 数据变化时刷新 iframe
watch(() => store.resume, () => {
  refreshKey.value++
}, { deep: true })

async function switchTemplate(templateId: string) {
  if (!store.currentSessionId || templateId === currentTemplate.value) return
  try {
    await updateTemplate(store.currentSessionId, templateId)
    await store.loadResume()
  } catch (e: any) {
    alert('切换模板失败: ' + e.message)
  }
}

async function resetTemplate() {
  if (!store.currentSessionId) return
  try {
    await deleteCustomTemplate(store.currentSessionId)
    await store.loadResume()
  } catch (e: any) {
    alert('重置样式失败: ' + e.message)
  }
}

async function exportPDF() {
  if (!store.currentSessionId || exporting.value) return
  exporting.value = true
  try {
    const resp = await fetch(
      `http://localhost:8090/api/sessions/${store.currentSessionId}/resume/pdf`
    )
    if (!resp.ok) throw new Error('导出失败')
    const blob = await resp.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'resume.pdf'
    a.click()
    URL.revokeObjectURL(url)
  } catch (e: any) {
    alert('PDF导出失败: ' + e.message)
  } finally {
    exporting.value = false
  }
}
</script>

<style scoped>
.resume-preview {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: #e8e8e8;
}

.preview-header {
  padding: 12px 16px;
  background: white;
  border-bottom: 1px solid #ddd;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.preview-header h3 {
  margin: 0;
  font-size: 14px;
  color: #333;
}

.preview-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.template-switcher {
  display: flex;
  gap: 0;
  border: 1px solid #ddd;
  border-radius: 6px;
  overflow: hidden;
}

.btn-template {
  background: white;
  border: none;
  border-right: 1px solid #ddd;
  padding: 5px 12px;
  font-size: 12px;
  cursor: pointer;
  color: #666;
  transition: all 0.15s;
}

.btn-template:last-child {
  border-right: none;
}

.btn-template:hover {
  background: #f5f5f5;
}

.btn-template.active {
  background: #4a6cf7;
  color: white;
}

.btn-reset {
  background: white;
  color: #666;
  border: 1px solid #ddd;
  padding: 5px 12px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 12px;
  transition: all 0.15s;
}

.btn-reset:hover {
  background: #f5f5f5;
  color: #333;
}

.btn-export {
  background: #4a6cf7;
  color: white;
  border: none;
  padding: 6px 14px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
}

.btn-export:hover:not(:disabled) {
  background: #3b5de7;
}

.btn-export:disabled {
  background: #ccc;
  cursor: not-allowed;
}

.preview-body {
  flex: 1;
  overflow: hidden;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 20px;
}

.iframe-wrapper {
  width: 210mm;
  height: 100%;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.15);
  background: white;
  flex-shrink: 0;
}

.resume-iframe {
  width: 100%;
  height: 100%;
  border: none;
  background: white;
}

.empty-resume {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 200px;
  height: 100%;
  color: #999;
  text-align: center;
  width: 100%;
}

.empty-icon {
  font-size: 48px;
  margin-bottom: 8px;
}

.empty-hint {
  font-size: 13px;
  color: #bbb;
  margin-top: 4px;
}
</style>
