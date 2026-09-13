<template>
  <div>
    <div class="avg">
      平均评分：<RatingStars :value="avgRating" />（{{ avgRating.toFixed(1) }}）
    </div>
    <el-divider />
    <EmptyState v-if="list.length === 0" text="暂无评论" />
    <el-card v-for="c in list" :key="c.id" shadow="never" class="comment-item">
      <div class="row">
        <RatingStars :value="c.rating" />
        <span class="time">
          <el-tag v-if="c.is_pinned" type="warning" size="small" class="pin-tag">置顶</el-tag>
          {{ formatDateTime(c.created_at) }}
        </span>
      </div>
      <div class="content">{{ c.content }}</div>
      <div class="actions">
        <template v-if="canPin">
          <el-button v-if="c.is_pinned" size="small" text @click="unpin(c.id)">取消置顶</el-button>
          <el-button v-else size="small" type="primary" text @click="pin(c.id)">置顶</el-button>
        </template>
        <RoleGuard :roles="['admin']">
          <el-button size="small" type="danger" text @click="remove(c.id)">删除</el-button>
        </RoleGuard>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { ElMessage } from 'element-plus'
import RatingStars from '@/components/common/RatingStars.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import RoleGuard from '@/components/common/RoleGuard.vue'
import { useCommentStore } from '@/stores/commentStore'
import { deleteComment, pinComment, unpinComment } from '@/api/comment'
import { useAuth } from '@/hooks/useAuth'
import { formatDateTime } from '@/utils/dateFormat'

const props = defineProps<{ activityId: number; organizerId?: number }>()
const store = useCommentStore()
const { list, avgRating } = storeToRefs(store)
const { auth, isAdmin } = useAuth()

// 仅活动组织者或管理员可置顶/取消置顶（与后端校验一致）
const canPin = computed(() => {
  if (!auth.isLoggedIn || !auth.user) return false
  if (isAdmin.value) return true
  return auth.user.role === 'organizer' && auth.user.id === props.organizerId
})

async function remove(id: number) {
  await deleteComment(id)
  ElMessage.success('已删除')
  await store.fetchList(props.activityId)
}

async function pin(id: number) {
  await pinComment(props.activityId, id)
  ElMessage.success('置顶成功')
  await store.fetchList(props.activityId)
}

async function unpin(id: number) {
  await unpinComment(props.activityId, id)
  ElMessage.success('已取消置顶')
  await store.fetchList(props.activityId)
}

onMounted(() => store.fetchList(props.activityId))
</script>

<style scoped>
.avg { display: flex; align-items: center; gap: 8px; }
.comment-item { margin-bottom: 8px; }
.row { display: flex; justify-content: space-between; }
.time { color: #909399; font-size: 12px; }
.pin-tag { margin-right: 6px; }
.content { margin-top: 6px; }
.actions { text-align: right; }
</style>
