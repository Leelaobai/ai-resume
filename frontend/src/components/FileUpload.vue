<template>
  <div class="file-upload" v-if="store.currentSessionId">
    <div
      class="drop-zone"
      :class="{ dragging }"
      @dragover.prevent="dragging = true"
      @dragleave="dragging = false"
      @drop.prevent="handleDrop"
      @click="triggerInput"
    >
      <input
        ref="fileInput"
        type="file"
        accept=".pdf,.docx,.doc"
        style="display: none"
        @change="handleSelect"
      />
      <div v-if="uploading" class="upload-status">
        <span class="spinner"></span> 解析中...
      </div>
      <div v-else>
        <p>📄 上传已有简历</p>
        <p class="hint">拖拽或点击，支持 PDF / DOCX</p>
      </div>
    </div>
    <p v-if="error" class="error-msg">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useChatStore } from '../stores/chat'

const store = useChatStore()
const fileInput = ref<HTMLInputElement>()
const dragging = ref(false)
const uploading = ref(false)
const error = ref('')

function triggerInput() {
  fileInput.value?.click()
}

function handleSelect(e: Event) {
  const input = e.target as HTMLInputElement
  if (input.files?.[0]) upload(input.files[0])
}

function handleDrop(e: DragEvent) {
  dragging.value = false
  if (e.dataTransfer?.files?.[0]) upload(e.dataTransfer.files[0])
}

async function upload(file: File) {
  if (!store.currentSessionId) return
  error.value = ''
  uploading.value = true

  try {
    const formData = new FormData()
    formData.append('file', file)

    const resp = await fetch(
      `http://localhost:8090/api/sessions/${store.currentSessionId}/upload`,
      { method: 'POST', body: formData }
    )

    if (!resp.ok) {
      const data = await resp.json()
      throw new Error(data.error || '上传失败')
    }

    const result = await resp.json()

    // 把解析结果发给Agent，让它智能合并到简历
    const parsed = JSON.stringify(result.data, null, 2)
    store.sendMessage(
      `我上传了一份已有的简历，以下是解析出的内容，请将其合并到当前简历中。如果某个部分已有内容，请智能合并而非覆盖。\n\n${parsed}`
    )
  } catch (e: any) {
    error.value = e.message
  } finally {
    uploading.value = false
  }
}
</script>

<style scoped>
.file-upload {
  padding: 8px 16px;
}

.drop-zone {
  border: 2px dashed #ccc;
  border-radius: 8px;
  padding: 16px;
  text-align: center;
  cursor: pointer;
  transition: all 0.2s;
}

.drop-zone:hover,
.drop-zone.dragging {
  border-color: #4a6cf7;
  background: rgba(74, 108, 247, 0.05);
}

.drop-zone p {
  margin: 0;
  font-size: 13px;
  color: #555;
}

.hint {
  color: #999 !important;
  font-size: 12px !important;
  margin-top: 4px !important;
}

.upload-status {
  color: #4a6cf7;
  font-size: 13px;
}

.spinner {
  display: inline-block;
  width: 14px;
  height: 14px;
  border: 2px solid #4a6cf7;
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  vertical-align: middle;
  margin-right: 6px;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.error-msg {
  color: #e74c3c;
  font-size: 12px;
  margin-top: 6px;
  text-align: center;
}
</style>
